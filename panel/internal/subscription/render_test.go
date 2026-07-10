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

func TestRenderNewProtocols(t *testing.T) {
	eps := []ProxyEndpoint{
		{
			Name: "n-tuic", Server: "1.1.1.1", Port: 8443, Protocol: "tuic",
			Params: map[string]any{
				"uuid": "2dd61d93-75d8-4da4-ac0e-6aece7eac365", "password": "p",
				"congestion_control": "bbr", "server_name": "t.example.com",
			},
		},
		{
			Name: "n-anytls", Server: "1.1.1.1", Port: 443, Protocol: "anytls",
			Params: map[string]any{"password": "p", "server_name": "a.example.com"},
		},
		{
			Name: "n-vmess", Server: "1.1.1.1", Port: 10086, Protocol: "vmess",
			Params: map[string]any{
				"uuid": "bf000d23-0752-40b4-affe-68f7707a9661",
				"alter_id": float64(0), "tls_mode": "tls", "server_name": "v.example.com",
			},
		},
	}
	clash, err := RenderClash(eps)
	if err != nil {
		t.Fatal(err)
	}
	cs := string(clash)
	for _, want := range []string{"type: tuic", "type: anytls", "type: vmess", "congestion-controller: bbr"} {
		if !strings.Contains(cs, want) {
			t.Fatalf("clash missing %q in:\n%s", want, cs)
		}
	}
	sb, err := RenderSingbox(eps)
	if err != nil {
		t.Fatal(err)
	}
	ss := string(sb)
	for _, want := range []string{`"type": "tuic"`, `"type": "anytls"`, `"type": "vmess"`, `"congestion_control": "bbr"`} {
		if !strings.Contains(ss, want) {
			t.Fatalf("singbox missing %q in:\n%s", want, ss)
		}
	}
}
