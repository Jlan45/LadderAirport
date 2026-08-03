package converter

import (
	"encoding/json"
	"testing"

	"github.com/ladderairport/panel/internal/store"
)

func testSSInbound(id string, port int) store.InboundConfig {
	return store.InboundConfig{
		ID: id, Name: "ss-" + id, Protocol: "shadowsocks", Enabled: true,
		Params: map[string]any{
			"listen": "0.0.0.0", "port": port, "method": "aes-128-gcm", "password": "pw-" + id,
		},
	}
}

// decodeConfig unmarshals a generated config and returns its route rules.
func decodeRouteRules(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	route, ok := cfg["route"].(map[string]any)
	if !ok {
		t.Fatalf("route = %v", cfg["route"])
	}
	if route["final"] != "direct" {
		t.Fatalf("final = %v", route["final"])
	}
	rulesRaw, _ := route["rules"].([]any)
	out := []map[string]any{}
	for _, item := range rulesRaw {
		rule, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("rule = %v", item)
		}
		out = append(out, rule)
	}
	return out
}

func TestConvertRouteRulesAfterChainRules(t *testing.T) {
	inbounds := []store.InboundConfig{testSSInbound("a1", 11001), testSSInbound("b2", 11002)}
	chainOutbound := map[string]any{
		"type": "shadowsocks", "tag": "chain-deadbeef-hop-0-next",
		"server": "198.51.100.2", "server_port": 443, "method": "aes-128-gcm", "password": "x",
	}
	raw, err := Convert(inbounds, ConvertOptions{
		ChainRoutes: []ChainRoute{{InboundID: "b2", Outbound: chainOutbound}},
		RouteRules: []RouteRule{
			{MatchType: "domain", MatchValue: "www.corp.example", Outbound: "chain-deadbeef-hop-0-next"},
			{MatchType: "domain_suffix", MatchValue: "internal.example", Outbound: "direct"},
			{MatchType: "ip_cidr", MatchValue: "203.0.113.0/24", Reject: true},
			{MatchType: "process_name", MatchValue: "telegram", Outbound: "direct"}, // agent-side: skipped
			{MatchType: "domain", MatchValue: "dangling.example", Outbound: "no-such-outbound"},
			{MatchType: "domain_keyword", MatchValue: "", Outbound: "direct"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rules := decodeRouteRules(t, raw)
	// 1 chain rule + 3 valid plan rules; process_name/dangling/empty skipped.
	if len(rules) != 4 {
		t.Fatalf("rules = %v", rules)
	}
	// Chain rule stays first.
	if rule := rules[0]; rule["action"] != "route" || rule["outbound"] != "chain-deadbeef-hop-0-next" {
		t.Fatalf("chain rule = %v", rule)
	}
	if in, ok := rules[0]["inbound"].([]any); !ok || len(in) != 1 {
		t.Fatalf("chain rule inbound = %v", rules[0]["inbound"])
	}
	if rule := rules[1]; rule["action"] != "route" || rule["outbound"] != "chain-deadbeef-hop-0-next" {
		t.Fatalf("proxy plan rule = %v", rule)
	}
	if domains, ok := rules[1]["domain"].([]any); !ok || len(domains) != 1 || domains[0] != "www.corp.example" {
		t.Fatalf("proxy plan rule domain = %v", rules[1]["domain"])
	}
	if rule := rules[2]; rule["action"] != "route" || rule["outbound"] != "direct" {
		t.Fatalf("direct plan rule = %v", rule)
	}
	if _, ok := rules[2]["domain_suffix"]; !ok {
		t.Fatalf("direct plan rule condition = %v", rules[2])
	}
	if rule := rules[3]; rule["action"] != "reject" {
		t.Fatalf("block plan rule = %v", rule)
	}
	if _, hasOutbound := rules[3]["outbound"]; hasOutbound {
		t.Fatalf("reject rule must not carry outbound: %v", rules[3])
	}
	if _, ok := rules[3]["ip_cidr"]; !ok {
		t.Fatalf("block plan rule condition = %v", rules[3])
	}
}

func TestConvertRouteRulesNilKeepsLegacyShape(t *testing.T) {
	raw, err := Convert([]store.InboundConfig{testSSInbound("a1", 11001)}, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	route := cfg["route"].(map[string]any)
	if _, hasRules := route["rules"]; hasRules {
		t.Fatalf("route = %v", route)
	}
}
