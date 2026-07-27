// Package certmanager coordinates Agent-local keys, ACME DNS-01 issuance and
// staged protocol certificate installation.
package certmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	acmeflow "github.com/ladderairport/panel/internal/acme"
	"github.com/ladderairport/panel/internal/dnsprovider"
	"github.com/ladderairport/panel/internal/nodeconfig"
	"github.com/ladderairport/panel/internal/secretstore"
	"github.com/ladderairport/panel/internal/store"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
)

type Agent interface {
	Close() error
	Ping(ctx context.Context) (*agentv1.PingResponse, error)
	ApplyConfig(ctx context.Context, configJSON, hash string, replace bool) (*agentv1.ApplyConfigResponse, error)
	PrepareProtocolCertificate(
		ctx context.Context, certificateID, generationID string, dnsNames []string,
	) (*agentv1.PrepareProtocolCertificateResponse, error)
	InstallProtocolCertificate(
		ctx context.Context, certificateID, generationID, keyID, certificatePEM string,
		dnsNames []string,
	) (*agentv1.InstallProtocolCertificateResponse, error)
}

type DialAgent func(ctx context.Context, node store.Node, token string) (Agent, error)

type IssueFunc func(
	ctx context.Context,
	account *store.ACMEAccount,
	presenter acmeflow.DNS01Presenter,
	csrPEM string,
	domains []string,
) (*acmeflow.IssuanceResult, error)

type Service struct {
	Store         *store.Store
	Secrets       *secretstore.Store
	Providers     *dnsprovider.Registry
	ACME          *acmeflow.Service
	DialAgent     DialAgent
	DefaultToken  func() string
	IssueFunc     IssueFunc
	ConfigBuilder *nodeconfig.Builder
	Coordinator   *sync.Mutex
	Timeout       time.Duration
	Now           func() time.Time
}

func (s *Service) Issue(ctx context.Context, certificateID string) (resultErr error) {
	if s == nil || s.Store == nil || s.Secrets == nil || s.Providers == nil {
		return fmt.Errorf("协议证书自动化尚未初始化")
	}
	certificate, err := s.Store.GetProtocolCertificate(certificateID)
	if err != nil {
		return err
	}
	defer func() {
		if resultErr == nil {
			return
		}
		now := s.now()
		certificate.Status = "retry_wait"
		certificate.RetryCount++
		certificate.NextRetryUnix = now.Add(retryDelay(certificate.RetryCount)).Unix()
		certificate.LastError = resultErr.Error()
		_ = s.Store.UpdateProtocolCertificate(certificate)
	}()
	domain, err := s.Store.GetManagedDomain(certificate.ManagedDomainID)
	if err != nil {
		return err
	}
	if !domain.Enabled || domain.State != "ready" {
		return fmt.Errorf("托管域名尚未完成 DNS 同步")
	}
	if domain.NodeID != certificate.NodeID {
		return fmt.Errorf("证书节点与托管域名节点不一致")
	}
	account, err := s.Store.GetACMEAccount(certificate.ACMEAccountID)
	if err != nil {
		return err
	}
	if account.Status != "active" || account.RegistrationURI == "" {
		return fmt.Errorf("ACME 账号尚未注册或不可用")
	}
	node, err := s.Store.GetNode(certificate.NodeID)
	if err != nil {
		return err
	}
	token := node.Token
	if token == "" && s.DefaultToken != nil {
		token = s.DefaultToken()
	}
	if s.DialAgent == nil {
		return fmt.Errorf("Agent 证书连接器不可用")
	}
	opCtx, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	agent, err := s.DialAgent(opCtx, *node, token)
	if err != nil {
		return fmt.Errorf("连接 Agent 准备协议证书失败：%w", err)
	}
	defer func() { _ = agent.Close() }()
	ping, err := agent.Ping(opCtx)
	if err != nil {
		return fmt.Errorf("检查 Agent 协议证书能力失败：%w", err)
	}
	if !hasCapability(ping.GetCapabilities(), "protocol-cert-v1") {
		return fmt.Errorf("Agent 版本过旧，不支持协议证书；请先升级 Agent")
	}
	if hasStagedCandidate(certificate) {
		if err := s.deployCandidate(opCtx, agent, certificate); err != nil {
			return err
		}
		certificate.RetryCount = 0
		certificate.NextRetryUnix = 0
		certificate.LastError = ""
		return s.Store.UpdateProtocolCertificate(certificate)
	}

	generationID := fmt.Sprintf("r%d", certificate.Revision+1)
	prepared, err := agent.PrepareProtocolCertificate(
		opCtx, certificate.ID, generationID, certificate.Domains,
	)
	if err != nil {
		return fmt.Errorf("Agent 生成协议证书 CSR 失败：%w", err)
	}
	certificate.Status = "authorizing"
	certificate.AgentKeyID = prepared.GetKeyId()
	certificate.PublicKeyFingerprint = prepared.GetPublicKeyFingerprint()
	certificate.LastError = ""
	if err := s.Store.UpdateProtocolCertificate(certificate); err != nil {
		return err
	}
	presenter, err := s.newPresenter(domain)
	if err != nil {
		return err
	}
	issued, err := s.issue(opCtx, account, presenter, prepared.GetCsrPem(), certificate.Domains)
	if err != nil {
		return err
	}
	certificate.Status = "installing"
	certificate.CertPEM = issued.FullChainPEM
	if err := s.Store.UpdateProtocolCertificate(certificate); err != nil {
		return err
	}
	installed, err := agent.InstallProtocolCertificate(
		opCtx, certificate.ID, generationID, prepared.GetKeyId(),
		issued.FullChainPEM, certificate.Domains,
	)
	if err != nil {
		return fmt.Errorf("Agent 安装协议证书失败：%w", err)
	}
	certificate.CandidateCertPath = installed.GetCertificatePath()
	certificate.CandidateKeyPath = installed.GetKeyPath()
	certificate.Fingerprint = installed.GetFingerprint()
	certificate.Serial = installed.GetSerial()
	certificate.NotBeforeUnix = installed.GetNotBeforeUnix()
	certificate.NotAfterUnix = installed.GetNotAfterUnix()
	certificate.RenewAfterUnix = renewalTime(
		installed.GetNotBeforeUnix(), installed.GetNotAfterUnix(),
	)
	if err := s.deployCandidate(opCtx, agent, certificate); err != nil {
		return err
	}
	certificate.RetryCount = 0
	certificate.NextRetryUnix = 0
	certificate.LastError = ""
	return s.Store.UpdateProtocolCertificate(certificate)
}

func (s *Service) deployCandidate(
	ctx context.Context,
	agent Agent,
	certificate *store.ProtocolCertificate,
) error {
	bindings, err := s.Store.ListNodeInboundTLSBindings(certificate.NodeID)
	if err != nil {
		return err
	}
	bound := false
	for _, binding := range bindings {
		if binding.Mode == "managed" && binding.CertificateID == certificate.ID {
			bound = true
			break
		}
	}
	if !bound {
		certificate.ActiveCertPath = certificate.CandidateCertPath
		certificate.ActiveKeyPath = certificate.CandidateKeyPath
		certificate.Revision++
		certificate.Status = "active"
		return nil
	}
	if s.ConfigBuilder == nil {
		return fmt.Errorf("节点配置构建器不可用，无法激活新证书")
	}
	coordinator := s.Coordinator
	if coordinator == nil {
		coordinator = &sync.Mutex{}
	}
	coordinator.Lock()
	defer coordinator.Unlock()

	// Active* remains the last confirmed generation while the builder overlays
	// Candidate* for the deploying state. A crash can therefore safely replay
	// the same candidate without losing the rollback baseline.
	certificate.Status = "deploying"
	if err := s.Store.UpdateProtocolCertificate(certificate); err != nil {
		return err
	}
	config, err := s.ConfigBuilder.Build(certificate.NodeID)
	if err != nil {
		return fmt.Errorf("构建新证书候选配置失败：%w", err)
	}
	response, err := agent.ApplyConfig(ctx, config.JSON, config.Hash, true)
	if err != nil {
		return fmt.Errorf("下发新证书候选配置失败：%w", err)
	}
	if !response.GetOk() {
		return fmt.Errorf("Agent 拒绝新证书候选配置：%s", response.GetMessage())
	}
	certificate.ActiveCertPath = certificate.CandidateCertPath
	certificate.ActiveKeyPath = certificate.CandidateKeyPath
	certificate.Revision++
	certificate.Status = "active"
	return nil
}

func hasStagedCandidate(certificate *store.ProtocolCertificate) bool {
	if certificate == nil ||
		certificate.CandidateCertPath == "" || certificate.CandidateKeyPath == "" {
		return false
	}
	return certificate.CandidateCertPath != certificate.ActiveCertPath ||
		certificate.CandidateKeyPath != certificate.ActiveKeyPath
}

func hasCapability(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func (s *Service) issue(
	ctx context.Context,
	account *store.ACMEAccount,
	presenter acmeflow.DNS01Presenter,
	csrPEM string,
	domains []string,
) (*acmeflow.IssuanceResult, error) {
	if s.IssueFunc != nil {
		return s.IssueFunc(ctx, account, presenter, csrPEM, domains)
	}
	if s.ACME == nil {
		return nil, fmt.Errorf("ACME 客户端不可用")
	}
	client, err := s.ACME.NewClient(account)
	if err != nil {
		return nil, err
	}
	return acmeflow.Issue(ctx, client, presenter, csrPEM, domains)
}

func (s *Service) newPresenter(domain *store.ManagedDomain) (*dnsPresenter, error) {
	account, err := s.Store.GetDNSAccount(domain.DNSAccountID)
	if err != nil {
		return nil, err
	}
	raw, err := s.Secrets.Decrypt(
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
	provider, err := s.Providers.New(account.Provider, dnsprovider.Config{
		Credentials: credentials, Settings: account.Settings,
		Zone:        account.Zone,
		HTTPTimeout: s.timeout(),
	})
	if err != nil {
		return nil, err
	}
	return &dnsPresenter{
		provider: provider, zone: dnsprovider.Zone{Name: domain.Zone},
		timeout: s.timeout(), secrets: credentialValues(credentials),
	}, nil
}

func credentialValues(credentials map[string]string) []string {
	values := make([]string, 0, len(credentials))
	for _, value := range credentials {
		values = append(values, value)
	}
	return values
}

func (s *Service) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return 2 * time.Minute
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func renewalTime(notBefore, notAfter int64) int64 {
	lifetime := notAfter - notBefore
	if lifetime <= 0 {
		return notAfter
	}
	return notBefore + lifetime*2/3
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
