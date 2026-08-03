package nodeconfig

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/ladderairport/panel/internal/store"
)

func openRouteRuleStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func addNodeWithInbound(t *testing.T, st *store.Store, name, address string, port int) (store.Node, store.InboundConfig) {
	t.Helper()
	node, inbound := addNodeAndInbound(t, st, name, address, port)
	if err := st.SetNodeInbounds(node.ID, []string{inbound.ID}); err != nil {
		t.Fatal(err)
	}
	return node, inbound
}

// addNodeAndInbound creates the node and inbound without a standalone
// attachment (chain hops may not reuse standalone-bound inbounds).
func addNodeAndInbound(t *testing.T, st *store.Store, name, address string, port int) (store.Node, store.InboundConfig) {
	t.Helper()
	node := &store.Node{Name: name, Address: address, GRPCPort: 50051, Status: "online"}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	inbound := &store.InboundConfig{
		Name: "ss-" + name, Protocol: "shadowsocks", Enabled: true,
		Params: map[string]any{"port": port, "method": "aes-128-gcm", "password": "pw-" + name},
	}
	if err := st.CreateInbound(inbound); err != nil {
		t.Fatal(err)
	}
	return *node, *inbound
}

func TestGlobalRouteRulesResolution(t *testing.T) {
	st := openRouteRuleStore(t)
	nodeA, inA := addNodeAndInbound(t, st, "a", "192.0.2.1", 11001)
	nodeB, inB := addNodeAndInbound(t, st, "b", "192.0.2.2", 11002)
	nodeC, _ := addNodeAndInbound(t, st, "c", "192.0.2.3", 11003)

	chain := &store.ProxyChain{
		Name: "res-chain",
		Hops: []store.ProxyChainHop{
			{NodeID: nodeA.ID, InboundID: inA.ID},
			{NodeID: nodeB.ID, InboundID: inB.ID},
		},
	}
	if err := st.CreateProxyChain(chain); err != nil {
		t.Fatal(err)
	}
	// The effective chain set passed to the builder treats the chain as enabled.
	chain.Enabled = true
	plan := &store.RoutePlan{
		Name: "g", Scope: "global", Enabled: true,
		Rules: []store.RoutePlanRule{
			{MatchType: "domain_suffix", MatchValue: "corp.example", Action: "proxy", TargetChainID: chain.ID, Enabled: true},
			{MatchType: "domain", MatchValue: "plain.example", Action: "direct", Enabled: true},
			{MatchType: "ip_cidr", MatchValue: "198.51.100.0/24", Action: "block", Enabled: true},
			{MatchType: "process_name", MatchValue: "ssh", Action: "direct", Enabled: true},
			{MatchType: "domain", MatchValue: "off.example", Action: "direct", Enabled: false},
		},
	}
	if err := st.CreateRoutePlan(plan); err != nil {
		t.Fatal(err)
	}
	// A disabled plan must not contribute rules.
	disabled := &store.RoutePlan{
		Name: "off", Scope: "global", Enabled: false,
		Rules: []store.RoutePlanRule{
			{MatchType: "domain", MatchValue: "nope.example", Action: "direct", Enabled: true},
		},
	}
	if err := st.CreateRoutePlan(disabled); err != nil {
		t.Fatal(err)
	}

	builder := &Builder{Store: st}
	chains := []store.ProxyChain{*chain}

	// First hop: proxy rule targets the chain next-hop outbound.
	rules, err := builder.globalRouteRules(nodeA.ID, chains)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 3 {
		t.Fatalf("nodeA rules = %+v", rules)
	}
	if rules[0].Outbound != ChainOutboundTag(chain.ID, 0) || rules[0].Reject {
		t.Fatalf("nodeA rule[0] = %+v", rules[0])
	}
	if rules[1].Outbound != "direct" || rules[2].Outbound != "" || !rules[2].Reject {
		t.Fatalf("nodeA rules = %+v", rules)
	}

	// Last hop (chain exit): proxy rule degrades to direct.
	rules, err = builder.globalRouteRules(nodeB.ID, chains)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 3 || rules[0].Outbound != "direct" {
		t.Fatalf("nodeB rules = %+v", rules)
	}

	// Node not on the chain: the proxy rule is skipped, the rest stay.
	rules, err = builder.globalRouteRules(nodeC.ID, chains)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Fatalf("nodeC rules = %+v", rules)
	}
	for _, rule := range rules {
		if rule.MatchValue == "corp.example" {
			t.Fatalf("nodeC should skip chain rule: %+v", rules)
		}
	}
}

func TestBuildIncludesGlobalRouteRules(t *testing.T) {
	st := openRouteRuleStore(t)
	node, _ := addNodeWithInbound(t, st, "solo", "192.0.2.10", 12001)
	plan := &store.RoutePlan{
		Name: "g2", Scope: "global", Enabled: true,
		Rules: []store.RoutePlanRule{
			{MatchType: "domain_keyword", MatchValue: "ads", Action: "block", Enabled: true},
			{MatchType: "domain_suffix", MatchValue: "lan.example", Action: "direct", Enabled: true},
		},
	}
	if err := st.CreateRoutePlan(plan); err != nil {
		t.Fatal(err)
	}
	result, err := (&Builder{Store: st}).Build(node.ID)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(result.JSON), &cfg); err != nil {
		t.Fatal(err)
	}
	route := cfg["route"].(map[string]any)
	rules, ok := route["rules"].([]any)
	if !ok || len(rules) != 2 {
		t.Fatalf("rules = %v", route["rules"])
	}
	first := rules[0].(map[string]any)
	if first["action"] != "reject" {
		t.Fatalf("first rule = %v", first)
	}
	kw := first["domain_keyword"].([]any)
	if len(kw) != 1 || kw[0] != "ads" {
		t.Fatalf("first rule = %v", first)
	}
	second := rules[1].(map[string]any)
	if second["action"] != "route" || second["outbound"] != "direct" {
		t.Fatalf("second rule = %v", second)
	}
	if route["final"] != "direct" {
		t.Fatalf("final = %v", route["final"])
	}
}
