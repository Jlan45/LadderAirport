package subscription

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"
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

func TestFilterPlaceholderServers(t *testing.T) {
	eps := []ProxyEndpoint{
		{Name: "notice", Server: "0.0.0.0", Port: 443, Protocol: "trojan"},
		{Name: "v6", Server: "::", Port: 443, Protocol: "trojan"},
		{Name: "loop", Server: "127.0.0.1", Port: 443, Protocol: "trojan"},
		{Name: "ok", Server: "1.2.3.4", Port: 443, Protocol: "trojan"},
		{Name: "badport", Server: "1.2.3.4", Port: 0, Protocol: "trojan"},
	}
	out := filterDialableEndpoints(eps)
	if len(out) != 1 || out[0].Name != "ok" {
		t.Fatalf("%+v", out)
	}
}

func TestMergeDropsPlaceholderExternal(t *testing.T) {
	local := []ProxyEndpoint{{Name: "mine", Server: "9.9.9.9", Port: 443, Protocol: "trojan"}}
	ext := []ProxyEndpoint{
		{Name: "notice", Server: "0.0.0.0", Port: 443, Protocol: "trojan"},
		{Name: "good", Server: "8.8.8.8", Port: 443, Protocol: "trojan"},
	}
	out := MergeEndpoints(local, ext)
	if len(out) != 2 {
		t.Fatalf("got %d: %+v", len(out), out)
	}
	for _, ep := range out {
		if ep.Server == "0.0.0.0" {
			t.Fatalf("placeholder leaked: %+v", ep)
		}
	}
}

func TestDedupeConcurrentLookupBounded(t *testing.T) {
	orig := lookupHostIPs
	t.Cleanup(func() { lookupHostIPs = orig })
	var inflight atomic.Int32
	var peak atomic.Int32
	lookupHostIPs = func(ctx context.Context, host string) ([]net.IP, error) {
		cur := inflight.Add(1)
		for {
			prev := peak.Load()
			if cur <= prev || peak.CompareAndSwap(prev, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inflight.Add(-1)
		return []net.IP{net.ParseIP("10.9.9.9")}, nil
	}

	eps := make([]ProxyEndpoint, 0, 16)
	for i := 0; i < 16; i++ {
		eps = append(eps, ProxyEndpoint{
			Name: fmt.Sprintf("h%d", i), Server: fmt.Sprintf("h%d.example.com", i),
			Port: 443, Protocol: "trojan",
		})
	}
	start := time.Now()
	out := dedupeByHost(context.Background(), eps)
	elapsed := time.Since(start)
	// All hosts resolve to the same IP:port — only the first survives.
	if len(out) != 1 || out[0].Name != "h0" {
		t.Fatalf("%+v", out)
	}
	if got := peak.Load(); got > hostLookupConcurrency {
		t.Fatalf("lookup concurrency %d exceeds limit %d", got, hostLookupConcurrency)
	}
	if peak.Load() < 2 {
		t.Fatalf("lookups did not run concurrently (peak=%d)", peak.Load())
	}
	// Serial would take 16*20ms = 320ms; bounded concurrency needs ~2 rounds.
	if elapsed > 300*time.Millisecond {
		t.Fatalf("lookups look serial: %s", elapsed)
	}
}
