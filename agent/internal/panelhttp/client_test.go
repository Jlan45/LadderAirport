package panelhttp

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestClientReusesTLSConnection(t *testing.T) {
	var newConns atomic.Int32
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	ts.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	ts.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			newConns.Add(1)
		}
	}
	ts.StartTLS()
	defer ts.Close()

	tr := NewTransport()
	tr.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
		ClientSessionCache: tls.NewLRUClientSessionCache(4),
	}
	client := &http.Client{Timeout: RequestTimeout, Transport: tr}

	for i := 0; i < 3; i++ {
		resp, err := client.Get(ts.URL + "/api/v1/agent/report")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	}
	if got := newConns.Load(); got != 1 {
		t.Fatalf("new TLS connections = %d, want 1 (keep-alive reuse)", got)
	}
}
