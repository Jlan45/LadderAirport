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

func TestCollectEndpointsPrefersPublicAddress(t *testing.T) {
	nodes := []store.Node{{
		ID: "n1", Name: "hk",
		Address: "10.0.0.1", PublicAddress: "edge.example.com",
	}}
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
	if eps[0].Server != "edge.example.com" {
		t.Fatalf("server = %q, want public_address", eps[0].Server)
	}
}

func TestCollectEndpointsAppliesPortMappings(t *testing.T) {
	nodes := []store.Node{{
		ID: "n1", Name: "nat",
		Address: "10.0.0.8", PublicAddress: "203.0.113.9",
		PortMappings: []store.PortMapping{
			{ListenPort: 8443, PublicPort: 443},
			{ListenPort: 9000, PublicPort: 19000},
		},
	}}
	ins := map[string][]store.InboundConfig{
		"n1": {
			{
				ID: "i1", Name: "hy2", Protocol: "hysteria2", Enabled: true,
				Params: map[string]any{"port": float64(8443), "password": "p"},
			},
			{
				ID: "i2", Name: "ss", Protocol: "shadowsocks", Enabled: true,
				Params: map[string]any{"port": float64(9000), "method": "aes-128-gcm", "password": "p"},
			},
			{
				ID: "i3", Name: "ss2", Protocol: "shadowsocks", Enabled: true,
				// No mapping → keep listen port.
				Params: map[string]any{"port": float64(10086), "method": "aes-128-gcm", "password": "p"},
			},
		},
	}
	eps, err := CollectEndpoints(nodes, ins, nil)
	if err != nil || len(eps) != 3 {
		t.Fatalf("eps=%v err=%v", eps, err)
	}
	want := map[string]int{"hy2": 443, "ss": 19000, "ss2": 10086}
	for _, ep := range eps {
		// Name is sanitized "nat-<inbound>"
		key := ep.Inbound.Name
		if ep.Port != want[key] {
			t.Fatalf("%s port = %d, want %d (server=%s)", key, ep.Port, want[key], ep.Server)
		}
		if ep.Server != "203.0.113.9" {
			t.Fatalf("%s server = %q", key, ep.Server)
		}
	}
}

func TestClientServerHost(t *testing.T) {
	if got := clientServerHost(store.Node{Address: "10.0.0.1"}); got != "10.0.0.1" {
		t.Fatalf("fallback = %q", got)
	}
	if got := clientServerHost(store.Node{Address: "10.0.0.1", PublicAddress: " pub.example "}); got != "pub.example" {
		t.Fatalf("prefer public = %q", got)
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


func TestCollectEndpointsPerInboundNAT(t *testing.T) {
	nodes := []store.Node{{
		ID: "n1", Name: "nat",
		Address: "10.0.0.8", PublicAddress: "node-default.example.com",
		PortMappings: []store.PortMapping{{ListenPort: 9000, PublicPort: 19000}},
	}}
	atts := map[string][]store.NodeInboundAttachment{
		"n1": {
			{
				InboundConfig: store.InboundConfig{
					ID: "i1", Name: "hy2", Protocol: "hysteria2", Enabled: true,
					Params: map[string]any{"port": float64(8443), "password": "p"},
				},
				PublicAddress: "edge.example.com",
				PublicPort:    443,
			},
			{
				InboundConfig: store.InboundConfig{
					ID: "i2", Name: "ss", Protocol: "shadowsocks", Enabled: true,
					Params: map[string]any{"port": float64(9000), "method": "aes-128-gcm", "password": "p"},
				},
				// no attachment override → node port_mappings + node public_address
			},
			{
				InboundConfig: store.InboundConfig{
					ID: "i3", Name: "ss2", Protocol: "shadowsocks", Enabled: true,
					Params: map[string]any{"port": float64(10086), "method": "aes-128-gcm", "password": "p"},
				},
				PublicPort: 10086, // same as listen, still fine
			},
		},
	}
	eps, err := CollectEndpointsFromAttachments(nodes, atts, nil)
	if err != nil || len(eps) != 3 {
		t.Fatalf("eps=%v err=%v", eps, err)
	}
	wantHost := map[string]string{"hy2": "edge.example.com", "ss": "node-default.example.com", "ss2": "node-default.example.com"}
	wantPort := map[string]int{"hy2": 443, "ss": 19000, "ss2": 10086}
	for _, ep := range eps {
		key := ep.Inbound.Name
		if ep.Server != wantHost[key] {
			t.Fatalf("%s server=%q want %q", key, ep.Server, wantHost[key])
		}
		if ep.Port != wantPort[key] {
			t.Fatalf("%s port=%d want %d", key, ep.Port, wantPort[key])
		}
	}
}
