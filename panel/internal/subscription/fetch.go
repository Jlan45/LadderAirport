package subscription

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	FetchTimeout = 15 * time.Second
	MaxBodyBytes = 4 << 20 // 4 MiB
	MaxRedirects = 3
	defaultUA    = "LadderAirport-Panel/1.0"
)

// FetchURL downloads a subscription body with SSRF protections.
func FetchURL(ctx context.Context, rawURL string, headers map[string]string) ([]byte, error) {
	u, err := parsePublicHTTPURL(rawURL)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout:       FetchTimeout,
		CheckRedirect: redirectChecker(headers),
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(address)
				if err != nil {
					host = address
					port = ""
				}
				ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil {
					return nil, err
				}
				var lastErr error
				d := net.Dialer{Timeout: FetchTimeout}
				for _, ipa := range ips {
					if isBlockedIP(ipa.IP) {
						lastErr = fmt.Errorf("地址 %s 已被安全策略拦截", ipa.IP)
						continue
					}
					addr := ipa.IP.String()
					if port != "" {
						addr = net.JoinHostPort(addr, port)
					}
					conn, err := d.DialContext(ctx, network, addr)
					if err != nil {
						lastErr = err
						continue
					}
					return conn, nil
				}
				if lastErr == nil {
					lastErr = fmt.Errorf("主机 %s 没有可安全访问的地址", host)
				}
				return nil, lastErr
			},
			// Disable HTTP/2 optional; keep defaults otherwise.
			ForceAttemptHTTP2: true,
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", defaultUA)
	req.Header.Set("Accept", "*/*")
	for k, v := range headers {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("获取外部内容失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("获取外部内容失败：HTTP %s", resp.Status)
	}
	limited := io.LimitReader(resp.Body, MaxBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("读取外部内容失败：%w", err)
	}
	if len(body) > MaxBodyBytes {
		return nil, fmt.Errorf("外部内容超过 %d 字节限制", MaxBodyBytes)
	}
	return body, nil
}

// redirectChecker validates redirect targets and strips custom headers (they
// often carry subscription tokens) when a redirect crosses hosts.
func redirectChecker(headers map[string]string) func(req *http.Request, via []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= MaxRedirects {
			return fmt.Errorf("重定向次数过多")
		}
		if err := validatePublicURL(req.URL); err != nil {
			return err
		}
		if !strings.EqualFold(req.URL.Hostname(), via[0].URL.Hostname()) {
			for k := range headers {
				req.Header.Del(k)
			}
		}
		return nil
	}
}

func parsePublicHTTPURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("必须提供 URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("URL 无效：%w", err)
	}
	if err := validatePublicURL(u); err != nil {
		return nil, err
	}
	return u, nil
}

func validatePublicURL(u *url.URL) error {
	if u == nil {
		return fmt.Errorf("URL 不能为空")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("不支持 URL 方案 %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL 缺少主机名")
	}
	// Block obvious local hostnames without DNS.
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") ||
		lower == "metadata.google.internal" {
		return fmt.Errorf("主机 %q 已被安全策略拦截", host)
	}
	// If host is a literal IP, check immediately.
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("地址 %s 已被安全策略拦截", ip)
		}
	}
	return nil
}

func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	// CGNAT 100.64.0.0/10
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
		// AWS/GCP metadata
		if ip4[0] == 169 && ip4[1] == 254 {
			return true
		}
	}
	return false
}
