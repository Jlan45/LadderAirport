// Package subscription builds client-facing Clash / sing-box configs from
// node + inbound inventory, with basic CN split routing.
package subscription

import (
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ladderairport/panel/internal/store"
	"gopkg.in/yaml.v3"
)

// ProxyEndpoint is one client outbound derived from a node-bound inbound
// or normalized from an external subscription source.
type ProxyEndpoint struct {
	Name       string
	Node       store.Node          // zero for external sources
	Inbound    store.InboundConfig // zero for external sources
	Server     string
	Port       int
	Protocol   string
	Params     map[string]any
	SourceID   string // "" = local inventory; external source id when merged
	SourceName string // display group name; empty = local ("本地")
}

// clientServerHost returns the host clients should dial.
// Prefer PublicAddress when set; otherwise fall back to control Address.
func clientServerHost(n store.Node) string {
	return clientServerHostForInbound(n, "")
}

// clientServerHostForInbound prefers per-inbound public host, then node public_address, then address.
func clientServerHostForInbound(n store.Node, inboundPublic string) string {
	if s := strings.TrimSpace(inboundPublic); s != "" {
		return s
	}
	if s := strings.TrimSpace(n.PublicAddress); s != "" {
		return s
	}
	return strings.TrimSpace(n.Address)
}

// CollectEndpoints walks nodes and their attached inbounds.
// inboundFilter empty = all enabled inbounds; otherwise only listed IDs.
//
// Compatibility wrapper: treats inbounds as attachments without per-inbound NAT overrides.
func CollectEndpoints(nodes []store.Node, nodeInbounds map[string][]store.InboundConfig, inboundFilter []string) ([]ProxyEndpoint, error) {
	atts := map[string][]store.NodeInboundAttachment{}
	for nodeID, ins := range nodeInbounds {
		list := make([]store.NodeInboundAttachment, 0, len(ins))
		for _, in := range ins {
			list = append(list, store.NodeInboundAttachment{InboundConfig: in})
		}
		atts[nodeID] = list
	}
	return CollectEndpointsFromAttachments(nodes, atts, inboundFilter)
}

// CollectEndpointsFromAttachments builds subscription endpoints using per-inbound NAT overrides.
// Public host priority: attachment.public_address → node.public_address → node.address
// Public port priority: attachment.public_port → node.port_mappings[listen] → listen port
func CollectEndpointsFromAttachments(nodes []store.Node, nodeAttachments map[string][]store.NodeInboundAttachment, inboundFilter []string) ([]ProxyEndpoint, error) {
	filter := map[string]bool{}
	for _, id := range inboundFilter {
		if id != "" {
			filter[id] = true
		}
	}
	useFilter := len(filter) > 0

	var out []ProxyEndpoint
	for _, n := range nodes {
		atts := nodeAttachments[n.ID]
		for _, att := range atts {
			in := att.InboundConfig
			if !in.Enabled {
				continue
			}
			if useFilter && !filter[in.ID] {
				continue
			}
			listenPort, err := paramInt(in.Params, "port")
			if err != nil || listenPort < 1 {
				continue
			}
			port := listenPort
			if att.PublicPort >= 1 && att.PublicPort <= 65535 {
				port = att.PublicPort
			} else {
				port = store.MapPublicPort(n.PortMappings, listenPort)
			}
			server := clientServerHostForInbound(n, att.PublicAddress)
			if server == "" || server == "0.0.0.0" || server == "::" {
				continue
			}
			name := sanitizeName(fmt.Sprintf("%s-%s", n.Name, in.Name))
			out = append(out, ProxyEndpoint{
				Name:     name,
				Node:     n,
				Inbound:  in,
				Server:   server,
				Port:     port,
				Protocol: in.Protocol,
				Params:   in.Params,
			})
		}
	}
	if len(out) == 0 {
		return []ProxyEndpoint{}, nil
	}
	return out, nil
}

// Clash group / rule names (match common Mihomo CN templates).
const (
	clashGroupSelect = "🚀 节点选择"
	clashGroupDirect = "🎯 全球直连"
	clashGroupLocal  = "本地"
	clashHealthURL   = "https://www.gstatic.com/generate_204"
)

// RenderClash produces Clash / Mihomo YAML.
// Template follows a standard CN split config, but expands external sources as
// inlined proxies + per-source url-test groups on the panel (no proxy-providers).
func RenderClash(endpoints []ProxyEndpoint) ([]byte, error) {
	proxies := make([]map[string]any, 0, len(endpoints))
	for _, ep := range endpoints {
		p, err := clashProxy(ep)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ep.Name, err)
		}
		// Prefer UDP on/off already set per protocol; force udp true when absent
		// so merged external nodes behave like provider override: udp: true.
		if _, ok := p["udp"]; !ok {
			switch ep.Protocol {
			case "shadowsocks", "trojan", "vless", "vmess", "anytls":
				p["udp"] = true
			}
		}
		proxies = append(proxies, p)
	}

	doc := map[string]any{
		"port":                       7890,
		"socks-port":                 7891,
		"allow-lan":                  true,
		"mode":                       "rule",
		"log-level":                  "info",
		"external-controller":        "127.0.0.1:9090",
		"unified-delay":              true,
		"tcp-concurrent":             true,
		"find-process-mode":          "strict",
		"global-client-fingerprint":  "chrome",
		"ipv6":                       false,
		"dns":                        clashDNS(),
		"proxies":                    proxies,
		"proxy-groups":               clashProxyGroups(endpoints),
		"rules": []string{
			"RULE-SET,privateip," + clashGroupDirect + ",no-resolve",
			"RULE-SET,cn," + clashGroupDirect,
			"RULE-SET,cnip," + clashGroupDirect + ",no-resolve",
			"MATCH," + clashGroupSelect,
		},
		"rule-providers": clashRuleProviders(),
	}
	return yaml.Marshal(doc)
}

func clashDNS() map[string]any {
	// Overseas DNS queries detour via the top select group (Clash #group syntax).
	detour := "#" + clashGroupSelect
	return map[string]any{
		"enable":              true,
		"listen":              "0.0.0.0:1053",
		"ipv6":                false,
		"enhanced-mode":       "fake-ip",
		"fake-ip-range":       "198.18.0.1/16",
		"fake-ip-filter-mode": "blacklist",
		"cache-algorithm":     "arc",
		"prefer-h3":           false,
		"use-hosts":           true,
		"use-system-hosts":    true,
		"respect-rules":       true,
		"default-nameserver": []string{
			"223.5.5.5",
			"119.29.29.29",
			"114.114.114.114",
		},
		"proxy-server-nameserver": []string{
			"223.5.5.5",
			"119.29.29.29",
			"114.114.114.114",
			"https://dns.alidns.com/dns-query",
			"https://doh.pub/dns-query",
		},
		"direct-nameserver": []string{
			"https://dns.alidns.com/dns-query",
			"https://doh.pub/dns-query",
		},
		"nameserver-policy": map[string]any{
			"rule-set:cn": []string{
				"https://dns.alidns.com/dns-query",
				"https://doh.pub/dns-query",
			},
			"+.lan":   []string{"223.5.5.5"},
			"+.local": []string{"223.5.5.5"},
			"+.arpa":  []string{"223.5.5.5"},
		},
		"nameserver": []string{
			"https://1.1.1.1/dns-query" + detour,
			"https://1.0.0.1/dns-query" + detour,
			"https://8.8.8.8/dns-query" + detour,
			"https://8.8.4.4/dns-query" + detour,
			"tls://1.1.1.1" + detour,
			"tls://1.0.0.1" + detour,
			"tls://8.8.8.8" + detour,
			"tls://8.8.4.4" + detour,
		},
		"fake-ip-filter": []string{
			"*.lan",
			"*.local",
			"*.arpa",
			"localhost.ptlogin2.qq.com",
			"+.msftconnecttest.com",
			"+.msftncsi.com",
			"time.*.com",
			"time.*.gov",
			"time.*.edu.cn",
			"+.ntp.org.cn",
			"+.pool.ntp.org",
			"+.stun.*.*",
			"+.stun.*.*.*",
			"+.stun.*.*.*.*",
			"+.srv.nintendo.net",
			"+.xboxlive.com",
			"+.battlenet.com.cn",
			"+.wargaming.net",
		},
	}
}

func clashRuleProviders() map[string]any {
	return map[string]any{
		"cn": map[string]any{
			"type":     "http",
			"behavior": "domain",
			"format":   "mrs",
			"path":     "./ruleset/cn.mrs",
			"url":      "https://testingcf.jsdelivr.net/gh/DustinWin/ruleset_geodata@refs/heads/mihomo-ruleset/cn.mrs",
			"interval": 86400,
		},
		"privateip": map[string]any{
			"type":     "http",
			"behavior": "ipcidr",
			"format":   "mrs",
			"path":     "./ruleset/privateip.mrs",
			"url":      "https://testingcf.jsdelivr.net/gh/DustinWin/ruleset_geodata@refs/heads/mihomo-ruleset/privateip.mrs",
			"interval": 86400,
		},
		"cnip": map[string]any{
			"type":     "http",
			"behavior": "ipcidr",
			"format":   "mrs",
			"path":     "./ruleset/cnip.mrs",
			"url":      "https://testingcf.jsdelivr.net/gh/DustinWin/ruleset_geodata@refs/heads/mihomo-ruleset/cnip.mrs",
			"interval": 86400,
		},
	}
}

// clashProxyGroups builds:
//   🚀 节点选择 (select of per-source groups)
//   🎯 全球直连 (DIRECT / 节点选择)
//   one url-test group per source (本地 + each external source name)
// No proxy-providers — all members are inlined proxy names.
func clashProxyGroups(endpoints []ProxyEndpoint) []map[string]any {
	order := make([]string, 0)
	members := map[string][]string{}
	for _, ep := range endpoints {
		g := strings.TrimSpace(ep.SourceName)
		if g == "" {
			g = clashGroupLocal
		}
		if _, ok := members[g]; !ok {
			order = append(order, g)
			members[g] = []string{}
		}
		members[g] = append(members[g], ep.Name)
	}

	groups := make([]map[string]any, 0, len(order)+2)

	// Top select: list source groups (stable first-seen order).
	selectProxies := append([]string{}, order...)
	groups = append(groups, map[string]any{
		"name":    clashGroupSelect,
		"type":    "select",
		"proxies": selectProxies,
	})
	groups = append(groups, map[string]any{
		"name": clashGroupDirect,
		"type": "select",
		"proxies": []string{
			"DIRECT",
			clashGroupSelect,
		},
	})

	for _, g := range order {
		groups = append(groups, map[string]any{
			"name":      g,
			"type":      "url-test",
			"proxies":   members[g],
			"url":       clashHealthURL,
			"interval":  300,
			"tolerance": 100,
			"lazy":      true,
		})
	}
	return groups
}


// RenderSingbox produces sing-box client JSON with remote CN rule sets.
func RenderSingbox(endpoints []ProxyEndpoint) ([]byte, error) {
	tags := make([]string, 0, len(endpoints))
	outbounds := make([]map[string]any, 0, len(endpoints)+4)
	for _, ep := range endpoints {
		ob, err := singboxOutbound(ep)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ep.Name, err)
		}
		outbounds = append(outbounds, ob)
		tags = append(tags, ep.Name)
	}
	selectorOutbounds := append([]string{}, tags...)
	selectorOutbounds = append(selectorOutbounds, "direct")
	outbounds = append([]map[string]any{
		{
			"type":      "selector",
			"tag":       "proxy",
			"outbounds": selectorOutbounds,
		},
	}, outbounds...)
	outbounds = append(outbounds,
		map[string]any{"type": "direct", "tag": "direct"},
		map[string]any{"type": "block", "tag": "block"},
		map[string]any{"type": "dns", "tag": "dns-out"},
	)

	cfg := map[string]any{
		"log": map[string]any{"level": "info"},
		"dns": map[string]any{
			"servers": []map[string]any{
				{"tag": "local", "address": "local", "detour": "direct"},
				{"tag": "remote", "address": "8.8.8.8", "detour": "proxy"},
			},
			"rules": []map[string]any{
				{"outbound": "any", "server": "local"},
			},
			"final": "remote",
		},
		"inbounds": []map[string]any{
			{
				"type":        "mixed",
				"tag":         "mixed-in",
				"listen":      "127.0.0.1",
				"listen_port": 2080,
			},
		},
		"outbounds": outbounds,
		"route": map[string]any{
			"auto_detect_interface": true,
			"final":                 "proxy",
			"rules": []map[string]any{
				{"protocol": "dns", "outbound": "dns-out"},
				{"ip_is_private": true, "outbound": "direct"},
				{"rule_set": "geoip-cn", "outbound": "direct"},
				{"rule_set": "geosite-cn", "outbound": "direct"},
			},
			"rule_set": []map[string]any{
				{
					"tag":             "geoip-cn",
					"type":            "remote",
					"format":          "binary",
					"url":             "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-cn.srs",
					"download_detour": "direct",
				},
				{
					"tag":             "geosite-cn",
					"type":            "remote",
					"format":          "binary",
					"url":             "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-cn.srs",
					"download_detour": "direct",
				},
			},
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

func clashProxy(ep ProxyEndpoint) (map[string]any, error) {
	p := map[string]any{
		"name":   ep.Name,
		"server": ep.Server,
		"port":   ep.Port,
	}
	switch ep.Protocol {
	case "shadowsocks":
		method, _ := paramString(ep.Params, "method")
		password, _ := paramString(ep.Params, "password")
		if method == "" || password == "" {
			return nil, fmt.Errorf("missing method/password")
		}
		p["type"] = "ss"
		p["cipher"] = method
		p["password"] = password
		if n, _ := paramString(ep.Params, "network"); n != "" {
			p["udp"] = n != "tcp"
		} else {
			p["udp"] = true
		}
	case "trojan":
		password, _ := paramString(ep.Params, "password")
		if password == "" {
			return nil, fmt.Errorf("missing password")
		}
		p["type"] = "trojan"
		p["password"] = password
		p["udp"] = true
		p["skip-cert-verify"] = true
		if sn, _ := paramString(ep.Params, "server_name"); sn != "" {
			p["sni"] = sn
		}
	case "vless":
		uid, _ := paramString(ep.Params, "uuid")
		if uid == "" {
			return nil, fmt.Errorf("missing uuid")
		}
		p["type"] = "vless"
		p["uuid"] = uid
		p["udp"] = true
		p["network"] = "tcp"
		if flow, _ := paramString(ep.Params, "flow"); flow != "" {
			p["flow"] = flow
		}
		mode, _ := paramString(ep.Params, "tls_mode")
		if mode == "" {
			mode = "none"
		}
		switch mode {
		case "none":
			// plain
		case "tls":
			p["tls"] = true
			p["skip-cert-verify"] = true
			if sn, _ := paramString(ep.Params, "server_name"); sn != "" {
				p["servername"] = sn
			}
		case "reality":
			p["tls"] = true
			p["client-fingerprint"] = "chrome"
			sn, _ := paramString(ep.Params, "server_name")
			if sn == "" {
				sn = "www.microsoft.com"
			}
			p["servername"] = sn
			pub, err := resolveRealityPublicKey(ep.Params)
			if err != nil {
				return nil, err
			}
			sid, _ := paramString(ep.Params, "short_id")
			p["reality-opts"] = map[string]any{
				"public-key": pub,
				"short-id":   sid,
			}
		default:
			return nil, fmt.Errorf("unsupported tls_mode %q", mode)
		}
	case "hysteria2":
		password, _ := paramString(ep.Params, "password")
		if password == "" {
			return nil, fmt.Errorf("missing password")
		}
		p["type"] = "hysteria2"
		p["password"] = password
		p["skip-cert-verify"] = true
		if sn, _ := paramString(ep.Params, "server_name"); sn != "" {
			p["sni"] = sn
		}
		if up, err := paramInt(ep.Params, "up_mbps"); err == nil && up > 0 {
			p["up"] = fmt.Sprintf("%d Mbps", up)
		}
		if down, err := paramInt(ep.Params, "down_mbps"); err == nil && down > 0 {
			p["down"] = fmt.Sprintf("%d Mbps", down)
		}
	case "tuic":
		uid, _ := paramString(ep.Params, "uuid")
		password, _ := paramString(ep.Params, "password")
		if uid == "" || password == "" {
			return nil, fmt.Errorf("missing uuid/password")
		}
		p["type"] = "tuic"
		p["uuid"] = uid
		p["password"] = password
		p["skip-cert-verify"] = true
		p["udp-relay-mode"] = "native"
		if cc, _ := paramString(ep.Params, "congestion_control"); cc != "" {
			p["congestion-controller"] = cc
		} else {
			p["congestion-controller"] = "cubic"
		}
		if sn, _ := paramString(ep.Params, "server_name"); sn != "" {
			p["sni"] = sn
		}
		p["alpn"] = []string{"h3"}
	case "anytls":
		password, _ := paramString(ep.Params, "password")
		if password == "" {
			return nil, fmt.Errorf("missing password")
		}
		p["type"] = "anytls"
		p["password"] = password
		p["udp"] = true
		p["skip-cert-verify"] = true
		p["client-fingerprint"] = "chrome"
		if sn, _ := paramString(ep.Params, "server_name"); sn != "" {
			p["sni"] = sn
		}
	case "vmess":
		uid, _ := paramString(ep.Params, "uuid")
		if uid == "" {
			return nil, fmt.Errorf("missing uuid")
		}
		p["type"] = "vmess"
		p["uuid"] = uid
		p["cipher"] = "auto"
		p["udp"] = true
		p["network"] = "tcp"
		alterID := 0
		if n, err := paramInt(ep.Params, "alter_id"); err == nil && n >= 0 {
			alterID = n
		}
		p["alterId"] = alterID
		mode, _ := paramString(ep.Params, "tls_mode")
		if mode == "" {
			mode = "none"
		}
		switch mode {
		case "none":
		case "tls":
			p["tls"] = true
			p["skip-cert-verify"] = true
			if sn, _ := paramString(ep.Params, "server_name"); sn != "" {
				p["servername"] = sn
			}
		default:
			return nil, fmt.Errorf("unsupported tls_mode %q", mode)
		}
	default:
		return nil, fmt.Errorf("unsupported protocol %q", ep.Protocol)
	}
	return p, nil
}

func singboxOutbound(ep ProxyEndpoint) (map[string]any, error) {
	o := map[string]any{
		"tag":         ep.Name,
		"server":      ep.Server,
		"server_port": ep.Port,
	}
	switch ep.Protocol {
	case "shadowsocks":
		method, _ := paramString(ep.Params, "method")
		password, _ := paramString(ep.Params, "password")
		if method == "" || password == "" {
			return nil, fmt.Errorf("missing method/password")
		}
		o["type"] = "shadowsocks"
		o["method"] = method
		o["password"] = password
	case "trojan":
		password, _ := paramString(ep.Params, "password")
		if password == "" {
			return nil, fmt.Errorf("missing password")
		}
		o["type"] = "trojan"
		o["password"] = password
		o["tls"] = map[string]any{
			"enabled":     true,
			"insecure":    true,
			"server_name": firstNonEmpty(paramStringMust(ep.Params, "server_name"), ep.Server),
		}
	case "vless":
		uid, _ := paramString(ep.Params, "uuid")
		if uid == "" {
			return nil, fmt.Errorf("missing uuid")
		}
		o["type"] = "vless"
		o["uuid"] = uid
		if flow, _ := paramString(ep.Params, "flow"); flow != "" {
			o["flow"] = flow
		}
		mode, _ := paramString(ep.Params, "tls_mode")
		if mode == "" {
			mode = "none"
		}
		switch mode {
		case "none":
		case "tls":
			o["tls"] = map[string]any{
				"enabled":     true,
				"insecure":    true,
				"server_name": firstNonEmpty(paramStringMust(ep.Params, "server_name"), ep.Server),
			}
		case "reality":
			pub, err := resolveRealityPublicKey(ep.Params)
			if err != nil {
				return nil, err
			}
			sid, _ := paramString(ep.Params, "short_id")
			sn := firstNonEmpty(paramStringMust(ep.Params, "server_name"), "www.microsoft.com")
			o["tls"] = map[string]any{
				"enabled":     true,
				"server_name": sn,
				"utls": map[string]any{
					"enabled":     true,
					"fingerprint": "chrome",
				},
				"reality": map[string]any{
					"enabled":    true,
					"public_key": pub,
					"short_id":   sid,
				},
			}
		default:
			return nil, fmt.Errorf("unsupported tls_mode %q", mode)
		}
	case "hysteria2":
		password, _ := paramString(ep.Params, "password")
		if password == "" {
			return nil, fmt.Errorf("missing password")
		}
		o["type"] = "hysteria2"
		o["password"] = password
		o["tls"] = map[string]any{
			"enabled":     true,
			"insecure":    true,
			"server_name": firstNonEmpty(paramStringMust(ep.Params, "server_name"), ep.Server),
		}
		if up, err := paramInt(ep.Params, "up_mbps"); err == nil && up > 0 {
			o["up_mbps"] = up
		}
		if down, err := paramInt(ep.Params, "down_mbps"); err == nil && down > 0 {
			o["down_mbps"] = down
		}
	case "tuic":
		uid, _ := paramString(ep.Params, "uuid")
		password, _ := paramString(ep.Params, "password")
		if uid == "" || password == "" {
			return nil, fmt.Errorf("missing uuid/password")
		}
		o["type"] = "tuic"
		o["uuid"] = uid
		o["password"] = password
		o["udp_relay_mode"] = "native"
		if cc, _ := paramString(ep.Params, "congestion_control"); cc != "" {
			o["congestion_control"] = cc
		} else {
			o["congestion_control"] = "cubic"
		}
		o["tls"] = map[string]any{
			"enabled":     true,
			"insecure":    true,
			"server_name": firstNonEmpty(paramStringMust(ep.Params, "server_name"), ep.Server),
			"alpn":        []string{"h3"},
		}
	case "anytls":
		password, _ := paramString(ep.Params, "password")
		if password == "" {
			return nil, fmt.Errorf("missing password")
		}
		o["type"] = "anytls"
		o["password"] = password
		o["tls"] = map[string]any{
			"enabled":     true,
			"insecure":    true,
			"server_name": firstNonEmpty(paramStringMust(ep.Params, "server_name"), ep.Server),
			"utls": map[string]any{
				"enabled":     true,
				"fingerprint": "chrome",
			},
		}
	case "vmess":
		uid, _ := paramString(ep.Params, "uuid")
		if uid == "" {
			return nil, fmt.Errorf("missing uuid")
		}
		o["type"] = "vmess"
		o["uuid"] = uid
		o["security"] = "auto"
		alterID := 0
		if n, err := paramInt(ep.Params, "alter_id"); err == nil && n >= 0 {
			alterID = n
		}
		o["alter_id"] = alterID
		mode, _ := paramString(ep.Params, "tls_mode")
		if mode == "" {
			mode = "none"
		}
		switch mode {
		case "none":
		case "tls":
			o["tls"] = map[string]any{
				"enabled":     true,
				"insecure":    true,
				"server_name": firstNonEmpty(paramStringMust(ep.Params, "server_name"), ep.Server),
			}
		default:
			return nil, fmt.Errorf("unsupported tls_mode %q", mode)
		}
	default:
		return nil, fmt.Errorf("unsupported protocol %q", ep.Protocol)
	}
	return o, nil
}

func realityPublicKey(privateKeyB64 string) (string, error) {
	if privateKeyB64 == "" {
		return "", fmt.Errorf("missing reality private_key")
	}
	raw, err := base64.RawURLEncoding.DecodeString(privateKeyB64)
	if err != nil {
		// try StdEncoding
		raw, err = base64.StdEncoding.DecodeString(privateKeyB64)
		if err != nil {
			return "", fmt.Errorf("decode reality private key: %w", err)
		}
	}
	if len(raw) != 32 {
		return "", fmt.Errorf("reality private key must be 32 bytes")
	}
	priv, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", fmt.Errorf("reality private key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()), nil
}


// resolveRealityPublicKey prefers an already-known public_key (external sources)
// and falls back to deriving from private_key (local inventory).
func resolveRealityPublicKey(params map[string]any) (string, error) {
	if pub, ok := paramString(params, "public_key"); ok {
		return pub, nil
	}
	priv, _ := paramString(params, "private_key")
	return realityPublicKey(priv)
}

func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	repl := strings.NewReplacer(" ", "-", "/", "-", "\\", "-", ",", "-", ":", "-")
	s = repl.Replace(s)
	if s == "" {
		return "proxy"
	}
	return s
}

func paramString(m map[string]any, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, ok := m[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		s = fmt.Sprint(v)
	}
	s = strings.TrimSpace(s)
	return s, s != ""
}

func paramStringMust(m map[string]any, key string) string {
	s, _ := paramString(m, key)
	return s
}

func paramInt(m map[string]any, key string) (int, error) {
	if m == nil {
		return 0, fmt.Errorf("missing")
	}
	v, ok := m[key]
	if !ok || v == nil {
		return 0, fmt.Errorf("missing")
	}
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		return int(n), nil
	case json.Number:
		i, err := n.Int64()
		return int(i), err
	case string:
		var i int
		_, err := fmt.Sscanf(strings.TrimSpace(n), "%d", &i)
		return i, err
	default:
		return 0, fmt.Errorf("bad type")
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
