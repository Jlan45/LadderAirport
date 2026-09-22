// Package dnsreconcile converges managed A/AAAA records to node addresses.
package dnsreconcile

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/ladderairport/panel/internal/dnsprovider"
	"github.com/ladderairport/panel/internal/secretstore"
	"github.com/ladderairport/panel/internal/store"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
)

const reconcileInterval = 5 * time.Minute

type Agent interface {
	Close() error
	GetPublicAddresses(ctx context.Context, ipv4, ipv6 bool) (*agentv1.GetPublicAddressesResponse, error)
}

type DialAgent func(ctx context.Context, node store.Node, token string) (Agent, error)

type Service struct {
	Store        *store.Store
	Secrets      *secretstore.Store
	Providers    *dnsprovider.Registry
	DialAgent    DialAgent
	DefaultToken func() string
	Timeout      time.Duration
	Now          func() time.Time
}

func (s *Service) Reconcile(ctx context.Context, domainID string) error {
	domain, err := s.Store.GetManagedDomain(domainID)
	if err != nil {
		return err
	}
	if !domain.Enabled {
		return nil
	}
	now := s.now()
	if domain.AddressSource == "agent_public" {
		node, err := s.Store.GetNode(domain.NodeID)
		if err != nil {
			return err
		}
		if !node.DDNSEnabled {
			// Node-level DDNS switch is off: skip the probe entirely but keep
			// the regular schedule so reconcile resumes once re-enabled.
			domain.State = "paused"
			domain.RetryCount = 0
			domain.LastError = "节点已关闭 DDNS 自动解析，公网探测已暂停"
			domain.NextReconcileUnix = now.Add(reconcileInterval).Unix()
			return s.Store.UpdateManagedDomain(domain)
		}
	}
	note, err := s.reconcile(ctx, domain, now)
	if err != nil {
		domain.State = "retry_wait"
		domain.RetryCount++
		domain.NextReconcileUnix = now.Add(retryDelay(domain.RetryCount)).Unix()
		domain.LastError = err.Error()
		_ = s.Store.UpdateManagedDomain(domain)
		return err
	}
	domain.State = "ready"
	domain.RetryCount = 0
	domain.LastError = note
	domain.LastReconcileUnix = now.Unix()
	domain.NextReconcileUnix = now.Add(reconcileInterval).Unix()
	return s.Store.UpdateManagedDomain(domain)
}

// Cleanup removes only records that were originally created by Panel. The
// value match is deliberate: if an operator has changed a record since the
// last reconcile, cleanup leaves that record untouched.
func (s *Service) Cleanup(ctx context.Context, domainID string) error {
	domain, err := s.Store.GetManagedDomain(domainID)
	if err != nil {
		return err
	}
	certificates, bindings, err := s.Store.ManagedDomainTLSUsage(domainID)
	if err != nil {
		return err
	}
	if certificates > 0 || bindings > 0 {
		return fmt.Errorf("托管域名仍被协议证书或 TLS 绑定引用")
	}
	provider, credentials, err := s.providerForDomain(domain)
	if err != nil {
		return err
	}
	zone := dnsprovider.Zone{Name: domain.Zone}
	name, err := dnsprovider.RelativeName(domain.FQDN, domain.Zone)
	if err != nil {
		return err
	}
	opCtx, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	if domain.CreatedAByPanel && domain.DesiredIPv4 != "" {
		if err := provider.Delete(opCtx, zone, dnsprovider.RecordRef{
			ID: domain.ProviderRecordAID, Name: name, Type: dnsprovider.TypeA,
			Value: domain.DesiredIPv4,
		}); err != nil {
			return secretstoreError(err, credentials)
		}
	}
	if domain.CreatedAAAAByPanel && domain.DesiredIPv6 != "" {
		if err := provider.Delete(opCtx, zone, dnsprovider.RecordRef{
			ID: domain.ProviderRecordAAAAID, Name: name, Type: dnsprovider.TypeAAAA,
			Value: domain.DesiredIPv6,
		}); err != nil {
			return secretstoreError(err, credentials)
		}
	}
	return s.Store.DeleteManagedDomain(domain.ID)
}

// reconcile converges the DNS records. The returned note is an informational
// message persisted on the domain (e.g. fallback to the node control address);
// it is empty on a straight success.
func (s *Service) reconcile(ctx context.Context, domain *store.ManagedDomain, now time.Time) (string, error) {
	if s == nil || s.Store == nil || s.Secrets == nil || s.Providers == nil {
		return "", fmt.Errorf("DNS 同步服务尚未初始化")
	}
	provider, credentials, err := s.providerForDomain(domain)
	if err != nil {
		return "", err
	}
	previousIPv4, previousIPv6 := domain.DesiredIPv4, domain.DesiredIPv6
	ipv4, ipv6, note, err := s.desiredAddresses(ctx, domain)
	if err != nil {
		return "", err
	}
	domain.DesiredIPv4, domain.DesiredIPv6 = ipv4, ipv6
	zone := dnsprovider.Zone{Name: domain.Zone}
	name, err := dnsprovider.RelativeName(domain.FQDN, domain.Zone)
	if err != nil {
		return "", err
	}
	opCtx, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	if domain.RecordMode != "a" && domain.RecordMode != "dual" {
		if err := cleanupDisabledRecord(
			opCtx, provider, zone, name, dnsprovider.TypeA, previousIPv4,
			domain.ProviderRecordAID, domain.CreatedAByPanel,
		); err != nil {
			return "", secretstoreError(err, credentials)
		}
		domain.DesiredIPv4, domain.ObservedIPv4 = "", []string{}
		domain.ProviderRecordAID, domain.CreatedAByPanel = "", false
	}
	if domain.RecordMode != "aaaa" && domain.RecordMode != "dual" {
		if err := cleanupDisabledRecord(
			opCtx, provider, zone, name, dnsprovider.TypeAAAA, previousIPv6,
			domain.ProviderRecordAAAAID, domain.CreatedAAAAByPanel,
		); err != nil {
			return "", secretstoreError(err, credentials)
		}
		domain.DesiredIPv6, domain.ObservedIPv6 = "", []string{}
		domain.ProviderRecordAAAAID, domain.CreatedAAAAByPanel = "", false
	}
	switch domain.RecordMode {
	case "a":
		domain.ObservedIPv4, domain.ProviderRecordAID, domain.CreatedAByPanel, err =
			reconcileRecord(opCtx, provider, zone, name, dnsprovider.TypeA, ipv4, time.Duration(domain.TTL)*time.Second, domain.CreatedAByPanel)
	case "aaaa":
		domain.ObservedIPv6, domain.ProviderRecordAAAAID, domain.CreatedAAAAByPanel, err =
			reconcileRecord(opCtx, provider, zone, name, dnsprovider.TypeAAAA, ipv6, time.Duration(domain.TTL)*time.Second, domain.CreatedAAAAByPanel)
	case "dual":
		domain.ObservedIPv4, domain.ProviderRecordAID, domain.CreatedAByPanel, err =
			reconcileRecord(opCtx, provider, zone, name, dnsprovider.TypeA, ipv4, time.Duration(domain.TTL)*time.Second, domain.CreatedAByPanel)
		if err == nil {
			domain.ObservedIPv6, domain.ProviderRecordAAAAID, domain.CreatedAAAAByPanel, err =
				reconcileRecord(opCtx, provider, zone, name, dnsprovider.TypeAAAA, ipv6, time.Duration(domain.TTL)*time.Second, domain.CreatedAAAAByPanel)
		}
	default:
		return "", fmt.Errorf("记录模式无效：%s", domain.RecordMode)
	}
	if err != nil {
		return "", secretstoreError(err, credentials)
	}
	domain.LastReconcileUnix = now.Unix()
	return note, nil
}

func cleanupDisabledRecord(
	ctx context.Context,
	provider dnsprovider.Provider,
	zone dnsprovider.Zone,
	name string,
	typ dnsprovider.RecordType,
	value, id string,
	createdByPanel bool,
) error {
	if !createdByPanel || value == "" {
		return nil
	}
	return provider.Delete(ctx, zone, dnsprovider.RecordRef{
		ID: id, Name: name, Type: typ, Value: value,
	})
}

func (s *Service) providerForDomain(domain *store.ManagedDomain) (dnsprovider.Provider, map[string]string, error) {
	if s == nil || s.Store == nil || s.Secrets == nil || s.Providers == nil {
		return nil, nil, fmt.Errorf("DNS 同步服务尚未初始化")
	}
	account, err := s.Store.GetDNSAccount(domain.DNSAccountID)
	if err != nil {
		return nil, nil, err
	}
	if !account.Enabled {
		return nil, nil, fmt.Errorf("DNS 账号已禁用")
	}
	credentials, err := decryptCredentials(s.Secrets, account)
	if err != nil {
		return nil, nil, err
	}
	provider, err := s.Providers.New(account.Provider, dnsprovider.Config{
		Credentials: credentials, Settings: account.Settings,
		Zone:        account.Zone,
		HTTPTimeout: s.timeout(),
	})
	if err != nil {
		return nil, nil, secretstoreError(err, credentials)
	}
	return provider, credentials, nil
}

func reconcileRecord(
	ctx context.Context,
	provider dnsprovider.Provider,
	zone dnsprovider.Zone,
	name string,
	typ dnsprovider.RecordType,
	desired string,
	ttl time.Duration,
	alreadyCreated bool,
) ([]string, string, bool, error) {
	if desired == "" {
		return nil, "", alreadyCreated, fmt.Errorf("%s 记录没有可用目标地址", typ)
	}
	records, err := provider.Lookup(ctx, zone, name, typ)
	if err != nil {
		return nil, "", alreadyCreated, err
	}
	observed := recordValues(records)
	if len(observed) == 1 && observed[0] == desired {
		id := ""
		if len(records) == 1 {
			id = records[0].ID
		}
		return observed, id, alreadyCreated, nil
	}
	created := alreadyCreated || len(records) == 0
	ref, err := provider.Upsert(ctx, zone, dnsprovider.Record{
		Name: name, Type: typ, Value: desired, TTL: ttl,
	})
	if err != nil {
		return observed, "", created, err
	}
	records, err = provider.Lookup(ctx, zone, name, typ)
	if err != nil {
		return observed, ref.ID, created, err
	}
	observed = recordValues(records)
	if len(observed) != 1 || observed[0] != desired {
		return observed, ref.ID, created, fmt.Errorf("%s 记录写入后校验不一致", typ)
	}
	return observed, ref.ID, created, nil
}

// desiredAddresses resolves the target A/AAAA values. The note return value
// describes a degraded but usable resolution (agent probe fell back to the
// node control address); it is empty for the normal path.
func (s *Service) desiredAddresses(ctx context.Context, domain *store.ManagedDomain) (string, string, string, error) {
	switch domain.AddressSource {
	case "manual":
		ipv4, ipv6, err := validateDesired(domain.ManualIPv4, domain.ManualIPv6, domain.RecordMode)
		return ipv4, ipv6, "", err
	case "node_address":
		node, err := s.Store.GetNode(domain.NodeID)
		if err != nil {
			return "", "", "", err
		}
		ipv4, ipv6, err := nodeAddressDesired(node, domain.RecordMode)
		return ipv4, ipv6, "", err
	case "agent_public":
		if s.DialAgent == nil {
			return "", "", "", fmt.Errorf("Agent 公网地址连接器不可用")
		}
		node, err := s.Store.GetNode(domain.NodeID)
		if err != nil {
			return "", "", "", err
		}
		ipv4, ipv6, probeErr := s.probeAgentPublic(ctx, node, domain.RecordMode)
		if probeErr == nil {
			return ipv4, ipv6, "", nil
		}
		// Probe failed (RPC error or no public address reported): fall back
		// to the node control address when it is itself a usable public IP.
		fallbackIPv4, fallbackIPv6, fallbackErr := nodeAddressDesired(node, domain.RecordMode)
		if fallbackErr == nil {
			return fallbackIPv4, fallbackIPv6,
				fmt.Sprintf("Agent 公网探测失败（%v），已回退使用节点控制地址", probeErr), nil
		}
		return "", "", "", probeErr
	default:
		return "", "", "", fmt.Errorf("地址来源无效：%s", domain.AddressSource)
	}
}

// nodeAddressDesired derives desired records from the node control address.
// It fails when the control address is not a usable public IP.
func nodeAddressDesired(node *store.Node, mode string) (string, string, error) {
	address, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(node.Address), "[]"))
	if err != nil || !publicAddress(address) {
		return "", "", fmt.Errorf("节点控制地址不是可用公网 IP")
	}
	if address.Is4() {
		return validateDesired(address.String(), "", mode)
	}
	return validateDesired("", address.String(), mode)
}

func (s *Service) probeAgentPublic(ctx context.Context, node *store.Node, mode string) (string, string, error) {
	token := node.Token
	if token == "" && s.DefaultToken != nil {
		token = s.DefaultToken()
	}
	if node.ControlMode == store.ControlModeUplink {
		return "", "", fmt.Errorf("uplink 节点不支持即时公网探测")
	}
	opCtx, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	client, err := s.DialAgent(opCtx, *node, token)
	if err != nil {
		return "", "", fmt.Errorf("连接 Agent 获取公网地址失败：%w", err)
	}
	defer func() { _ = client.Close() }()
	want4 := mode == "a" || mode == "dual"
	want6 := mode == "aaaa" || mode == "dual"
	response, err := client.GetPublicAddresses(opCtx, want4, want6)
	if err != nil {
		return "", "", fmt.Errorf("Agent 公网地址探测失败：%w", err)
	}
	return validateDesired(response.GetIpv4(), response.GetIpv6(), mode)
}

func validateDesired(ipv4, ipv6, mode string) (string, string, error) {
	ipv4 = strings.TrimSpace(ipv4)
	ipv6 = strings.TrimSpace(ipv6)
	if mode == "a" || mode == "dual" {
		address, err := netip.ParseAddr(ipv4)
		if err != nil || !address.Is4() || !publicAddress(address) {
			return "", "", fmt.Errorf("IPv4 目标地址无效或不是公网地址")
		}
		ipv4 = address.String()
	}
	if mode == "aaaa" || mode == "dual" {
		address, err := netip.ParseAddr(ipv6)
		if err != nil || !address.Is6() || !publicAddress(address) {
			return "", "", fmt.Errorf("IPv6 目标地址无效或不是公网地址")
		}
		ipv6 = address.String()
	}
	return ipv4, ipv6, nil
}

func recordValues(records []dnsprovider.Record) []string {
	out := make([]string, 0, len(records))
	for _, record := range records {
		out = append(out, record.Value)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func decryptCredentials(secrets *secretstore.Store, account *store.DNSAccount) (map[string]string, error) {
	raw, err := secrets.Decrypt(
		account.CredentialsCiphertext,
		"dns-account:"+account.ID+":"+account.Provider,
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

func secretstoreError(err error, credentials map[string]string) error {
	values := make([]string, 0, len(credentials))
	for _, value := range credentials {
		values = append(values, value)
	}
	return fmt.Errorf("%s", secretstore.Redact(err.Error(), values...))
}

func (s *Service) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return 20 * time.Second
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func retryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return time.Minute
	case 2:
		return 5 * time.Minute
	case 3:
		return 15 * time.Minute
	case 4:
		return time.Hour
	default:
		return 6 * time.Hour
	}
}

func publicAddress(address netip.Addr) bool {
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() ||
		address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsUnspecified() {
		return false
	}
	return !netip.MustParsePrefix("100.64.0.0/10").Contains(address)
}
