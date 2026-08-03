package control

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

type PublicAddressResolver struct {
	Client    *http.Client
	IPv4URLs  []string
	IPv6URLs  []string
	Consensus int
}

// ParseIPEchoURLs parses LADDER_IP_ECHO_URLS: a comma-separated list of
// endpoints that each return a plain-text IP address. Blank entries are
// dropped; an empty or unset value yields nil (keep the built-in defaults).
func ParseIPEchoURLs(value string) []string {
	var urls []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			urls = append(urls, item)
		}
	}
	return urls
}

type PublicAddressResult struct {
	IPv4        string
	IPv6        string
	IPv4Sources []string
	IPv6Sources []string
	Warnings    []string
}

func NewPublicAddressResolver() *PublicAddressResolver {
	return &PublicAddressResolver{
		Client: &http.Client{Timeout: 8 * time.Second},
		IPv4URLs: []string{
			"https://api4.ipify.org",
			"https://ipv4.icanhazip.com",
			"https://v4.ident.me",
		},
		IPv6URLs: []string{
			"https://api6.ipify.org",
			"https://ipv6.icanhazip.com",
			"https://v6.ident.me",
		},
		Consensus: 2,
	}
}

// SetEchoURLs replaces the probe endpoints for both address families with the
// same custom list. Each endpoint is tried for whichever family its response
// matches; responses for the wrong family are rejected as usual.
func (r *PublicAddressResolver) SetEchoURLs(urls []string) {
	r.IPv4URLs = append([]string(nil), urls...)
	r.IPv6URLs = append([]string(nil), urls...)
}

func (r *PublicAddressResolver) Resolve(ctx context.Context, ipv4, ipv6 bool) (PublicAddressResult, error) {
	if !ipv4 && !ipv6 {
		ipv4, ipv6 = true, true
	}
	result := PublicAddressResult{}
	if ipv4 {
		address, sources, err := r.resolveFamily(ctx, r.IPv4URLs, true)
		if err != nil {
			result.Warnings = append(result.Warnings, "IPv4 探测失败："+err.Error())
		} else {
			result.IPv4, result.IPv4Sources = address, sources
		}
	}
	if ipv6 {
		address, sources, err := r.resolveFamily(ctx, r.IPv6URLs, false)
		if err != nil {
			result.Warnings = append(result.Warnings, "IPv6 探测失败："+err.Error())
		} else {
			result.IPv6, result.IPv6Sources = address, sources
		}
	}
	if result.IPv4 == "" && result.IPv6 == "" {
		return result, fmt.Errorf("没有获得可信公网地址")
	}
	return result, nil
}

func (r *PublicAddressResolver) resolveFamily(
	ctx context.Context,
	endpoints []string,
	ipv4 bool,
) (string, []string, error) {
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	// Probe all endpoints concurrently; failed or invalid responses are skipped.
	type probeResult struct {
		address string
		source  string
	}
	results := make(chan probeResult, len(endpoints))
	group, groupCtx := errgroup.WithContext(ctx)
	for _, endpoint := range endpoints {
		group.Go(func() error {
			request, err := http.NewRequestWithContext(groupCtx, http.MethodGet, endpoint, nil)
			if err != nil {
				return nil
			}
			response, err := client.Do(request)
			if err != nil {
				return nil
			}
			raw, readErr := io.ReadAll(io.LimitReader(response.Body, 256))
			_ = response.Body.Close()
			if readErr != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
				return nil
			}
			address, err := netip.ParseAddr(strings.TrimSpace(string(raw)))
			if err != nil || address.Is4() != ipv4 || !publicAddress(address) {
				return nil
			}
			results <- probeResult{address: address.String(), source: endpointSource(endpoint)}
			return nil
		})
	}
	_ = group.Wait()
	close(results)
	counts := map[string]int{}
	sources := map[string][]string{}
	for result := range results {
		counts[result.address]++
		sources[result.address] = append(sources[result.address], result.source)
	}
	consensus := r.Consensus
	if consensus <= 0 {
		consensus = 2
	}
	best := ""
	for address, count := range counts {
		if count >= consensus && (best == "" || count > counts[best]) {
			best = address
		}
	}
	if best == "" {
		return "", nil, fmt.Errorf("探测来源未达到 %d 个一致结果", consensus)
	}
	sort.Strings(sources[best])
	return best, sources[best], nil
}

func publicAddress(address netip.Addr) bool {
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() ||
		address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsUnspecified() {
		return false
	}
	return !netip.MustParsePrefix("100.64.0.0/10").Contains(address)
}

func endpointSource(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" {
		return "未知来源"
	}
	return parsed.Hostname()
}
