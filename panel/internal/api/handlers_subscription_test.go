package api

import (
	"testing"

	"github.com/ladderairport/panel/internal/store"
)

func TestSubFilenameUsesSubscriptionName(t *testing.T) {
	cases := []struct {
		name, format, want string
	}{
		{"香港主力", "clash", "香港主力.yaml"},
		{"My Sub", "singbox", "My-Sub.json"},
		{"a/b:c", "clash", "abc.yaml"},
		{"", "clash", "subscription.yaml"},
		{"  spaced  name  ", "singbox", "spaced-name.json"},
	}
	for _, tc := range cases {
		got := subFilename(&store.Subscription{Name: tc.name, Format: tc.format})
		if got != tc.want {
			t.Fatalf("name=%q format=%q got %q want %q", tc.name, tc.format, got, tc.want)
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
