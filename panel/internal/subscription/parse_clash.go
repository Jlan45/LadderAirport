package subscription

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// parseClashYAML reads top-level proxies[] from a Clash/Mihomo config.
// Unsupported types are skipped; zero proxies is an error.
func parseClashYAML(raw []byte) ([]ProxyEndpoint, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("clash yaml: %w", err)
	}
	rawProxies, ok := doc["proxies"]
	if !ok {
		return nil, fmt.Errorf("clash yaml: missing proxies")
	}
	list, ok := rawProxies.([]any)
	if !ok {
		return nil, fmt.Errorf("clash yaml: proxies is not a list")
	}
	var out []ProxyEndpoint
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			// yaml.v3 may produce map[string]interface{} already; also try string-keyed via remarshal
			continue
		}
		ep, err := clashMapToEndpoint(m)
		if err != nil {
			continue
		}
		out = append(out, ep)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("clash yaml: no supported proxies")
	}
	return out, nil
}

func clashMapToEndpoint(m map[string]any) (ProxyEndpoint, error) {
	name := anyToString(m["name"])
	server := anyToString(m["server"])
	port, ok := anyToInt(m["port"])
	if !ok || port < 1 || port > 65535 {
		return ProxyEndpoint{}, fmt.Errorf("bad port")
	}
	if server == "" {
		return ProxyEndpoint{}, fmt.Errorf("missing server")
	}
	if name == "" {
		name = fmt.Sprintf("%s-%d", server, port)
	}
	typ := strings.ToLower(anyToString(m["type"]))
	params := map[string]any{}
	protocol := ""

	switch typ {
	case "ss", "shadowsocks":
		protocol = "shadowsocks"
		cipher := firstNonEmpty(anyToString(m["cipher"]), anyToString(m["method"]))
		password := anyToString(m["password"])
		if cipher == "" || password == "" {
			return ProxyEndpoint{}, fmt.Errorf("ss missing cipher/password")
		}
		params["method"] = cipher
		params["password"] = password
		if anyToBool(m["udp"]) {
			params["network"] = "udp"
		}
	case "trojan":
		protocol = "trojan"
		password := anyToString(m["password"])
		if password == "" {
			return ProxyEndpoint{}, fmt.Errorf("trojan missing password")
		}
		params["password"] = password
		if sn := firstNonEmpty(anyToString(m["sni"]), anyToString(m["servername"])); sn != "" {
			params["server_name"] = sn
		}
	case "vless":
		protocol = "vless"
		uid := firstNonEmpty(anyToString(m["uuid"]), anyToString(m["id"]))
		if uid == "" {
			return ProxyEndpoint{}, fmt.Errorf("vless missing uuid")
		}
		params["uuid"] = uid
		if flow := anyToString(m["flow"]); flow != "" {
			params["flow"] = flow
		}
		// Reality via reality-opts
		if ro, ok := m["reality-opts"].(map[string]any); ok {
			params["tls_mode"] = "reality"
			if pub := firstNonEmpty(anyToString(ro["public-key"]), anyToString(ro["public_key"])); pub != "" {
				params["public_key"] = pub
			}
			if sid := firstNonEmpty(anyToString(ro["short-id"]), anyToString(ro["short_id"])); sid != "" {
				params["short_id"] = sid
			}
		} else if anyToBool(m["tls"]) {
			params["tls_mode"] = "tls"
		} else {
			params["tls_mode"] = "none"
		}
		if sn := firstNonEmpty(anyToString(m["servername"]), anyToString(m["sni"])); sn != "" {
			params["server_name"] = sn
		}
	case "hysteria2", "hy2":
		protocol = "hysteria2"
		password := firstNonEmpty(anyToString(m["password"]), anyToString(m["auth"]))
		if password == "" {
			return ProxyEndpoint{}, fmt.Errorf("hysteria2 missing password")
		}
		params["password"] = password
		if sn := firstNonEmpty(anyToString(m["sni"]), anyToString(m["servername"])); sn != "" {
			params["server_name"] = sn
		}
		if up, ok := parseMbps(m["up"]); ok {
			params["up_mbps"] = up
		}
		if down, ok := parseMbps(m["down"]); ok {
			params["down_mbps"] = down
		}
	case "tuic":
		protocol = "tuic"
		uid := anyToString(m["uuid"])
		password := anyToString(m["password"])
		if uid == "" || password == "" {
			return ProxyEndpoint{}, fmt.Errorf("tuic missing uuid/password")
		}
		params["uuid"] = uid
		params["password"] = password
		if cc := firstNonEmpty(anyToString(m["congestion-controller"]), anyToString(m["congestion_control"])); cc != "" {
			params["congestion_control"] = cc
		}
		if sn := firstNonEmpty(anyToString(m["sni"]), anyToString(m["servername"])); sn != "" {
			params["server_name"] = sn
		}
	case "anytls":
		protocol = "anytls"
		password := anyToString(m["password"])
		if password == "" {
			return ProxyEndpoint{}, fmt.Errorf("anytls missing password")
		}
		params["password"] = password
		if sn := firstNonEmpty(anyToString(m["sni"]), anyToString(m["servername"])); sn != "" {
			params["server_name"] = sn
		}
	case "vmess":
		protocol = "vmess"
		uid := firstNonEmpty(anyToString(m["uuid"]), anyToString(m["id"]))
		if uid == "" {
			return ProxyEndpoint{}, fmt.Errorf("vmess missing uuid")
		}
		params["uuid"] = uid
		if aid, ok := anyToInt(m["alterId"]); ok {
			params["alter_id"] = aid
		} else if aid, ok := anyToInt(m["alter_id"]); ok {
			params["alter_id"] = aid
		}
		if anyToBool(m["tls"]) {
			params["tls_mode"] = "tls"
		} else {
			params["tls_mode"] = "none"
		}
		if sn := firstNonEmpty(anyToString(m["servername"]), anyToString(m["sni"])); sn != "" {
			params["server_name"] = sn
		}
	default:
		return ProxyEndpoint{}, fmt.Errorf("unsupported type %q", typ)
	}

	return ProxyEndpoint{
		Name:     sanitizeName(name),
		Server:   server,
		Port:     port,
		Protocol: protocol,
		Params:   params,
	}, nil
}

func parseMbps(v any) (int, bool) {
	if n, ok := anyToInt(v); ok && n > 0 {
		return n, true
	}
	s := anyToString(v)
	if s == "" {
		return 0, false
	}
	// "100 Mbps" / "100Mbps" / "100"
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err == nil && n > 0
}
