package subscription

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// lookupHostIPs resolves a host to IP addresses.
// Overridden in tests.
var lookupHostIPs = defaultLookupHostIPs

const hostLookupTimeout = 2 * time.Second

func defaultLookupHostIPs(ctx context.Context, host string) ([]net.IP, error) {
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), hostLookupTimeout)
		defer cancel()
	} else if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, hostLookupTimeout)
		defer cancel()
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	out := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		if a.IP != nil {
			out = append(out, a.IP)
		}
	}
	return out, nil
}

// MergeEndpoints concatenates local then external endpoints, drops host+port duplicates
// (domains resolved to real IPs), then uniquifies names.
// Same IP with different ports are kept. Local endpoints are preferred on collision.
func MergeEndpoints(local, external []ProxyEndpoint) []ProxyEndpoint {
	return MergeEndpointsContext(context.Background(), local, external)
}

// MergeEndpointsContext is like MergeEndpoints but uses ctx for DNS lookups.
func MergeEndpointsContext(ctx context.Context, local, external []ProxyEndpoint) []ProxyEndpoint {
	n := len(local) + len(external)
	if n == 0 {
		return []ProxyEndpoint{}
	}
	out := make([]ProxyEndpoint, 0, n)
	out = append(out, local...)
	out = append(out, external...)
	out = filterDialableEndpoints(out)
	out = dedupeByHost(ctx, out)
	return uniquifyNames(out)
}

// prefixExternalName builds "{source}-{proxy}" and sanitizes.
func prefixExternalName(sourceName, proxyName string) string {
	return sanitizeName(fmt.Sprintf("%s-%s", sourceName, proxyName))
}

// dedupeByHost keeps the first endpoint for each real host:port identity.
// Domains are resolved; if any resolved IP:port was already seen, the endpoint is dropped.
// Literal IPs compare as themselves. Lookup failures fall back to hostname:port.
// Same IP with different ports are treated as distinct.
// Unique hosts are resolved concurrently (bounded by hostLookupConcurrency).
func dedupeByHost(ctx context.Context, eps []ProxyEndpoint) []ProxyEndpoint {
	if len(eps) <= 1 {
		return eps
	}
	resolveCache := prefetchHostIdentities(ctx, eps)
	seenIPPort := map[string]bool{}   // "ip:port"
	seenNamePort := map[string]bool{} // "name:host:port" when DNS fails
	out := make([]ProxyEndpoint, 0, len(eps))
	for _, ep := range eps {
		ids := hostIdentity(ctx, ep.Server, resolveCache)
		if len(ids) == 0 {
			out = append(out, ep)
			continue
		}
		port := ep.Port
		// Name-fallback keys (DNS failed): only collide with same hostname+port.
		if strings.HasPrefix(ids[0], "name:") {
			k := fmt.Sprintf("%s:%d", ids[0], port)
			if seenNamePort[k] {
				continue
			}
			seenNamePort[k] = true
			out = append(out, ep)
			continue
		}
		// IP identities: drop if any IP:port already seen.
		dup := false
		for _, ip := range ids {
			if seenIPPort[fmt.Sprintf("%s:%d", ip, port)] {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		for _, ip := range ids {
			seenIPPort[fmt.Sprintf("%s:%d", ip, port)] = true
		}
		out = append(out, ep)
	}
	return out
}

// hostLookupConcurrency bounds parallel DNS lookups during endpoint merge.
const hostLookupConcurrency = 8

// prefetchHostIdentities resolves every unique non-IP host in eps concurrently
// and returns a cache of host -> ip strings (or ["name:"+host] on failure).
func prefetchHostIdentities(ctx context.Context, eps []ProxyEndpoint) map[string][]string {
	unique := map[string]struct{}{}
	for _, ep := range eps {
		host := normalizeHost(ep.Server)
		if host == "" || net.ParseIP(host) != nil {
			continue
		}
		unique[host] = struct{}{}
	}
	cache := make(map[string][]string, len(unique))
	if len(unique) == 0 {
		return cache
	}
	var mu sync.Mutex
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(hostLookupConcurrency)
	for host := range unique {
		group.Go(func() error {
			resolved := resolveHostIdentity(groupCtx, host)
			mu.Lock()
			cache[host] = resolved
			mu.Unlock()
			return nil
		})
	}
	// Lookups always soft-fail into the cache, so Wait never errors.
	_ = group.Wait()
	return cache
}

// resolveHostIdentity performs one DNS lookup, falling back to "name:host".
func resolveHostIdentity(ctx context.Context, host string) []string {
	ips, err := lookupHostIPs(ctx, host)
	if err != nil || len(ips) == 0 {
		return []string{"name:" + host}
	}
	set := map[string]struct{}{}
	for _, ip := range ips {
		set[ip.String()] = struct{}{}
	}
	ids := make([]string, 0, len(set))
	for s := range set {
		ids = append(ids, s)
	}
	return ids
}

// hostIdentity returns either a list of IP strings, or a single "name:host" fallback.
func hostIdentity(ctx context.Context, server string, cache map[string][]string) []string {
	host := normalizeHost(server)
	if host == "" {
		return nil
	}
	if ids, ok := cache[host]; ok {
		return ids
	}
	if ip := net.ParseIP(host); ip != nil {
		ids := []string{ip.String()}
		cache[host] = ids
		return ids
	}
	ips, err := lookupHostIPs(ctx, host)
	if err != nil || len(ips) == 0 {
		ids := []string{"name:" + host}
		cache[host] = ids
		return ids
	}
	// Unique IP strings (order irrelevant for membership checks).
	set := map[string]struct{}{}
	for _, ip := range ips {
		set[ip.String()] = struct{}{}
	}
	ids := make([]string, 0, len(set))
	for s := range set {
		ids = append(ids, s)
	}
	cache[host] = ids
	return ids
}

func normalizeHost(server string) string {
	s := strings.TrimSpace(server)
	if s == "" {
		return ""
	}
	// Strip optional brackets for IPv6 literals like [::1]
	if strings.HasPrefix(s, "[") {
		if i := strings.Index(s, "]"); i > 0 {
			s = s[1:i]
		}
	}
	// If someone passed host:port, keep host only (defensive).
	if h, _, err := net.SplitHostPort(s); err == nil {
		s = h
	}
	return strings.ToLower(strings.TrimSpace(s))
}

// isDialableServer reports whether server is usable as a client dial target.
// Drops empty, unspecified (0.0.0.0 / ::), and common placeholder hosts used by
// airport "notice" nodes.
func isDialableServer(server string) bool {
	host := normalizeHost(server)
	if host == "" {
		return false
	}
	switch host {
	case "0.0.0.0", "::", "::0", "0", "localhost", "local":
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
			return false
		}
	}
	return true
}

// filterDialableEndpoints drops endpoints with unusable server or port.
func filterDialableEndpoints(eps []ProxyEndpoint) []ProxyEndpoint {
	if len(eps) == 0 {
		return eps
	}
	out := make([]ProxyEndpoint, 0, len(eps))
	for _, ep := range eps {
		if ep.Port < 1 || ep.Port > 65535 {
			continue
		}
		if !isDialableServer(ep.Server) {
			continue
		}
		out = append(out, ep)
	}
	return out
}

func uniquifyNames(eps []ProxyEndpoint) []ProxyEndpoint {
	seen := map[string]int{}
	for i := range eps {
		base := eps[i].Name
		if base == "" {
			base = "proxy"
			eps[i].Name = base
		}
		if n, ok := seen[strings.ToLower(eps[i].Name)]; ok {
			// append -2, -3, ...
			for {
				n++
				candidate := fmt.Sprintf("%s-%d", base, n)
				if _, exists := seen[strings.ToLower(candidate)]; !exists {
					eps[i].Name = candidate
					seen[strings.ToLower(candidate)] = 1
					seen[strings.ToLower(base)] = n
					break
				}
			}
		} else {
			seen[strings.ToLower(eps[i].Name)] = 1
		}
	}
	return eps
}
