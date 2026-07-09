package subscription

import (
	"strings"
	"testing"

	"github.com/ladderairport/panel/internal/store"
)

func sampleEndpoints() []ProxyEndpoint {
	return []ProxyEndpoint{
		{
			Name:     "hk-ss",
			Server:   "1.2.3.4",
			Port:     8388,
			Protocol: "shadowsocks",
			Params: map[string]any{
				"method":   "aes-256-gcm",
				"password": "secret",
			},
			Node:    store.Node{Name: "hk", Address: "1.2.3.4"},
			Inbound: store.InboundConfig{Name: "ss", Protocol: "shadowsocks"},
		},
	}
}

func TestRenderClashHasCNRules(t *testing.T) {
	b, err := RenderClash(sampleEndpoints())
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"type: ss", "GEOIP,CN,DIRECT", "GEOSITE,cn,DIRECT", "MATCH,PROXY", "PROXY"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}

func TestRenderSingboxHasCNRuleSets(t *testing.T) {
	b, err := RenderSingbox(sampleEndpoints())
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"type": "shadowsocks"`, `geoip-cn`, `geosite-cn`, `"final": "proxy"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}

func TestCollectEndpoints(t *testing.T) {
	nodes := []store.Node{{ID: "n1", Name: "hk", Address: "10.0.0.1"}}
	ins := map[string][]store.InboundConfig{
		"n1": {{
			ID: "i1", Name: "ss", Protocol: "shadowsocks", Enabled: true,
			Params: map[string]any{"port": float64(9000), "method": "aes-128-gcm", "password": "p"},
		}},
	}
	eps, err := CollectEndpoints(nodes, ins, nil)
	if err != nil || len(eps) != 1 {
		t.Fatalf("eps=%v err=%v", eps, err)
	}
	if eps[0].Port != 9000 || eps[0].Server != "10.0.0.1" {
		t.Fatalf("%+v", eps[0])
	}
}
