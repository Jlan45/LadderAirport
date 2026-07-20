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
