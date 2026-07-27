package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ladderairport/panel/internal/dnsprovider"
	"github.com/ladderairport/panel/internal/secretstore"
	"github.com/ladderairport/panel/internal/store"
)

type dnsAccountRequest struct {
	Name        string            `json:"name"`
	Provider    string            `json:"provider"`
	Zone        string            `json:"zone"`
	Credentials map[string]string `json:"credentials"`
	Settings    map[string]any    `json:"settings"`
	Enabled     *bool             `json:"enabled"`
}

type managedDomainRequest struct {
	NodeID        string `json:"node_id"`
	DNSAccountID  string `json:"dns_account_id"`
	Zone          string `json:"zone"`
	FQDN          string `json:"fqdn"`
	RecordMode    string `json:"record_mode"`
	AddressSource string `json:"address_source"`
	ManualIPv4    string `json:"manual_ipv4"`
	ManualIPv6    string `json:"manual_ipv6"`
	TTL           int    `json:"ttl"`
	Enabled       *bool  `json:"enabled"`
}

func (s *Server) handleListDNSProviders(w http.ResponseWriter, _ *http.Request) {
	if s.DNSProviders == nil {
		writeError(w, http.StatusServiceUnavailable, "DNS 供应商注册表不可用")
		return
	}
	writeJSON(w, http.StatusOK, s.DNSProviders.Metadata())
}

func (s *Server) handleListDNSAccounts(w http.ResponseWriter, _ *http.Request) {
	accounts, err := s.Store.ListDNSAccounts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, accounts)
}

func (s *Server) handleCreateDNSAccount(w http.ResponseWriter, r *http.Request) {
	if !s.dnsReady(w) {
		return
	}
	var request dnsAccountRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "JSON 请求体无效")
		return
	}
	account, err := s.buildDNSAccount(nil, request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.CreateDNSAccount(account); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, account)
}

func (s *Server) handleUpdateDNSAccount(w http.ResponseWriter, r *http.Request) {
	if !s.dnsReady(w) {
		return
	}
	current, err := s.Store.GetDNSAccount(pathID(r))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var request dnsAccountRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "JSON 请求体无效")
		return
	}
	account, err := s.buildDNSAccount(current, request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.UpdateDNSAccount(account); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) handleDeleteDNSAccount(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteDNSAccount(pathID(r)); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTestDNSAccount(w http.ResponseWriter, r *http.Request) {
	if !s.dnsReady(w) {
		return
	}
	account, err := s.Store.GetDNSAccount(pathID(r))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	credentials, err := s.decryptDNSCredentials(account)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	provider, err := s.DNSProviders.New(account.Provider, dnsprovider.Config{
		Credentials: credentials,
		Settings:    account.Settings,
		Zone:        account.Zone,
		HTTPTimeout: 15 * time.Second,
	})
	if err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		err = provider.Test(ctx)
	}
	account.LastTestUnix = time.Now().Unix()
	if err != nil {
		account.LastTestError = secretstore.Redact(err.Error(), credentialValues(credentials)...)
		_ = s.Store.UpdateDNSAccount(account)
		writeError(w, http.StatusBadGateway, account.LastTestError)
		return
	}
	account.LastTestError = ""
	if err := s.Store.UpdateDNSAccount(account); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tested_at_unix": account.LastTestUnix})
}

func (s *Server) buildDNSAccount(current *store.DNSAccount, request dnsAccountRequest) (*store.DNSAccount, error) {
	name := strings.TrimSpace(request.Name)
	providerName := strings.ToLower(strings.TrimSpace(request.Provider))
	if current != nil {
		if name == "" {
			name = current.Name
		}
		if providerName == "" {
			providerName = current.Provider
		}
	}
	if name == "" || providerName == "" {
		return nil, fmt.Errorf("必须提供 DNS 账号名称和供应商")
	}
	zoneInput := strings.TrimSpace(request.Zone)
	if zoneInput == "" && current != nil {
		zoneInput = current.Zone
	}
	zone, err := dnsprovider.NormalizeFQDN(zoneInput)
	if err != nil {
		return nil, fmt.Errorf("DNS 账号区域无效：%w", err)
	}
	if current != nil && providerName != current.Provider && len(request.Credentials) == 0 {
		return nil, fmt.Errorf("更换供应商时必须重新提供凭据")
	}
	settings := request.Settings
	if settings == nil && current != nil {
		settings = current.Settings
	}
	if settings == nil {
		settings = map[string]any{}
	}
	delete(settings, "test_zone")
	enabled := true
	if current != nil {
		enabled = current.Enabled
	}
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	id := uuid.NewString()
	ciphertext := ""
	if current != nil {
		id = current.ID
		ciphertext = current.CredentialsCiphertext
	}
	if current == nil || len(request.Credentials) > 0 {
		// Factory validation catches missing required fields before encryption.
		if _, err := s.DNSProviders.New(providerName, dnsprovider.Config{
			Credentials: request.Credentials, Settings: settings, Zone: zone,
		}); err != nil {
			return nil, err
		}
		raw, err := json.Marshal(request.Credentials)
		if err != nil {
			return nil, fmt.Errorf("编码 DNS 凭据失败")
		}
		ciphertext, err = s.Secrets.Encrypt(raw, dnsAccountAAD(id, providerName))
		if err != nil {
			return nil, err
		}
	}
	return &store.DNSAccount{
		ID:                    id,
		Name:                  name,
		Provider:              providerName,
		Zone:                  zone,
		CredentialsCiphertext: ciphertext,
		HasCredentials:        ciphertext != "",
		Settings:              settings,
		Enabled:               enabled,
		CreatedAtUnix:         valueOrZero(current, func(a *store.DNSAccount) int64 { return a.CreatedAtUnix }),
	}, nil
}

func (s *Server) decryptDNSCredentials(account *store.DNSAccount) (map[string]string, error) {
	if account == nil || account.CredentialsCiphertext == "" {
		return nil, fmt.Errorf("DNS 账号没有可用凭据")
	}
	raw, err := s.Secrets.Decrypt(
		account.CredentialsCiphertext,
		dnsAccountAAD(account.ID, account.Provider),
	)
	if err != nil {
		return nil, fmt.Errorf("解密 DNS 账号凭据失败：%w", err)
	}
	credentials := map[string]string{}
	if err := json.Unmarshal(raw, &credentials); err != nil {
		return nil, fmt.Errorf("解析 DNS 账号凭据失败")
	}
	return credentials, nil
}

func dnsAccountAAD(id, provider string) string {
	return "dns-account:" + id + ":" + provider
}

func credentialValues(credentials map[string]string) []string {
	out := make([]string, 0, len(credentials))
	for _, value := range credentials {
		out = append(out, value)
	}
	return out
}

func valueOrZero[T any](value *T, getter func(*T) int64) int64 {
	if value == nil {
		return 0
	}
	return getter(value)
}

func (s *Server) dnsReady(w http.ResponseWriter) bool {
	if s.Secrets == nil || s.DNSProviders == nil {
		writeError(w, http.StatusServiceUnavailable, "DNS 自动化尚未初始化")
		return false
	}
	return true
}

func (s *Server) handleListManagedDomains(w http.ResponseWriter, _ *http.Request) {
	domains, err := s.Store.ListManagedDomains()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, domains)
}

func (s *Server) handleCreateManagedDomain(w http.ResponseWriter, r *http.Request) {
	var request managedDomainRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "JSON 请求体无效")
		return
	}
	domain, err := s.buildManagedDomain(nil, request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.CreateManagedDomain(domain); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if _, err := s.enqueueDomainReconcile(domain.ID, true); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, domain)
}

func (s *Server) handleUpdateManagedDomain(w http.ResponseWriter, r *http.Request) {
	current, err := s.Store.GetManagedDomain(pathID(r))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var request managedDomainRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "JSON 请求体无效")
		return
	}
	domain, err := s.buildManagedDomain(current, request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.UpdateManagedDomain(domain); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if domain.Enabled {
		_, _ = s.enqueueDomainReconcile(domain.ID, true)
	}
	writeJSON(w, http.StatusOK, domain)
}

func (s *Server) handleDeleteManagedDomain(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	domain, err := s.Store.GetManagedDomain(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	certificates, bindings, err := s.Store.ManagedDomainTLSUsage(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if certificates > 0 || bindings > 0 {
		writeError(w, http.StatusConflict, fmt.Sprintf(
			"该域名仍关联 %d 个协议证书和 %d 个 TLS 绑定，请先解除关联",
			certificates, bindings,
		))
		return
	}
	if (domain.CreatedAByPanel || domain.CreatedAAAAByPanel) &&
		r.URL.Query().Get("delete_records") != "true" {
		writeError(w, http.StatusConflict, "该域名包含 Panel 创建的记录；请明确设置 delete_records=true 或先禁用记录管理")
		return
	}
	// External deletion is handled by a cleanup job in the reconciler phase.
	if domain.CreatedAByPanel || domain.CreatedAAAAByPanel {
		domain.Enabled = false
		domain.State = "deleting"
		if err := s.Store.UpdateManagedDomain(domain); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		job, err := s.Store.EnqueueAutomationJob(&store.AutomationJob{
			Type: "dns.cleanup", TargetType: "managed_domain", TargetID: id,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, job)
		return
	}
	if err := s.Store.DeleteManagedDomain(id); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleReconcileManagedDomain(w http.ResponseWriter, r *http.Request) {
	if _, err := s.Store.GetManagedDomain(pathID(r)); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	job, err := s.enqueueDomainReconcile(pathID(r), true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) enqueueDomainReconcile(id string, force bool) (*store.AutomationJob, error) {
	return s.Store.EnqueueAutomationJob(&store.AutomationJob{
		Type: "dns.reconcile", TargetType: "managed_domain", TargetID: id,
		Payload: map[string]any{"force": force},
	})
}

func (s *Server) buildManagedDomain(current *store.ManagedDomain, request managedDomainRequest) (*store.ManagedDomain, error) {
	domain := &store.ManagedDomain{}
	if current != nil {
		*domain = *current
	}
	if value := strings.TrimSpace(request.NodeID); value != "" {
		domain.NodeID = value
	}
	if value := strings.TrimSpace(request.DNSAccountID); value != "" {
		domain.DNSAccountID = value
	}
	if domain.NodeID == "" || domain.DNSAccountID == "" {
		return nil, fmt.Errorf("必须提供节点和 DNS 账号")
	}
	if _, err := s.Store.GetNode(domain.NodeID); err != nil {
		return nil, err
	}
	account, err := s.Store.GetDNSAccount(domain.DNSAccountID)
	if err != nil {
		return nil, err
	}
	if account.Zone == "" {
		return nil, fmt.Errorf("DNS 账号尚未配置区域，请先编辑账号")
	}
	fqdnInput := request.FQDN
	if strings.TrimSpace(fqdnInput) == "" {
		fqdnInput = domain.FQDN
	}
	fqdn, err := managedFQDN(fqdnInput, account.Zone)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.Zone) != "" {
		requestedZone, err := dnsprovider.NormalizeFQDN(request.Zone)
		if err != nil {
			return nil, err
		}
		if requestedZone != account.Zone {
			return nil, fmt.Errorf("托管域名区域由 DNS 账号决定，当前账号区域为 %s", account.Zone)
		}
	}
	if _, err := dnsprovider.RelativeName(fqdn, account.Zone); err != nil {
		return nil, err
	}
	domain.FQDN = fqdn
	domain.Zone = account.Zone
	if current != nil && (current.CreatedAByPanel || current.CreatedAAAAByPanel) &&
		(domain.DNSAccountID != current.DNSAccountID ||
			domain.Zone != current.Zone || domain.FQDN != current.FQDN) {
		return nil, fmt.Errorf("该域名已有 Panel 创建的记录；更换账号、区域或域名前请先显式删除旧记录")
	}
	if request.RecordMode != "" {
		domain.RecordMode = strings.ToLower(strings.TrimSpace(request.RecordMode))
	}
	if domain.RecordMode == "" {
		domain.RecordMode = "a"
	}
	switch domain.RecordMode {
	case "a", "aaaa", "dual":
	default:
		return nil, fmt.Errorf("记录模式必须为 a、aaaa 或 dual")
	}
	if request.AddressSource != "" {
		domain.AddressSource = strings.ToLower(strings.TrimSpace(request.AddressSource))
	}
	if domain.AddressSource == "" {
		domain.AddressSource = "manual"
	}
	switch domain.AddressSource {
	case "manual", "node_address", "agent_public":
	default:
		return nil, fmt.Errorf("地址来源无效")
	}
	if request.ManualIPv4 != "" || current == nil {
		domain.ManualIPv4 = strings.TrimSpace(request.ManualIPv4)
	}
	if request.ManualIPv6 != "" || current == nil {
		domain.ManualIPv6 = strings.TrimSpace(request.ManualIPv6)
	}
	if domain.AddressSource == "manual" {
		if (domain.RecordMode == "a" || domain.RecordMode == "dual") &&
			!validAddressFamily(domain.ManualIPv4, true) {
			return nil, fmt.Errorf("必须提供有效的手工 IPv4 地址")
		}
		if (domain.RecordMode == "aaaa" || domain.RecordMode == "dual") &&
			!validAddressFamily(domain.ManualIPv6, false) {
			return nil, fmt.Errorf("必须提供有效的手工 IPv6 地址")
		}
	}
	if request.TTL > 0 {
		domain.TTL = request.TTL
	}
	if domain.TTL == 0 {
		domain.TTL = 300
	}
	if domain.TTL < 60 || domain.TTL > 86400 {
		return nil, fmt.Errorf("TTL 必须在 60 到 86400 秒之间")
	}
	if current == nil {
		domain.ID = uuid.NewString()
		domain.Enabled = true
		domain.State = "pending"
		domain.ObservedIPv4 = []string{}
		domain.ObservedIPv6 = []string{}
	}
	if request.Enabled != nil {
		domain.Enabled = *request.Enabled
	}
	if domain.Enabled && domain.State == "disabled" {
		domain.State = "pending"
	}
	if !domain.Enabled {
		domain.State = "disabled"
	}
	return domain, nil
}

func managedFQDN(value, zone string) (string, error) {
	value = strings.Trim(strings.TrimSpace(value), ".")
	comparison := strings.ToLower(value)
	if comparison != zone && !strings.HasSuffix(comparison, "."+zone) {
		value += "." + zone
	}
	normalized, err := dnsprovider.NormalizeFQDN(value)
	if err != nil {
		return "", err
	}
	if _, err := dnsprovider.RelativeName(normalized, zone); err != nil {
		return "", err
	}
	return normalized, nil
}

func validAddressFamily(value string, ipv4 bool) bool {
	address, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	if ipv4 {
		return address.Is4()
	}
	return address.Is6()
}
