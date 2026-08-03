package subscription

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ladderairport/panel/internal/store"
	"gopkg.in/yaml.v3"
)

func samplePlanRules() []store.RoutePlanRule {
	return []store.RoutePlanRule{
		{Position: 0, MatchType: "domain", MatchValue: "www.corp.example", Action: "proxy", Enabled: true},
		{Position: 1, MatchType: "domain_suffix", MatchValue: "internal.example", Action: "direct", Enabled: true},
		{Position: 2, MatchType: "domain_keyword", MatchValue: "ads", Action: "block", Enabled: true},
		{Position: 3, MatchType: "ip_cidr", MatchValue: "203.0.113.0/24", Action: "direct", Enabled: true},
		{Position: 4, MatchType: "process_name", MatchValue: "telegram", Action: "proxy", Enabled: true},
		{Position: 5, MatchType: "domain", MatchValue: "off.example", Action: "block", Enabled: false},
	}
}

func TestRenderClashWithPlanRules(t *testing.T) {
	b, err := RenderClashWithRules(sampleEndpoints(), samplePlanRules())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	rulesRaw, ok := doc["rules"].([]any)
	if !ok {
		t.Fatalf("rules = %v", doc["rules"])
	}
	rules := make([]string, 0, len(rulesRaw))
	for _, item := range rulesRaw {
		rules = append(rules, item.(string))
	}
	wantPrefix := []string{
		"DOMAIN,www.corp.example," + clashGroupSelect,
		"DOMAIN-SUFFIX,internal.example,DIRECT",
		"DOMAIN-KEYWORD,ads,REJECT",
		"IP-CIDR,203.0.113.0/24,DIRECT,no-resolve",
		"PROCESS-NAME,telegram," + clashGroupSelect,
	}
	if len(rules) < len(wantPrefix) {
		t.Fatalf("rules = %v", rules)
	}
	for i, want := range wantPrefix {
		if rules[i] != want {
			t.Fatalf("rules[%d] = %q, want %q (all: %v)", i, rules[i], want, rules)
		}
	}
	// Default CN split rules must follow the plan rules.
	if !strings.Contains(strings.Join(rules, "\n"), "MATCH,"+clashGroupSelect) {
		t.Fatalf("rules = %v", rules)
	}
	for _, rule := range rules {
		if strings.Contains(rule, "off.example") {
			t.Fatalf("disabled rule leaked: %v", rules)
		}
	}
}

func TestRenderSingboxWithPlanRules(t *testing.T) {
	b, err := RenderSingboxWithRules(sampleEndpoints(), samplePlanRules())
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	route := cfg["route"].(map[string]any)
	rulesRaw := route["rules"].([]any)
	rules := make([]map[string]any, 0, len(rulesRaw))
	for _, item := range rulesRaw {
		rules = append(rules, item.(map[string]any))
	}
	// DNS hijack stays first; then the five enabled plan rules; then defaults.
	if len(rules) != 1+5+3 {
		t.Fatalf("rules = %v", rules)
	}
	if rules[0]["protocol"] != "dns" {
		t.Fatalf("rules[0] = %v", rules[0])
	}
	proxy := rules[1]
	if proxy["action"] != "route" || proxy["outbound"] != "proxy" {
		t.Fatalf("proxy rule = %v", proxy)
	}
	if domains := proxy["domain"].([]any); len(domains) != 1 || domains[0] != "www.corp.example" {
		t.Fatalf("proxy rule = %v", proxy)
	}
	direct := rules[2]
	if direct["action"] != "route" || direct["outbound"] != "direct" {
		t.Fatalf("direct rule = %v", direct)
	}
	block := rules[3]
	if block["action"] != "reject" {
		t.Fatalf("block rule = %v", block)
	}
	if _, hasOutbound := block["outbound"]; hasOutbound {
		t.Fatalf("reject rule must not carry outbound: %v", block)
	}
	cidr := rules[4]
	if values := cidr["ip_cidr"].([]any); len(values) != 1 || values[0] != "203.0.113.0/24" {
		t.Fatalf("cidr rule = %v", cidr)
	}
	proc := rules[5]
	if values := proc["process_name"].([]any); len(values) != 1 || values[0] != "telegram" {
		t.Fatalf("process rule = %v", proc)
	}
	if proc["outbound"] != "proxy" {
		t.Fatalf("process rule = %v", proc)
	}
	// Tail: private/CN direct rules preserved after plan rules.
	if rules[6]["ip_is_private"] != true {
		t.Fatalf("rules[6] = %v", rules[6])
	}
	for _, rule := range rules {
		if domains, ok := rule["domain"].([]any); ok {
			for _, d := range domains {
				if d == "off.example" {
					t.Fatalf("disabled rule leaked: %v", rules)
				}
			}
		}
	}
}

func TestRenderWithoutPlanRulesUnchanged(t *testing.T) {
	withNil, err := RenderSingboxWithRules(sampleEndpoints(), nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := RenderSingbox(sampleEndpoints())
	if err != nil {
		t.Fatal(err)
	}
	if string(withNil) != string(legacy) {
		t.Fatal("nil plan rules must render identically to RenderSingbox")
	}
	clashWithNil, err := RenderClashWithRules(sampleEndpoints(), nil)
	if err != nil {
		t.Fatal(err)
	}
	clashLegacy, err := RenderClash(sampleEndpoints())
	if err != nil {
		t.Fatal(err)
	}
	if string(clashWithNil) != string(clashLegacy) {
		t.Fatal("nil plan rules must render identically to RenderClash")
	}
}
