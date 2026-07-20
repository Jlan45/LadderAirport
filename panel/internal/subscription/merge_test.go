package subscription

import (
	"context"
	"net"
	"testing"
)

func TestDedupeSameIPDifferentPortKept(t *testing.T) {
	eps := []ProxyEndpoint{
		{Name: "a", Server: "1.2.3.4", Port: 443, Protocol: "trojan"},
		{Name: "b", Server: "1.2.3.4", Port: 8443, Protocol: "trojan"},
		{Name: "c", Server: "5.6.7.8", Port: 443, Protocol: "trojan"},
	}
	out := dedupeByHost(context.Background(), eps)
	if len(out) != 3 {
		t.Fatalf("got %d: %+v", len(out), out)
	}
}

func TestDedupeSameIPSamePortDropped(t *testing.T) {
	eps := []ProxyEndpoint{
		{Name: "a", Server: "1.2.3.4", Port: 443, Protocol: "trojan"},
		{Name: "b", Server: "1.2.3.4", Port: 443, Protocol: "vless"},
		{Name: "c", Server: "5.6.7.8", Port: 443, Protocol: "trojan"},
	}
	out := dedupeByHost(context.Background(), eps)
	if len(out) != 2 {
		t.Fatalf("got %d: %+v", len(out), out)
	}
	if out[0].Name != "a" || out[1].Name != "c" {
		t.Fatalf("%+v", out)
	}
}

func TestDedupeDomainSameIPDifferentPortKept(t *testing.T) {
	orig := lookupHostIPs
	t.Cleanup(func() { lookupHostIPs = orig })
	lookupHostIPs = func(ctx context.Context, host string) ([]net.IP, error) {
		switch host {
		case "a.example.com", "b.example.com":
			return []net.IP{net.ParseIP("10.0.0.1")}, nil
		case "c.example.com":
			return []net.IP{net.ParseIP("10.0.0.2")}, nil
		default:
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
	}

	// same resolved IP, different ports → keep both
	eps := []ProxyEndpoint{
		{Name: "local", Server: "a.example.com", Port: 443, Protocol: "trojan"},
		{Name: "other-port", Server: "b.example.com", Port: 8443, Protocol: "vless"},
		{Name: "other", Server: "c.example.com", Port: 443, Protocol: "ss"},
	}
	out := MergeEndpointsContext(context.Background(), eps[:1], eps[1:])
	if len(out) != 3 {
		t.Fatalf("got %d: %+v", len(out), out)
	}
}

func TestDedupeDomainSameIPSamePortDropped(t *testing.T) {
	orig := lookupHostIPs
	t.Cleanup(func() { lookupHostIPs = orig })
	lookupHostIPs = func(ctx context.Context, host string) ([]net.IP, error) {
		switch host {
		case "a.example.com", "b.example.com":
			return []net.IP{net.ParseIP("10.0.0.1")}, nil
		default:
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
	}

	eps := []ProxyEndpoint{
		{Name: "local", Server: "a.example.com", Port: 443, Protocol: "trojan"},
		{Name: "dup", Server: "b.example.com", Port: 443, Protocol: "vless"},
	}
	out := MergeEndpointsContext(context.Background(), eps[:1], eps[1:])
	if len(out) != 1 || out[0].Name != "local" {
		t.Fatalf("%+v", out)
	}
}

func TestDedupeDomainAndIPSamePortDropped(t *testing.T) {
	orig := lookupHostIPs
	t.Cleanup(func() { lookupHostIPs = orig })
	lookupHostIPs = func(ctx context.Context, host string) ([]net.IP, error) {
		if host == "edge.example.com" {
			return []net.IP{net.ParseIP("203.0.113.9")}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}

	local := []ProxyEndpoint{{Name: "mine", Server: "203.0.113.9", Port: 443, Protocol: "trojan"}}
	ext := []ProxyEndpoint{{Name: "theirs", Server: "edge.example.com", Port: 443, Protocol: "trojan"}}
	out := MergeEndpointsContext(context.Background(), local, ext)
	if len(out) != 1 || out[0].Name != "mine" {
		t.Fatalf("%+v", out)
	}
}

func TestDedupeDomainAndIPDifferentPortKept(t *testing.T) {
	orig := lookupHostIPs
	t.Cleanup(func() { lookupHostIPs = orig })
	lookupHostIPs = func(ctx context.Context, host string) ([]net.IP, error) {
		if host == "edge.example.com" {
			return []net.IP{net.ParseIP("203.0.113.9")}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}

	local := []ProxyEndpoint{{Name: "mine", Server: "203.0.113.9", Port: 443, Protocol: "trojan"}}
	ext := []ProxyEndpoint{{Name: "theirs", Server: "edge.example.com", Port: 8443, Protocol: "trojan"}}
	out := MergeEndpointsContext(context.Background(), local, ext)
	if len(out) != 2 {
		t.Fatalf("%+v", out)
	}
}

func TestDedupeLookupFailureFallsBackToNamePort(t *testing.T) {
	orig := lookupHostIPs
	t.Cleanup(func() { lookupHostIPs = orig })
	lookupHostIPs = func(ctx context.Context, host string) ([]net.IP, error) {
		return nil, &net.DNSError{Err: "timeout", Name: host, IsTimeout: true}
	}

	eps := []ProxyEndpoint{
		{Name: "a", Server: "x.example.com", Port: 1, Protocol: "trojan"},
		{Name: "b", Server: "x.example.com", Port: 2, Protocol: "trojan"}, // different port → keep
		{Name: "c", Server: "x.example.com", Port: 1, Protocol: "vless"},  // same host+port → drop
		{Name: "d", Server: "y.example.com", Port: 3, Protocol: "trojan"},
	}
	out := dedupeByHost(context.Background(), eps)
	if len(out) != 3 {
		t.Fatalf("got %d: %+v", len(out), out)
	}
}

func TestDedupeMultiAOrderIndependentSamePort(t *testing.T) {
	orig := lookupHostIPs
	t.Cleanup(func() { lookupHostIPs = orig })
	lookupHostIPs = func(ctx context.Context, host string) ([]net.IP, error) {
		if host == "a.example.com" {
			return []net.IP{net.ParseIP("1.1.1.1"), net.ParseIP("2.2.2.2")}, nil
		}
		if host == "b.example.com" {
			// reverse order
			return []net.IP{net.ParseIP("2.2.2.2"), net.ParseIP("1.1.1.1")}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	eps := []ProxyEndpoint{
		{Name: "a", Server: "a.example.com", Port: 443, Protocol: "trojan"},
		{Name: "b", Server: "b.example.com", Port: 443, Protocol: "trojan"},
	}
	out := dedupeByHost(context.Background(), eps)
	if len(out) != 1 || out[0].Name != "a" {
		t.Fatalf("%+v", out)
	}
}
