package api_test

import (
	"net/http"
	"testing"

	"github.com/ladderairport/panel/internal/store"
)

func TestCreateProtocolCertificateEnqueuesIssue(t *testing.T) {
	ts, client, st := newTestServer(t, nil, nil)
	resp := login(t, client, ts.URL, "admin")
	resp.Body.Close()
	node, domain, account := createCertificateAPIFixture(t, st)

	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/protocol-certificates", map[string]any{
		"node_id": node.ID, "managed_domain_id": domain.ID, "acme_account_id": account.ID,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d body=%v", resp.StatusCode, body)
	}
	certificate, ok := body["certificate"].(map[string]any)
	if !ok || certificate["status"] != "pending" {
		t.Fatalf("certificate=%v", body["certificate"])
	}
	jobs, err := st.ListAutomationJobs(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Type != "certificate.issue" ||
		jobs[0].TargetID != certificate["id"] {
		t.Fatalf("jobs=%+v", jobs)
	}
}

func TestManagedTLSBindingRejectsCertificateForAnotherNode(t *testing.T) {
	ts, client, st := newTestServer(t, nil, nil)
	resp := login(t, client, ts.URL, "admin")
	resp.Body.Close()
	node, domain, account := createCertificateAPIFixture(t, st)
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
	if err := st.SetNodeInbounds(node.ID, []string{inbound.ID}); err != nil {
		t.Fatal(err)
	}
	other := &store.Node{Name: "other", Address: "192.0.2.11", GRPCPort: 50051, Status: "online"}
	if err := st.CreateNode(other); err != nil {
		t.Fatal(err)
	}
	certificate := &store.ProtocolCertificate{
		NodeID: other.ID, ManagedDomainID: domain.ID, ACMEAccountID: account.ID,
		Domains: []string{domain.FQDN}, Status: "active",
		ActiveCertPath: "/cert", ActiveKeyPath: "/key",
	}
	if err := st.CreateProtocolCertificate(certificate); err == nil {
		t.Fatal("store accepted certificate whose node differs from managed domain")
	}
}

func TestManagedTLSBindingAcceptsActiveCertificate(t *testing.T) {
	ts, client, st := newTestServer(t, nil, nil)
	resp := login(t, client, ts.URL, "admin")
	resp.Body.Close()
	node, domain, account := createCertificateAPIFixture(t, st)
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
	if err := st.SetNodeInbounds(node.ID, []string{inbound.ID}); err != nil {
		t.Fatal(err)
	}
	certificate := &store.ProtocolCertificate{
		NodeID: node.ID, ManagedDomainID: domain.ID, ACMEAccountID: account.ID,
		Domains: []string{domain.FQDN}, Status: "active",
		ActiveCertPath: "/managed/r1/fullchain.pem", ActiveKeyPath: "/managed/r1/privkey.pem",
	}
	if err := st.CreateProtocolCertificate(certificate); err != nil {
		t.Fatal(err)
	}
	resp, body := doJSON(
		t, client, http.MethodPut,
		ts.URL+"/api/v1/nodes/"+node.ID+"/inbounds/"+inbound.ID+"/tls",
		map[string]any{
			"mode": "managed", "managed_domain_id": domain.ID,
			"certificate_id": certificate.ID,
		},
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%v", resp.StatusCode, body)
	}
	binding, err := st.GetNodeInboundTLSBinding(node.ID, inbound.ID)
	if err != nil || binding.Mode != "managed" || binding.CertificateID != certificate.ID {
		t.Fatalf("binding=%+v err=%v", binding, err)
	}
}

func createCertificateAPIFixture(t *testing.T, st *store.Store) (*store.Node, *store.ManagedDomain, *store.ACMEAccount) {
	t.Helper()
	node := &store.Node{Name: "edge", Address: "192.0.2.10", GRPCPort: 50051, Status: "online"}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	dnsAccount := &store.DNSAccount{
		Name: "DNS", Provider: "callback", CredentialsCiphertext: "encrypted",
		Settings: map[string]any{}, Enabled: true,
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
	account := &store.ACMEAccount{
		Name: "CA", DirectoryURL: "https://ca.example/directory",
		AccountKeyCiphertext: "encrypted", RegistrationURI: "https://ca.example/account/1",
		Status: "active",
	}
	if err := st.CreateACMEAccount(account); err != nil {
		t.Fatal(err)
	}
	return node, domain, account
}
