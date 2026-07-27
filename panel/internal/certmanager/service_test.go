package certmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	acmeflow "github.com/ladderairport/panel/internal/acme"
	"github.com/ladderairport/panel/internal/dnsprovider"
	"github.com/ladderairport/panel/internal/nodeconfig"
	"github.com/ladderairport/panel/internal/secretstore"
	"github.com/ladderairport/panel/internal/store"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
)

type fakeAgent struct {
	prepared  int
	installed int
	applied   int
	applyOK   bool
}

func (f *fakeAgent) Close() error { return nil }
func (f *fakeAgent) Ping(context.Context) (*agentv1.PingResponse, error) {
	return &agentv1.PingResponse{Capabilities: []string{"protocol-cert-v1"}}, nil
}
func (f *fakeAgent) ApplyConfig(context.Context, string, string, bool) (*agentv1.ApplyConfigResponse, error) {
	f.applied++
	return &agentv1.ApplyConfigResponse{Ok: f.applyOK, Message: "apply-result"}, nil
}
func (f *fakeAgent) PrepareProtocolCertificate(
	context.Context, string, string, []string,
) (*agentv1.PrepareProtocolCertificateResponse, error) {
	f.prepared++
	return &agentv1.PrepareProtocolCertificateResponse{
		KeyId: "cert/r1", CsrPem: "csr", PublicKeyFingerprint: "public-key",
	}, nil
}
func (f *fakeAgent) InstallProtocolCertificate(
	context.Context, string, string, string, string, []string,
) (*agentv1.InstallProtocolCertificateResponse, error) {
	f.installed++
	return &agentv1.InstallProtocolCertificateResponse{
		CertificatePath: "/certs/r1/fullchain.pem", KeyPath: "/certs/r1/privkey.pem",
		Fingerprint: "cert-fingerprint", Serial: "01",
		NotBeforeUnix: 2_000_000_000, NotAfterUnix: 2_007_776_000,
	}, nil
}

type noopProvider struct{}

func (*noopProvider) Test(context.Context) error { return nil }
func (*noopProvider) ResolveZone(context.Context, string) (dnsprovider.Zone, error) {
	return dnsprovider.Zone{Name: "example.com"}, nil
}
func (*noopProvider) Lookup(context.Context, dnsprovider.Zone, string, dnsprovider.RecordType) ([]dnsprovider.Record, error) {
	return nil, nil
}
func (*noopProvider) Upsert(context.Context, dnsprovider.Zone, dnsprovider.Record) (dnsprovider.RecordRef, error) {
	return dnsprovider.RecordRef{}, nil
}
func (*noopProvider) Delete(context.Context, dnsprovider.Zone, dnsprovider.RecordRef) error {
	return nil
}

func TestIssueStagesAgentCertificateAndMarksActive(t *testing.T) {
	service, st, certificate, agent := newCertificateFixture(t)
	service.IssueFunc = func(
		context.Context, *store.ACMEAccount, acmeflow.DNS01Presenter, string, []string,
	) (*acmeflow.IssuanceResult, error) {
		return &acmeflow.IssuanceResult{FullChainPEM: "certificate-chain"}, nil
	}
	if err := service.Issue(context.Background(), certificate.ID); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetProtocolCertificate(certificate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "active" || got.Revision != 1 ||
		got.ActiveCertPath != "/certs/r1/fullchain.pem" ||
		got.RenewAfterUnix != 2_005_184_000 ||
		agent.prepared != 1 || agent.installed != 1 {
		t.Fatalf("certificate=%+v agent=%+v", got, agent)
	}
}

func TestIssueFailureKeepsPreviousActivePathsAndSchedulesRetry(t *testing.T) {
	service, st, certificate, _ := newCertificateFixture(t)
	certificate.ActiveCertPath = "/certs/r0/fullchain.pem"
	certificate.ActiveKeyPath = "/certs/r0/privkey.pem"
	certificate.Revision = 1
	if err := st.UpdateProtocolCertificate(certificate); err != nil {
		t.Fatal(err)
	}
	service.IssueFunc = func(
		context.Context, *store.ACMEAccount, acmeflow.DNS01Presenter, string, []string,
	) (*acmeflow.IssuanceResult, error) {
		return nil, errors.New("ACME 暂时不可用")
	}
	now := time.Unix(2_000_000_000, 0)
	service.Now = func() time.Time { return now }
	if err := service.Issue(context.Background(), certificate.ID); err == nil {
		t.Fatal("issuance failure ignored")
	}
	got, _ := st.GetProtocolCertificate(certificate.ID)
	if got.Status != "retry_wait" || got.NextRetryUnix != now.Add(time.Minute).Unix() ||
		got.ActiveCertPath != "/certs/r0/fullchain.pem" || got.Revision != 1 {
		t.Fatalf("certificate=%+v", got)
	}
}

func TestRenewalAppliesCandidateConfigBeforeActivation(t *testing.T) {
	service, st, certificate, agent := newCertificateFixture(t)
	bindCertificate(t, st, certificate)
	service.IssueFunc = func(
		context.Context, *store.ACMEAccount, acmeflow.DNS01Presenter, string, []string,
	) (*acmeflow.IssuanceResult, error) {
		return &acmeflow.IssuanceResult{FullChainPEM: "renewed-chain"}, nil
	}
	if err := service.Issue(context.Background(), certificate.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetProtocolCertificate(certificate.ID)
	if agent.applied != 1 || got.Status != "active" || got.Revision != 2 ||
		got.ActiveCertPath != "/certs/r1/fullchain.pem" {
		t.Fatalf("certificate=%+v agent=%+v", got, agent)
	}
}

func TestRenewalApplyFailureRollsBackActivePaths(t *testing.T) {
	service, st, certificate, agent := newCertificateFixture(t)
	bindCertificate(t, st, certificate)
	agent.applyOK = false
	service.IssueFunc = func(
		context.Context, *store.ACMEAccount, acmeflow.DNS01Presenter, string, []string,
	) (*acmeflow.IssuanceResult, error) {
		return &acmeflow.IssuanceResult{FullChainPEM: "renewed-chain"}, nil
	}
	now := time.Unix(2_000_000_000, 0)
	service.Now = func() time.Time { return now }
	if err := service.Issue(context.Background(), certificate.ID); err == nil {
		t.Fatal("candidate apply failure ignored")
	}
	got, _ := st.GetProtocolCertificate(certificate.ID)
	if agent.applied != 1 || got.Status != "retry_wait" || got.Revision != 1 ||
		got.ActiveCertPath != "/certs/r0/fullchain.pem" ||
		got.ActiveKeyPath != "/certs/r0/privkey.pem" ||
		got.CandidateCertPath != "/certs/r1/fullchain.pem" {
		t.Fatalf("certificate=%+v agent=%+v", got, agent)
	}
}

func TestRenewalResumesStagedCandidateWithoutAnotherIssuance(t *testing.T) {
	service, st, certificate, agent := newCertificateFixture(t)
	bindCertificate(t, st, certificate)
	agent.applyOK = false
	issueCalls := 0
	service.IssueFunc = func(
		context.Context, *store.ACMEAccount, acmeflow.DNS01Presenter, string, []string,
	) (*acmeflow.IssuanceResult, error) {
		issueCalls++
		return &acmeflow.IssuanceResult{FullChainPEM: "renewed-chain"}, nil
	}
	if err := service.Issue(context.Background(), certificate.ID); err == nil {
		t.Fatal("candidate apply failure ignored")
	}

	agent.applyOK = true
	if err := service.Issue(context.Background(), certificate.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetProtocolCertificate(certificate.ID)
	if issueCalls != 1 || agent.prepared != 1 || agent.installed != 1 ||
		agent.applied != 2 || got.Status != "active" || got.Revision != 2 ||
		got.ActiveCertPath != "/certs/r1/fullchain.pem" {
		t.Fatalf("issueCalls=%d certificate=%+v agent=%+v", issueCalls, got, agent)
	}
}

func bindCertificate(t *testing.T, st *store.Store, certificate *store.ProtocolCertificate) {
	t.Helper()
	inbound := &store.InboundConfig{
		Name: "trojan", Protocol: "trojan", Enabled: true,
		Params: map[string]any{
			"listen": "0.0.0.0", "port": 443, "password": "secret",
			"tls_cert_path": "/legacy/cert", "tls_key_path": "/legacy/key",
		},
	}
	if err := st.CreateInbound(inbound); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeInbounds(certificate.NodeID, []string{inbound.ID}); err != nil {
		t.Fatal(err)
	}
	certificate.Status = "active"
	certificate.ActiveCertPath = "/certs/r0/fullchain.pem"
	certificate.ActiveKeyPath = "/certs/r0/privkey.pem"
	certificate.Revision = 1
	if err := st.UpdateProtocolCertificate(certificate); err != nil {
		t.Fatal(err)
	}
	if err := st.PutNodeInboundTLSBinding(&store.NodeInboundTLSBinding{
		NodeID: certificate.NodeID, InboundID: inbound.ID, Mode: "managed",
		ManagedDomainID: certificate.ManagedDomainID, CertificateID: certificate.ID,
	}); err != nil {
		t.Fatal(err)
	}
}

func newCertificateFixture(t *testing.T) (*Service, *store.Store, *store.ProtocolCertificate, *fakeAgent) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	node := &store.Node{Name: "edge", Address: "192.0.2.10", GRPCPort: 50051, Status: "online"}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	secrets, _ := secretstore.New(bytes.Repeat([]byte{4}, 32))
	raw, _ := json.Marshal(map[string]string{"token": "secret"})
	ciphertext, _ := secrets.Encrypt(raw, "dns-account:dns-account:memory")
	dnsAccount := &store.DNSAccount{
		ID: "dns-account", Name: "DNS", Provider: "memory",
		CredentialsCiphertext: ciphertext, Settings: map[string]any{}, Enabled: true,
	}
	if err := st.CreateDNSAccount(dnsAccount); err != nil {
		t.Fatal(err)
	}
	domain := &store.ManagedDomain{
		NodeID: node.ID, DNSAccountID: dnsAccount.ID, Zone: "example.com",
		FQDN: "edge.example.com", RecordMode: "a", AddressSource: "manual",
		ManualIPv4: "192.0.2.10", TTL: 300, Enabled: true, State: "ready",
	}
	if err := st.CreateManagedDomain(domain); err != nil {
		t.Fatal(err)
	}
	acmeAccount := &store.ACMEAccount{
		Name: "CA", DirectoryURL: "https://ca.example/directory",
		AccountKeyCiphertext: "encrypted", RegistrationURI: "https://ca.example/account/1",
		Status: "active",
	}
	if err := st.CreateACMEAccount(acmeAccount); err != nil {
		t.Fatal(err)
	}
	certificate := &store.ProtocolCertificate{
		NodeID: node.ID, ManagedDomainID: domain.ID, ACMEAccountID: acmeAccount.ID,
		Domains: []string{domain.FQDN}, Status: "pending",
	}
	if err := st.CreateProtocolCertificate(certificate); err != nil {
		t.Fatal(err)
	}
	registry := dnsprovider.NewRegistry()
	_ = registry.Register(dnsprovider.Metadata{Name: "memory"}, func(dnsprovider.Config) (dnsprovider.Provider, error) {
		return &noopProvider{}, nil
	})
	agent := &fakeAgent{applyOK: true}
	service := &Service{
		Store: st, Secrets: secrets, Providers: registry,
		ConfigBuilder: &nodeconfig.Builder{Store: st},
		DialAgent: func(context.Context, store.Node, string) (Agent, error) {
			return agent, nil
		},
	}
	return service, st, certificate, agent
}
