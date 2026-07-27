package api_test

import (
	"encoding/json"
	"fmt"
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
		"zone":     "Example.COM.",
		"credentials": map[string]string{
			"api_token": "super-secret-token",
		},
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
	if account.Zone != "example.com" {
		t.Fatalf("zone was not normalized: %q", account.Zone)
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

func TestDNSAccountRequiresZone(t *testing.T) {
	ts, client, _ := newTestServer(t, nil, nil)
	resp := login(t, client, ts.URL, "admin")
	resp.Body.Close()

	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/dns/accounts", map[string]any{
		"name": "Cloudflare", "provider": "cloudflare",
		"credentials": map[string]string{"api_token": "secret"},
	})
	if resp.StatusCode != http.StatusBadRequest ||
		!strings.Contains(fmt.Sprint(body["error"]), "区域") {
		t.Fatalf("status=%d body=%v", resp.StatusCode, body)
	}
}

func TestDNSAccountCanBeDeletedThroughAPI(t *testing.T) {
	ts, client, st := newTestServer(t, nil, nil)
	resp := login(t, client, ts.URL, "admin")
	resp.Body.Close()

	resp, account := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/dns/accounts", map[string]any{
		"name": "Disposable DNS", "provider": "cloudflare", "zone": "example.com",
		"credentials": map[string]string{"api_token": "secret"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d body=%v", resp.StatusCode, account)
	}
	id, _ := account["id"].(string)
	resp, body := doJSON(
		t, client, http.MethodDelete, ts.URL+"/api/v1/dns/accounts/"+id, nil,
	)
	if resp.StatusCode != http.StatusNoContent || body != nil {
		t.Fatalf("delete status=%d body=%v", resp.StatusCode, body)
	}
	if _, err := st.GetDNSAccount(id); err == nil {
		t.Fatal("DNS 账号删除后仍可读取")
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
		"name": "Cloudflare DNS", "provider": "cloudflare", "zone": "Example.COM.",
		"credentials": map[string]string{"api_token": "secret"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("account status=%d body=%v", resp.StatusCode, account)
	}
	resp, domain := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/managed-domains", map[string]any{
		"node_id": node.ID, "dns_account_id": account["id"],
		"fqdn":        "节点",
		"record_mode": "a", "address_source": "manual",
		"manual_ipv4": "192.0.2.10", "ttl": 300,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("domain status=%d body=%v", resp.StatusCode, domain)
	}
	if domain["fqdn"] != "xn--3px729a.example.com" || domain["zone"] != "example.com" {
		t.Fatalf("domain was not normalized: %v", domain)
	}
	resp, mismatched := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/managed-domains", map[string]any{
		"node_id": node.ID, "dns_account_id": account["id"],
		"zone": "example.net", "fqdn": "edge.example.net",
		"record_mode": "a", "address_source": "manual",
		"manual_ipv4": "192.0.2.11", "ttl": 300,
	})
	if resp.StatusCode != http.StatusBadRequest ||
		!strings.Contains(fmt.Sprint(mismatched["error"]), "账号区域") {
		t.Fatalf("mismatched zone status=%d body=%v", resp.StatusCode, mismatched)
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
