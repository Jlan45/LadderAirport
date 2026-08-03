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

func TestParseIPEchoURLs(t *testing.T) {
	if got := ParseIPEchoURLs(""); got != nil {
		t.Fatalf("empty env should yield nil, got %v", got)
	}
	got := ParseIPEchoURLs(" https://a.example.com/ip ,,https://b.example.com/ ,")
	want := []string{"https://a.example.com/ip", "https://b.example.com/"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestPublicAddressResolverCustomEchoURLs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4", "/v4-b":
			fmt.Fprint(w, "192.0.2.44\n")
		case "/v6", "/v6-b":
			fmt.Fprint(w, "2001:db8::44\n")
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	resolver := NewPublicAddressResolver()
	resolver.SetEchoURLs([]string{
		server.URL + "/v4", server.URL + "/v4-b",
		server.URL + "/v6", server.URL + "/v6-b",
		server.URL + "/bad", // 坏源应被跳过，不影响共识
	})
	if len(resolver.IPv4URLs) != 5 || len(resolver.IPv6URLs) != 5 {
		t.Fatalf("SetEchoURLs did not replace both families: %+v", resolver)
	}
	result, err := resolver.Resolve(context.Background(), true, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.IPv4 != "192.0.2.44" || result.IPv6 != "2001:db8::44" {
		t.Fatalf("result = %+v", result)
	}
}

func TestPublicAddressResolverDefaults(t *testing.T) {
	resolver := NewPublicAddressResolver()
	if len(resolver.IPv4URLs) == 0 || len(resolver.IPv6URLs) == 0 {
		t.Fatal("default resolver must keep built-in probe sources")
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
