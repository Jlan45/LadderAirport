package store

import (
	"strings"
	"testing"
	"time"
)

func createDNSFixture(t *testing.T, s *Store) (*Node, *InboundConfig, *DNSAccount, *ManagedDomain, *ACMEAccount) {
	t.Helper()
	node := &Node{
		Name:     "dns-node",
		Address:  "192.0.2.10",
		GRPCPort: 50051,
		Status:   "unknown",
	}
	if err := s.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	inbound := &InboundConfig{
		Name:     "tls-inbound",
		Protocol: "trojan",
		Params:   map[string]any{"port": 443},
		Enabled:  true,
	}
	if err := s.CreateInbound(inbound); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeInbounds(node.ID, []string{inbound.ID}); err != nil {
		t.Fatal(err)
	}
	account := &DNSAccount{
		Name:                  "cloudflare-primary",
		Provider:              "cloudflare",
		CredentialsCiphertext: "v1.encrypted.value",
		Settings:              map[string]any{"zone_id": "zone"},
		Enabled:               true,
	}
	if err := s.CreateDNSAccount(account); err != nil {
		t.Fatal(err)
	}
	domain := &ManagedDomain{
		NodeID:        node.ID,
		DNSAccountID:  account.ID,
		Zone:          "example.com",
		FQDN:          "edge.example.com",
		RecordMode:    "dual",
		AddressSource: "agent_public",
		TTL:           300,
		Enabled:       true,
		ObservedIPv4:  []string{},
		ObservedIPv6:  []string{},
	}
	if err := s.CreateManagedDomain(domain); err != nil {
		t.Fatal(err)
	}
	acme := &ACMEAccount{
		Name:                 "letsencrypt-staging",
		DirectoryURL:         "https://acme-staging-v02.api.letsencrypt.org/directory",
		Email:                "ops@example.com",
		AccountKeyCiphertext: "v1.account.key",
		Status:               "active",
	}
	if err := s.CreateACMEAccount(acme); err != nil {
		t.Fatal(err)
	}
	return node, inbound, account, domain, acme
}

func TestDNSAndCertificateStoreRoundTrip(t *testing.T) {
	s := openTestStore(t)
	node, inbound, account, domain, acme := createDNSFixture(t, s)

	gotAccount, err := s.GetDNSAccount(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !gotAccount.HasCredentials || gotAccount.Settings["zone_id"] != "zone" {
		t.Fatalf("unexpected DNS account: %+v", gotAccount)
	}

	domain.DesiredIPv4 = "192.0.2.10"
	domain.ObservedIPv4 = []string{"192.0.2.10"}
	domain.State = "ready"
	if err := s.UpdateManagedDomain(domain); err != nil {
		t.Fatal(err)
	}
	gotDomain, err := s.GetManagedDomain(domain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotDomain.State != "ready" || len(gotDomain.ObservedIPv4) != 1 {
		t.Fatalf("unexpected managed domain: %+v", gotDomain)
	}

	certificate := &ProtocolCertificate{
		NodeID:          node.ID,
		ManagedDomainID: domain.ID,
		ACMEAccountID:   acme.ID,
		Domains:         []string{domain.FQDN},
		Status:          "active",
		AgentKeyID:      "key-generation-1",
		ActiveCertPath:  "/var/lib/ladder-airport/protocol-certs/cert/gen/fullchain.pem",
		ActiveKeyPath:   "/var/lib/ladder-airport/protocol-certs/cert/gen/privkey.pem",
		Revision:        1,
	}
	if err := s.CreateProtocolCertificate(certificate); err != nil {
		t.Fatal(err)
	}
	if err := s.PutNodeInboundTLSBinding(&NodeInboundTLSBinding{
		NodeID:          node.ID,
		InboundID:       inbound.ID,
		Mode:            "managed",
		ManagedDomainID: domain.ID,
		CertificateID:   certificate.ID,
	}); err != nil {
		t.Fatal(err)
	}
	binding, err := s.GetNodeInboundTLSBinding(node.ID, inbound.ID)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Mode != "managed" || binding.CertificateID != certificate.ID {
		t.Fatalf("unexpected binding: %+v", binding)
	}
}

func TestDNSAccountDeleteIsRestrictedWhileReferenced(t *testing.T) {
	s := openTestStore(t)
	_, _, account, _, _ := createDNSFixture(t, s)
	err := s.DeleteDNSAccount(account.ID)
	if err == nil || !strings.Contains(err.Error(), "解除关联域名") {
		t.Fatalf("DeleteDNSAccount error = %v", err)
	}
}

func TestManagedDomainDeleteIsRestrictedWhileCertificateExists(t *testing.T) {
	s := openTestStore(t)
	node, _, _, domain, acme := createDNSFixture(t, s)
	certificate := &ProtocolCertificate{
		NodeID: node.ID, ManagedDomainID: domain.ID, ACMEAccountID: acme.ID,
		Domains: []string{domain.FQDN}, Status: "pending",
	}
	if err := s.CreateProtocolCertificate(certificate); err != nil {
		t.Fatal(err)
	}
	err := s.DeleteManagedDomain(domain.ID)
	if err == nil || !strings.Contains(err.Error(), "解除") {
		t.Fatalf("DeleteManagedDomain error = %v", err)
	}
}

func TestAutomationJobLeaseAndIdempotentEnqueue(t *testing.T) {
	s := openTestStore(t)
	first, err := s.EnqueueAutomationJob(&AutomationJob{
		Type:       "dns.reconcile",
		TargetType: "managed_domain",
		TargetID:   "domain-1",
		Payload:    map[string]any{"force": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.EnqueueAutomationJob(&AutomationJob{
		Type:       "dns.reconcile",
		TargetType: "managed_domain",
		TargetID:   "domain-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("duplicate active job created: %s != %s", second.ID, first.ID)
	}
	now := time.Now()
	jobs, err := s.ClaimAutomationJobs("worker-a", now, time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].State != "running" || jobs[0].Attempt != 1 {
		t.Fatalf("unexpected claimed jobs: %+v", jobs)
	}
	if err := s.FinishAutomationJob(first.ID, "worker-b", "success", "", 0); err == nil {
		t.Fatal("wrong lease owner completed job")
	}
	if err := s.FinishAutomationJob(first.ID, "worker-a", "success", "", 0); err != nil {
		t.Fatal(err)
	}
	third, err := s.EnqueueAutomationJob(&AutomationJob{
		Type:       "dns.reconcile",
		TargetType: "managed_domain",
		TargetID:   "domain-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if third.ID == first.ID {
		t.Fatal("completed job was incorrectly reused")
	}
}

func TestAutomationJobExpiredRunningLeaseIsRecovered(t *testing.T) {
	s := openTestStore(t)
	job, err := s.EnqueueAutomationJob(&AutomationJob{
		Type: "dns.reconcile", TargetType: "managed_domain", TargetID: "domain-expired",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	first, err := s.ClaimAutomationJobs("dead-worker", now, time.Minute, 10)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim=%+v err=%v", first, err)
	}
	recovered, err := s.ClaimAutomationJobs("new-worker", now.Add(2*time.Minute), time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].ID != job.ID ||
		recovered[0].LeaseOwner != "new-worker" || recovered[0].Attempt != 2 {
		t.Fatalf("recovered=%+v", recovered)
	}
}

func TestDeleteNodeCancelsDNSAndCertificateJobs(t *testing.T) {
	s := openTestStore(t)
	node, _, _, domain, acme := createDNSFixture(t, s)
	certificate := &ProtocolCertificate{
		NodeID: node.ID, ManagedDomainID: domain.ID, ACMEAccountID: acme.ID,
		Domains: []string{domain.FQDN}, Status: "pending",
	}
	if err := s.CreateProtocolCertificate(certificate); err != nil {
		t.Fatal(err)
	}
	for _, job := range []*AutomationJob{
		{Type: "dns.reconcile", TargetType: "managed_domain", TargetID: domain.ID},
		{Type: "certificate.issue", TargetType: "protocol_certificate", TargetID: certificate.ID},
	} {
		if _, err := s.EnqueueAutomationJob(job); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteNode(node.ID); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ListAutomationJobs(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("orphan jobs remain: %+v", jobs)
	}
}
