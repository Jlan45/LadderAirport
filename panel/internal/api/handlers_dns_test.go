package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ladderairport/panel/internal/store"
)

func TestDNSAccountSecretsAreEncryptedAndMasked(t *testing.T) {
	ts, client, st := newTestServer(t, nil, nil)
	resp := login(t, client, ts.URL, "admin")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}

	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/dns/accounts", map[string]any{
		"name":     "Cloudflare",
		"provider": "cloudflare",
		"credentials": map[string]string{
			"api_token": "super-secret-token",
		},
		"settings": map[string]any{"test_zone": "example.com"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d body=%v", resp.StatusCode, body)
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "super-secret-token") {
		t.Fatalf("secret leaked in response: %s", raw)
	}
	id, _ := body["id"].(string)
	account, err := st.GetDNSAccount(id)
	if err != nil {
		t.Fatal(err)
	}
	if account.CredentialsCiphertext == "" ||
		strings.Contains(account.CredentialsCiphertext, "super-secret-token") {
		t.Fatalf("credentials were not encrypted: %q", account.CredentialsCiphertext)
	}

	resp, body = doJSON(t, client, http.MethodPut, ts.URL+"/api/v1/dns/accounts/"+id, map[string]any{
		"name":     "Cloudflare Updated",
		"provider": "cloudflare",
	})
	if resp.StatusCode != http.StatusOK || body["has_credentials"] != true {
		t.Fatalf("update status=%d body=%v", resp.StatusCode, body)
	}
	updated, _ := st.GetDNSAccount(id)
	if updated.CredentialsCiphertext != account.CredentialsCiphertext {
		t.Fatal("omitted credential unexpectedly changed")
	}
}

func TestManagedDomainCreateNormalizesAndEnqueues(t *testing.T) {
	ts, client, st := newTestServer(t, nil, nil)
	resp := login(t, client, ts.URL, "admin")
	resp.Body.Close()

	node := &store.Node{Name: "edge", Address: "192.0.2.10", GRPCPort: 50051, Status: "unknown"}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	resp, account := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/dns/accounts", map[string]any{
		"name": "Callback", "provider": "callback",
		"credentials": map[string]string{"token": "secret"},
		"settings": map[string]any{
			"url":                   "http://127.0.0.1/callback",
			"allow_private_network": true,
			"test_zone":             "example.com",
		},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("account status=%d body=%v", resp.StatusCode, account)
	}
	resp, domain := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/managed-domains", map[string]any{
		"node_id": node.ID, "dns_account_id": account["id"],
		"zone": "Example.COM.", "fqdn": "节点.Example.COM.",
		"record_mode": "a", "address_source": "manual",
		"manual_ipv4": "192.0.2.10", "ttl": 300,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("domain status=%d body=%v", resp.StatusCode, domain)
	}
	if domain["fqdn"] != "xn--3px729a.example.com" || domain["zone"] != "example.com" {
		t.Fatalf("domain was not normalized: %v", domain)
	}
	jobs, err := st.ListAutomationJobs(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Type != "dns.reconcile" ||
		jobs[0].TargetID != domain["id"] {
		t.Fatalf("unexpected jobs: %+v", jobs)
	}
}

func TestDNSProviderMetadataRequiresAuthentication(t *testing.T) {
	ts, client, _ := newTestServer(t, nil, nil)
	resp, err := client.Get(ts.URL + "/api/v1/dns/providers")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
}
