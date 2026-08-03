package api

import (
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/ladderairport/panel/internal/store"
)

// trustedProxyNets parses the comma-separated trusted_proxy_cidrs setting.
// Invalid entries are ignored.
func trustedProxyNets(st *store.Settings) []netip.Prefix {
	if st == nil {
		return nil
	}
	var out []netip.Prefix
	for _, part := range strings.Split(st.TrustedProxyCIDRs, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(part); err == nil {
			out = append(out, prefix.Masked())
		}
	}
	return out
}

// remoteAddrTrusted reports whether the direct peer is a trusted proxy.
func remoteAddrTrusted(r *http.Request, nets []netip.Prefix) bool {
	if len(nets) == 0 {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	}
	for _, prefix := range nets {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// requestScheme returns "https" for direct TLS, and honors X-Forwarded-Proto
// only when the direct peer matches trusted_proxy_cidrs.
func (s *Server) requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if xf := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); xf != "" {
		if st, err := s.Store.GetSettings(); err == nil && remoteAddrTrusted(r, trustedProxyNets(st)) {
			return xf
		}
	}
	return "http"
}

// requestIsHTTPS reports whether the request arrived over HTTPS (directly or
// via a trusted proxy); it drives the session cookie Secure flag.
func (s *Server) requestIsHTTPS(r *http.Request) bool {
	return s.requestScheme(r) == "https"
}
