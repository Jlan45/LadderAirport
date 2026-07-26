package api

import (
	"net/http/httptest"
	"testing"

	"github.com/ladderairport/panel/internal/store"
)

func TestSubFilenameUsesSubscriptionName(t *testing.T) {
	cases := []struct {
		name, format, want string
	}{
		{"香港主力", "clash", "香港主力.yaml"},
		{"My Sub", "singbox", "My-Sub.json"},
		{"V2Ray Sub", "v2ray", "V2Ray-Sub.txt"},
		{"a/b:c", "clash", "abc.yaml"},
		{"", "clash", "subscription.yaml"},
		{"  spaced  name  ", "singbox", "spaced-name.json"},
	}
	for _, tc := range cases {
		got := subFilename(&store.Subscription{Name: tc.name}, tc.format)
		if got != tc.want {
			t.Fatalf("name=%q format=%q got %q want %q", tc.name, tc.format, got, tc.want)
		}
	}
}

func TestDetectFormat(t *testing.T) {
	cases := []struct {
		urlStr string
		ua     string
		want   string
	}{
		{"http://example.com/sub/token?flag=clash", "", "clash"},
		{"http://example.com/sub/token?flag=singbox", "", "singbox"},
		{"http://example.com/sub/token?format=v2ray", "", "v2ray"},
		{"http://example.com/sub/token?clash=1", "", "clash"},
		{"http://example.com/sub/token", "ClashMeta/v1.18.0", "clash"},
		{"http://example.com/sub/token", "sing-box 1.9.0", "singbox"},
		{"http://example.com/sub/token", "v2rayN/6.23", "v2ray"},
		{"http://example.com/sub/token", "Shadowrocket/2.2.0", "v2ray"},
		{"http://example.com/sub/token", "Mozilla/5.0", "v2ray"},
	}

	for _, tc := range cases {
		req := httptest.NewRequest("GET", tc.urlStr, nil)
		if tc.ua != "" {
			req.Header.Set("User-Agent", tc.ua)
		}
		got := detectFormat(req)
		if got != tc.want {
			t.Errorf("url=%q ua=%q got %q want %q", tc.urlStr, tc.ua, got, tc.want)
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	if got := sanitizeFilename(`evil/../x`); got != "evil..x" && got != "evilx" {
		// path seps stripped: "evil../x" -> seps removed -> "evil..x"
		if got != "evil..x" {
			t.Fatalf("got %q", got)
		}
	}
	if got := sanitizeFilename(""); got != "" {
		t.Fatalf("empty = %q", got)
	}
}
