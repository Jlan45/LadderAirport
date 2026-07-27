package control

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicAddressResolverConsensus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4":
			fmt.Fprint(w, "192.0.2.44\n")
		case "/v6":
			fmt.Fprint(w, "2001:db8::44\n")
		}
	}))
	defer server.Close()
	resolver := &PublicAddressResolver{
		Client: server.Client(), Consensus: 2,
		IPv4URLs: []string{server.URL + "/v4?a=1", server.URL + "/v4?a=2"},
		IPv6URLs: []string{server.URL + "/v6?a=1", server.URL + "/v6?a=2"},
	}
	result, err := resolver.Resolve(context.Background(), true, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.IPv4 != "192.0.2.44" || result.IPv6 != "2001:db8::44" {
		t.Fatalf("result = %+v", result)
	}
}

func TestPublicAddressResolverRejectsPrivateAndConflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/one" {
			fmt.Fprint(w, "10.0.0.1")
		} else {
			fmt.Fprint(w, "192.0.2.10")
		}
	}))
	defer server.Close()
	resolver := &PublicAddressResolver{
		Client: server.Client(), Consensus: 2,
		IPv4URLs: []string{server.URL + "/one", server.URL + "/two"},
	}
	if _, err := resolver.Resolve(context.Background(), true, false); err == nil {
		t.Fatal("accepted private/conflicting results")
	}
}
