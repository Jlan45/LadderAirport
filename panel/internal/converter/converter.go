package converter

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/ladderairport/panel/internal/store"
)

// Convert builds a full sing-box JSON config from inbound configs.
// Disabled inbounds are skipped. Returns an error if no enabled inbounds remain,
// if listen/port conflicts exist, or if required fields/validation fail.
func Convert(inbounds []store.InboundConfig) ([]byte, error) {
	enabled := make([]store.InboundConfig, 0, len(inbounds))
	for _, in := range inbounds {
		if in.Enabled {
			enabled = append(enabled, in)
		}
	}
	if len(enabled) == 0 {
		return nil, fmt.Errorf("no enabled inbounds")
	}

	seenPorts := map[string]string{} // "listen:port" -> name/id
	outInbounds := make([]map[string]any, 0, len(enabled))
	for _, in := range enabled {
		mapped, err := mapInbound(in)
		if err != nil {
			return nil, fmt.Errorf("inbound %q (%s): %w", in.Name, in.ID, err)
		}
		listen, _ := mapped["listen"].(string)
		port, _ := asInt(mapped["listen_port"])
		key := fmt.Sprintf("%s:%d", listen, port)
		if prev, ok := seenPorts[key]; ok {
			return nil, fmt.Errorf("port conflict on %s (between %s and %s)", key, prev, label(in))
		}
		seenPorts[key] = label(in)
		outInbounds = append(outInbounds, mapped)
	}

	cfg := map[string]any{
		"log": map[string]any{
			"level": "info",
		},
		"inbounds": outInbounds,
		"outbounds": []map[string]any{
			{"type": "direct", "tag": "direct"},
		},
		"route": map[string]any{
			"final": "direct",
		},
	}
	return json.Marshal(cfg)
}

func label(in store.InboundConfig) string {
	if in.Name != "" {
		return in.Name
	}
	return in.ID
}

func mapInbound(in store.InboundConfig) (map[string]any, error) {
	if in.Params == nil {
		return nil, fmt.Errorf("missing params")
	}
	switch in.Protocol {
	case "shadowsocks":
		return mapShadowsocks(in)
	case "trojan":
		return mapTrojan(in)
	case "vless":
		return mapVLESS(in)
	case "hysteria2":
		return mapHysteria2(in)
	default:
		return nil, fmt.Errorf("unsupported protocol %q", in.Protocol)
	}
}

func mapShadowsocks(in store.InboundConfig) (map[string]any, error) {
	listen, port, err := requireListenPort(in.Params)
	if err != nil {
		return nil, err
	}
	method, err := requireString(in.Params, "method")
	if err != nil {
		return nil, err
	}
	password, err := requireString(in.Params, "password")
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"type":        "shadowsocks",
		"tag":         inboundTag(in),
		"listen":      listen,
		"listen_port": port,
		"method":      method,
		"password":    password,
	}
	if network := optionalString(in.Params, "network"); network != "" {
		out["network"] = network
	}
	return out, nil
}

func mapTrojan(in store.InboundConfig) (map[string]any, error) {
	listen, port, err := requireListenPort(in.Params)
	if err != nil {
		return nil, err
	}
	password, err := requireString(in.Params, "password")
	if err != nil {
		return nil, err
	}
	tlsBlock, err := buildTLS(in.Params, true)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"type":        "trojan",
		"tag":         inboundTag(in),
		"listen":      listen,
		"listen_port": port,
		"users": []map[string]any{
			{"name": "default", "password": password},
		},
		"tls": tlsBlock,
	}, nil
}

func mapVLESS(in store.InboundConfig) (map[string]any, error) {
	listen, port, err := requireListenPort(in.Params)
	if err != nil {
		return nil, err
	}
	uid, err := requireString(in.Params, "uuid")
	if err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(uid); err != nil {
		return nil, fmt.Errorf("invalid UUID: %w", err)
	}
	user := map[string]any{
		"name": "default",
		"uuid": uid,
	}
	if flow := optionalString(in.Params, "flow"); flow != "" {
		user["flow"] = flow
	}
	out := map[string]any{
		"type":        "vless",
		"tag":         inboundTag(in),
		"listen":      listen,
		"listen_port": port,
		"users":       []map[string]any{user},
	}

	tlsMode := optionalString(in.Params, "tls_mode")
	if tlsMode == "" {
		tlsMode = "none"
	}
	switch tlsMode {
	case "none":
		// no tls block
	case "tls":
		tls, err := buildTLS(in.Params, true)
		if err != nil {
			return nil, err
		}
		if sn := optionalString(in.Params, "server_name"); sn != "" {
			tls["server_name"] = sn
		}
		out["tls"] = tls
	case "reality":
		// Prefer private_key (inbound Reality); accept public_key as alias for ops mistakes.
		priv := optionalString(in.Params, "private_key")
		if priv == "" {
			priv = optionalString(in.Params, "public_key")
		}
		if priv == "" {
			return nil, fmt.Errorf("missing required field private_key")
		}
		shortID, err := requireString(in.Params, "short_id")
		if err != nil {
			return nil, err
		}
		serverName, err := requireString(in.Params, "server_name")
		if err != nil {
			return nil, err
		}
		hsServer, err := requireString(in.Params, "handshake_server")
		if err != nil {
			return nil, err
		}
		hsPort := 443
		if v, ok := in.Params["handshake_server_port"]; ok && v != nil && fmt.Sprint(v) != "" {
			p, err := asInt(v)
			if err != nil {
				return nil, fmt.Errorf("invalid handshake_server_port: %w", err)
			}
			hsPort = p
		}
		out["tls"] = map[string]any{
			"enabled":     true,
			"server_name": serverName,
			"reality": map[string]any{
				"enabled": true,
				"handshake": map[string]any{
					"server":      hsServer,
					"server_port": hsPort,
				},
				"private_key": priv,
				"short_id":    []string{shortID},
			},
		}
	default:
		return nil, fmt.Errorf("invalid tls_mode %q", tlsMode)
	}
	return out, nil
}

func mapHysteria2(in store.InboundConfig) (map[string]any, error) {
	listen, port, err := requireListenPort(in.Params)
	if err != nil {
		return nil, err
	}
	password, err := requireString(in.Params, "password")
	if err != nil {
		return nil, err
	}
	tlsBlock, err := buildTLS(in.Params, true)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"type":        "hysteria2",
		"tag":         inboundTag(in),
		"listen":      listen,
		"listen_port": port,
		"users": []map[string]any{
			{"name": "default", "password": password},
		},
		"tls": tlsBlock,
	}
	if v, ok := in.Params["up_mbps"]; ok && v != nil && fmt.Sprint(v) != "" {
		n, err := asInt(v)
		if err != nil {
			return nil, fmt.Errorf("invalid up_mbps: %w", err)
		}
		if n > 0 {
			out["up_mbps"] = n
		}
	}
	if v, ok := in.Params["down_mbps"]; ok && v != nil && fmt.Sprint(v) != "" {
		n, err := asInt(v)
		if err != nil {
			return nil, fmt.Errorf("invalid down_mbps: %w", err)
		}
		if n > 0 {
			out["down_mbps"] = n
		}
	}
	return out, nil
}

// buildTLS builds a sing-box tls object from PEM (preferred) or file paths.
func buildTLS(params map[string]any, required bool) (map[string]any, error) {
	certPEM := optionalString(params, "tls_cert_pem")
	keyPEM := optionalString(params, "tls_key_pem")
	if certPEM != "" && keyPEM != "" {
		return map[string]any{
			"enabled":     true,
			"certificate": []string{certPEM},
			"key":         []string{keyPEM},
		}, nil
	}
	certPath := optionalString(params, "tls_cert_path")
	keyPath := optionalString(params, "tls_key_path")
	if certPath != "" && keyPath != "" {
		return map[string]any{
			"enabled":          true,
			"certificate_path": certPath,
			"key_path":         keyPath,
		}, nil
	}
	if required {
		return nil, fmt.Errorf("missing TLS material (tls_cert_pem/tls_key_pem or tls_cert_path/tls_key_path)")
	}
	return map[string]any{"enabled": false}, nil
}

func requireListenPort(params map[string]any) (listen string, port int, err error) {
	listen = optionalString(params, "listen")
	if listen == "" {
		listen = "0.0.0.0"
	}
	if _, ok := params["port"]; !ok || params["port"] == nil {
		return "", 0, fmt.Errorf("missing required field port")
	}
	port, err = asInt(params["port"])
	if err != nil {
		return "", 0, fmt.Errorf("invalid port: %w", err)
	}
	if port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("port out of range: %d", port)
	}
	return listen, port, nil
}

func requireString(params map[string]any, key string) (string, error) {
	v, ok := params[key]
	if !ok || v == nil {
		return "", fmt.Errorf("missing required field %s", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("field %s must be a string", key)
	}
	if strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("missing required field %s", key)
	}
	return s, nil
}

func optionalString(params map[string]any, key string) string {
	v, ok := params[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprint(v)
	}
	return s
}

func asInt(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int32:
		return int(n), nil
	case int64:
		return int(n), nil
	case float64:
		return int(n), nil
	case float32:
		return int(n), nil
	case json.Number:
		i, err := n.Int64()
		return int(i), err
	case string:
		return strconv.Atoi(strings.TrimSpace(n))
	default:
		return 0, fmt.Errorf("cannot convert %T to int", v)
	}
}

var nonTagChars = regexp.MustCompile(`[^a-z0-9]+`)

func inboundTag(in store.InboundConfig) string {
	base := strings.ToLower(strings.TrimSpace(in.Name))
	base = nonTagChars.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		id := in.ID
		if len(id) > 8 {
			id = id[:8]
		}
		base = id
	}
	if base == "" {
		base = "inbound"
	}
	return "in-" + base
}
