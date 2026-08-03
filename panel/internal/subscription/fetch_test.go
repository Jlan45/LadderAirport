package subscription

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchURLOK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("trojan://pw@example.com:443#x\n"))
	}))
	defer ts.Close()

	// httptest uses 127.0.0.1 — should be blocked by SSRF.
	_, err := FetchURL(t.Context(), ts.URL, nil)
	if err == nil {
		t.Fatal("expected SSRF block for loopback")
	}
}

func TestValidatePublicURL(t *testing.T) {
	cases := []struct {
		raw string
		ok  bool
	}{
		{"https://example.com/sub", true},
		{"http://1.2.3.4/a", true},
		{"file:///etc/passwd", false},
		{"https://localhost/x", false},
		{"http://127.0.0.1/x", false},
		{"http://10.0.0.1/x", false},
		{"http://169.254.169.254/latest", false},
	}
	for _, c := range cases {
		_, err := parsePublicHTTPURL(c.raw)
		if c.ok && err != nil {
			t.Fatalf("%s: unexpected err %v", c.raw, err)
		}
		if !c.ok && err == nil {
			t.Fatalf("%s: expected error", c.raw)
		}
	}
}

func TestRedirectStripsCustomHeadersCrossHost(t *testing.T) {
	first, _ := http.NewRequest(http.MethodGet, "https://a.example.com/sub", nil)
	next, _ := http.NewRequest(http.MethodGet, "https://b.example.com/sub", nil)
	next.Header.Set("X-Token", "secret")
	check := redirectChecker(map[string]string{"X-Token": "secret"})
	if err := check(next, []*http.Request{first}); err != nil {
		t.Fatal(err)
	}
	if next.Header.Get("X-Token") != "" {
		t.Fatal("custom header leaked across hosts")
	}
}

func TestRedirectKeepsCustomHeadersSameHost(t *testing.T) {
	first, _ := http.NewRequest(http.MethodGet, "https://a.example.com/sub", nil)
	next, _ := http.NewRequest(http.MethodGet, "https://a.example.com/renamed", nil)
	next.Header.Set("X-Token", "secret")
	check := redirectChecker(map[string]string{"X-Token": "secret"})
	if err := check(next, []*http.Request{first}); err != nil {
		t.Fatal(err)
	}
	if next.Header.Get("X-Token") != "secret" {
		t.Fatal("custom header dropped on same-host redirect")
	}
}
