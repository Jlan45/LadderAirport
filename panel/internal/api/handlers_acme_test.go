package api_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestACMEAccountKeyAndEABAreEncryptedAndMasked(t *testing.T) {
	ts, client, st := newTestServer(t, nil, nil)
	resp := login(t, client, ts.URL, "admin")
	resp.Body.Close()

	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/acme/accounts", map[string]any{
		"name":          "Let's Encrypt Staging",
		"directory_url": "https://acme-staging-v02.api.letsencrypt.org/directory",
		"email":         "ops@example.com",
		"eab_key_id":    "kid-1",
		"eab_hmac":      "c3VwZXItc2VjcmV0",
		"accept_terms":  true,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d body=%v", resp.StatusCode, body)
	}
	if body["has_account_key"] != true || body["has_eab_hmac"] != true {
		t.Fatalf("masked flags missing: %v", body)
	}
	id, _ := body["id"].(string)
	account, err := st.GetACMEAccount(id)
	if err != nil {
		t.Fatal(err)
	}
	if account.AccountKeyCiphertext == "" || account.EABHMACCiphertext == "" ||
		strings.Contains(account.EABHMACCiphertext, "c3VwZXItc2VjcmV0") {
		t.Fatalf("ACME secrets not encrypted: %+v", account)
	}
}

func TestACMEAccountUpdatePreservesOmittedEABSecret(t *testing.T) {
	ts, client, st := newTestServer(t, nil, nil)
	resp := login(t, client, ts.URL, "admin")
	resp.Body.Close()
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/acme/accounts", map[string]any{
		"name": "CA", "directory_url": "https://ca.example/directory",
		"eab_key_id": "kid-1", "eab_hmac": "c2VjcmV0", "accept_terms": true,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d body=%v", resp.StatusCode, body)
	}
	id := body["id"].(string)
	before, _ := st.GetACMEAccount(id)
	resp, body = doJSON(t, client, http.MethodPut, ts.URL+"/api/v1/acme/accounts/"+id, map[string]any{
		"name": "CA Updated", "eab_key_id": "kid-1",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status=%d body=%v", resp.StatusCode, body)
	}
	after, _ := st.GetACMEAccount(id)
	if after.EABHMACCiphertext != before.EABHMACCiphertext ||
		after.AccountKeyCiphertext != before.AccountKeyCiphertext {
		t.Fatal("omitted ACME secret unexpectedly changed")
	}
}
