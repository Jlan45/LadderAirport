package subscription

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	FetchTimeout  = 15 * time.Second
	MaxBodyBytes  = 4 << 20 // 4 MiB
	MaxRedirects  = 3
	defaultUA     = "LadderAirport-Panel/1.0"
)

// FetchURL downloads a subscription body with SSRF protections.
func FetchURL(ctx context.Context, rawURL string, headers map[string]string) ([]byte, error) {
	u, err := parsePublicHTTPURL(rawURL)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout: FetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= MaxRedirects {
				return fmt.Errorf("too many redirects")
			}
			if err := validatePublicURL(req.URL); err != nil {
				return err
			}
			return nil
		},
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(address)
				if err != nil {
					host = address
					port = ""
				}
				ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil {
					return nil, err
				}
				var lastErr error
				d := net.Dialer{Timeout: FetchTimeout}
				for _, ipa := range ips {
					if isBlockedIP(ipa.IP) {
						lastErr = fmt.Errorf("blocked address %s", ipa.IP)
						continue
					}
					addr := ipa.IP.String()
					if ipa.IP.To4() == nil {
						addr = "[" + addr + "]"
					}
					if port != "" {
						addr = net.JoinHostPort(ipa.IP.String(), port)
					}
					conn, err := d.DialContext(ctx, network, addr)
					if err != nil {
						lastErr = err
						continue
					}
					return conn, nil
				}
				if lastErr == nil {
					lastErr = fmt.Errorf("no safe addresses for %s", host)
				}
				return nil, lastErr
			},
			// Disable HTTP/2 optional; keep defaults otherwise.
			ForceAttemptHTTP2: true,
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", defaultUA)
	req.Header.Set("Accept", "*/*")
	for k, v := range headers {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch: HTTP %s", resp.Status)
	}
	limited := io.LimitReader(resp.Body, MaxBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("fetch read: %w", err)
	}
	if len(body) > MaxBodyBytes {
		return nil, fmt.Errorf("fetch: body exceeds %d bytes", MaxBodyBytes)
	}
	return body, nil
}

func parsePublicHTTPURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("url required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	if err := validatePublicURL(u); err != nil {
		return nil, err
	}
	return u, nil
}

func validatePublicURL(u *url.URL) error {
	if u == nil {
		return fmt.Errorf("nil url")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("missing host")
	}
	// Block obvious local hostnames without DNS.
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") ||
		lower == "metadata.google.internal" {
		return fmt.Errorf("blocked host %q", host)
	}
	// If host is a literal IP, check immediately.
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("blocked address %s", ip)
		}
	}
	return nil
}

func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	// CGNAT 100.64.0.0/10
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
		// AWS/GCP metadata
		if ip4[0] == 169 && ip4[1] == 254 {
			return true
		}
	}
	return false
}
