package dnsproviders

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ladderairport/panel/internal/dnsprovider"
)

type callbackProvider struct {
	rawURL              string
	method              string
	headers             map[string]string
	bodyTemplate        string
	successStatuses     map[int]bool
	responseContains    string
	credentials         map[string]string
	defaultZone         string
	timeout             time.Duration
	allowPrivateNetwork bool
}

type callbackResponse struct {
	ID      string               `json:"id"`
	Records []dnsprovider.Record `json:"records"`
}

func newCallback(config dnsprovider.Config) (dnsprovider.Provider, error) {
	rawURL := stringSetting(config.Settings, "url")
	if rawURL == "" {
		return nil, fmt.Errorf("必须提供 Callback URL")
	}
	method := strings.ToUpper(stringSetting(config.Settings, "method"))
	if method == "" {
		method = http.MethodPost
	}
	if method != http.MethodGet && method != http.MethodPost && method != http.MethodPut &&
		method != http.MethodPatch && method != http.MethodDelete {
		return nil, fmt.Errorf("Callback HTTP 方法无效：%s", method)
	}
	headers := map[string]string{}
	if rawHeaders, ok := config.Settings["headers"].(map[string]any); ok {
		for key, value := range rawHeaders {
			headers[key] = fmt.Sprint(value)
		}
	}
	if rawHeaders, ok := config.Settings["headers"].(map[string]string); ok {
		for key, value := range rawHeaders {
			headers[key] = value
		}
	}
	statuses := map[int]bool{}
	switch raw := config.Settings["success_statuses"].(type) {
	case []any:
		for _, value := range raw {
			if code, err := strconv.Atoi(fmt.Sprint(value)); err == nil {
				statuses[code] = true
			}
		}
	case []int:
		for _, code := range raw {
			statuses[code] = true
		}
	}
	timeout := config.HTTPTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	allowPrivate, _ := config.Settings["allow_private_network"].(bool)
	provider := &callbackProvider{
		rawURL:              rawURL,
		method:              method,
		headers:             headers,
		bodyTemplate:        stringSetting(config.Settings, "body"),
		successStatuses:     statuses,
		responseContains:    stringSetting(config.Settings, "response_contains"),
		credentials:         config.Credentials,
		defaultZone:         stringSetting(config.Settings, "test_zone"),
		timeout:             timeout,
		allowPrivateNetwork: allowPrivate,
	}
	// Validate static URL syntax immediately. DNS/IP policy is enforced against
	// the expanded URL immediately before dialing.
	if !strings.Contains(rawURL, "#{") {
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Hostname() == "" || parsed.User != nil {
			return nil, fmt.Errorf("Callback URL 无效")
		}
		if parsed.Scheme != "https" && !(allowPrivate && parsed.Scheme == "http") {
			return nil, fmt.Errorf("Callback URL 必须使用 HTTPS")
		}
	}
	return provider, nil
}

func (p *callbackProvider) Test(ctx context.Context) error {
	if p.defaultZone == "" {
		return fmt.Errorf("测试 Callback 时必须提供 test_zone")
	}
	_, err := p.Lookup(ctx, dnsprovider.Zone{Name: p.defaultZone}, "@", dnsprovider.TypeA)
	return err
}

func (p *callbackProvider) ResolveZone(_ context.Context, fqdn string) (dnsprovider.Zone, error) {
	if p.defaultZone == "" {
		return dnsprovider.Zone{}, fmt.Errorf("必须明确提供 DNS 区域")
	}
	zone, err := dnsprovider.NormalizeFQDN(p.defaultZone)
	if err != nil {
		return dnsprovider.Zone{}, err
	}
	normalized, err := dnsprovider.NormalizeFQDN(fqdn)
	if err != nil {
		return dnsprovider.Zone{}, err
	}
	if normalized != zone && !strings.HasSuffix(normalized, "."+zone) {
		return dnsprovider.Zone{}, fmt.Errorf("域名 %s 不属于区域 %s", normalized, zone)
	}
	return dnsprovider.Zone{Name: zone}, nil
}

func (p *callbackProvider) Lookup(
	ctx context.Context,
	zone dnsprovider.Zone,
	name string,
	typ dnsprovider.RecordType,
) ([]dnsprovider.Record, error) {
	response, err := p.do(ctx, map[string]string{
		"action": "lookup", "zone": zone.Name, "name": name, "type": string(typ),
	})
	if err != nil {
		return nil, err
	}
	return response.Records, nil
}

func (p *callbackProvider) Upsert(
	ctx context.Context,
	zone dnsprovider.Zone,
	record dnsprovider.Record,
) (dnsprovider.RecordRef, error) {
	response, err := p.do(ctx, map[string]string{
		"action": "upsert", "zone": zone.Name, "name": record.Name,
		"type": string(record.Type), "value": record.Value,
		"ttl": strconv.FormatInt(int64(record.TTL/time.Second), 10),
	})
	if err != nil {
		return dnsprovider.RecordRef{}, err
	}
	return dnsprovider.RecordRef{
		ID: response.ID, Name: record.Name, Type: record.Type, Value: record.Value,
	}, nil
}

func (p *callbackProvider) Delete(
	ctx context.Context,
	zone dnsprovider.Zone,
	ref dnsprovider.RecordRef,
) error {
	_, err := p.do(ctx, map[string]string{
		"action": "delete", "zone": zone.Name, "name": ref.Name,
		"type": string(ref.Type), "value": ref.Value, "id": ref.ID,
	})
	return err
}

func (p *callbackProvider) do(ctx context.Context, values map[string]string) (callbackResponse, error) {
	expandedURL := p.expand(p.rawURL, values)
	parsedURL, dialAddress, err := p.validateURL(ctx, expandedURL)
	if err != nil {
		return callbackResponse{}, err
	}
	bodyText := p.expand(p.bodyTemplate, values)
	if bodyText == "" && p.method != http.MethodGet {
		body, marshalErr := json.Marshal(values)
		if marshalErr != nil {
			return callbackResponse{}, marshalErr
		}
		bodyText = string(body)
	}
	var body io.Reader
	if bodyText != "" {
		body = bytes.NewBufferString(bodyText)
	}
	requestCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, p.method, parsedURL.String(), body)
	if err != nil {
		return callbackResponse{}, fmt.Errorf("创建 Callback 请求失败：%w", err)
	}
	for key, value := range p.headers {
		request.Header.Set(key, p.expand(value, values))
	}
	if bodyText != "" && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", "application/json")
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: p.timeout}).DialContext(ctx, network, dialAddress)
		},
		ForceAttemptHTTP2: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   p.timeout,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			return fmt.Errorf("Callback 不允许 HTTP 重定向")
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return callbackResponse{}, &dnsprovider.ProviderError{
			Kind: dnsprovider.ErrorTransient, Provider: "callback",
			Operation: "发送请求", Message: "请求失败", Cause: err,
		}
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return callbackResponse{}, fmt.Errorf("读取 Callback 响应失败：%w", err)
	}
	if !p.statusAccepted(response.StatusCode) {
		return callbackResponse{}, &dnsprovider.ProviderError{
			Kind: dnsprovider.ErrorPermanent, Provider: "callback",
			Operation: "检查响应", Message: fmt.Sprintf("HTTP %d", response.StatusCode),
		}
	}
	if p.responseContains != "" && !bytes.Contains(raw, []byte(p.responseContains)) {
		return callbackResponse{}, fmt.Errorf("Callback 响应不包含成功标记")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return callbackResponse{}, nil
	}
	var decoded callbackResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return callbackResponse{}, fmt.Errorf("解析 Callback JSON 响应失败：%w", err)
	}
	return decoded, nil
}

func (p *callbackProvider) validateURL(ctx context.Context, value string) (*url.URL, string, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" {
		return nil, "", fmt.Errorf("Callback URL 无效")
	}
	if parsed.User != nil {
		return nil, "", fmt.Errorf("Callback URL 不允许内嵌认证信息")
	}
	if parsed.Scheme != "https" && !(p.allowPrivateNetwork && parsed.Scheme == "http") {
		return nil, "", fmt.Errorf("Callback URL 必须使用 HTTPS")
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", parsed.Hostname())
	if err != nil {
		return nil, "", fmt.Errorf("解析 Callback 主机失败：%w", err)
	}
	var selected netip.Addr
	for _, ip := range ips {
		if !p.allowPrivateNetwork && unsafeCallbackAddress(ip) {
			continue
		}
		selected = ip
		break
	}
	if !selected.IsValid() {
		return nil, "", fmt.Errorf("Callback 目标地址被安全策略拒绝")
	}
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return parsed, net.JoinHostPort(selected.String(), port), nil
}

func (p *callbackProvider) expand(template string, values map[string]string) string {
	for key, value := range values {
		template = strings.ReplaceAll(template, "#{"+key+"}", value)
	}
	for key, value := range p.credentials {
		template = strings.ReplaceAll(template, "#{credential."+key+"}", value)
	}
	return template
}

func (p *callbackProvider) statusAccepted(status int) bool {
	if len(p.successStatuses) == 0 {
		return status >= 200 && status < 300
	}
	return p.successStatuses[status]
}

func unsafeCallbackAddress(address netip.Addr) bool {
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() ||
		address.IsLoopback() || address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() || address.IsMulticast() ||
		address.IsUnspecified() {
		return true
	}
	cgnat := netip.MustParsePrefix("100.64.0.0/10")
	return cgnat.Contains(address)
}
