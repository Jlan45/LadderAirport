package converter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/labberairport/panel/internal/store"
)

func TestConvertShadowsocksGolden(t *testing.T) {
	in := store.InboundConfig{
		ID:       "ss-001",
		Name:     "ss-main",
		Protocol: "shadowsocks",
		Enabled:  true,
		Params: map[string]any{
			"listen":   "0.0.0.0",
			"port":     float64(8388),
			"method":   "aes-256-gcm",
			"password": "ss-secret",
			"network":  "tcp",
		},
	}
	assertGolden(t, []store.InboundConfig{in}, "ss.json")
}

func TestConvertTrojanGolden(t *testing.T) {
	in := store.InboundConfig{
		ID:       "tr-001",
		Name:     "trojan-main",
		Protocol: "trojan",
		Enabled:  true,
		Params: map[string]any{
			"listen":        "0.0.0.0",
			"port":          float64(443),
			"password":      "trojan-pass",
			"tls_cert_path": "/etc/certs/fullchain.pem",
			"tls_key_path":  "/etc/certs/privkey.pem",
		},
	}
	assertGolden(t, []store.InboundConfig{in}, "trojan.json")
}

func TestConvertVLESSRealityGolden(t *testing.T) {
	in := store.InboundConfig{
		ID:       "vl-001",
		Name:     "vless-main",
		Protocol: "vless",
		Enabled:  true,
		Params: map[string]any{
			"listen":           "::",
			"port":             float64(443),
			"uuid":             "bf000d23-0752-40b4-affe-68f7707a9661",
			"flow":             "xtls-rprx-vision",
			"tls_mode":         "reality",
			"private_key":      "UuMBgl7MXTPx9inmQp2UC7Jcnwc6XYbwDNebonM-FCc",
			"short_id":         "0123456789abcdef",
			"server_name":      "www.example.com",
			"handshake_server": "www.example.com",
		},
	}
	assertGolden(t, []store.InboundConfig{in}, "vless.json")
}

func TestConvertHysteria2Golden(t *testing.T) {
	in := store.InboundConfig{
		ID:       "hy-001",
		Name:     "hy2-main",
		Protocol: "hysteria2",
		Enabled:  true,
		Params: map[string]any{
			"listen":        "0.0.0.0",
			"port":          float64(8443),
			"password":      "hy2-pass",
			"tls_cert_path": "/etc/certs/fullchain.pem",
			"tls_key_path":  "/etc/certs/privkey.pem",
			"up_mbps":       float64(100),
			"down_mbps":     float64(200),
		},
	}
	assertGolden(t, []store.InboundConfig{in}, "hysteria2.json")
}

func TestConvertEmptyError(t *testing.T) {
	if _, err := Convert(nil); err == nil {
		t.Fatal("expected error for empty list")
	}
	if _, err := Convert([]store.InboundConfig{}); err == nil {
		t.Fatal("expected error for empty list")
	}
	// only disabled
	if _, err := Convert([]store.InboundConfig{{
		Name: "x", Protocol: "shadowsocks", Enabled: false,
		Params: map[string]any{"port": 1, "method": "aes-256-gcm", "password": "p"},
	}}); err == nil {
		t.Fatal("expected error when all disabled")
	}
}

func TestConvertPortConflict(t *testing.T) {
	a := store.InboundConfig{
		ID: "a", Name: "a", Protocol: "shadowsocks", Enabled: true,
		Params: map[string]any{
			"listen": "0.0.0.0", "port": float64(8388),
			"method": "aes-256-gcm", "password": "p1",
		},
	}
	b := store.InboundConfig{
		ID: "b", Name: "b", Protocol: "shadowsocks", Enabled: true,
		Params: map[string]any{
			"listen": "0.0.0.0", "port": float64(8388),
			"method": "aes-256-gcm", "password": "p2",
		},
	}
	_, err := Convert([]store.InboundConfig{a, b})
	if err == nil {
		t.Fatal("expected port conflict error")
	}
}

func TestConvertInvalidUUID(t *testing.T) {
	in := store.InboundConfig{
		ID: "vl-bad", Name: "vless-bad", Protocol: "vless", Enabled: true,
		Params: map[string]any{
			"listen": "0.0.0.0", "port": float64(443),
			"uuid": "not-a-uuid", "tls_mode": "none",
		},
	}
	_, err := Convert([]store.InboundConfig{in})
	if err == nil {
		t.Fatal("expected invalid UUID error")
	}
}

func TestConvertSkipsDisabled(t *testing.T) {
	enabled := store.InboundConfig{
		ID: "ss-001", Name: "ss-main", Protocol: "shadowsocks", Enabled: true,
		Params: map[string]any{
			"listen": "0.0.0.0", "port": float64(8388),
			"method": "aes-256-gcm", "password": "ss-secret", "network": "tcp",
		},
	}
	disabled := store.InboundConfig{
		ID: "ss-002", Name: "ss-off", Protocol: "shadowsocks", Enabled: false,
		Params: map[string]any{
			"listen": "0.0.0.0", "port": float64(8389),
			"method": "aes-256-gcm", "password": "other",
		},
	}
	assertGolden(t, []store.InboundConfig{disabled, enabled}, "ss.json")
}

func TestConvertMissingRequired(t *testing.T) {
	in := store.InboundConfig{
		ID: "ss", Name: "ss", Protocol: "shadowsocks", Enabled: true,
		Params: map[string]any{"port": float64(8388), "method": "aes-256-gcm"},
	}
	if _, err := Convert([]store.InboundConfig{in}); err == nil {
		t.Fatal("expected missing password error")
	}
}

func assertGolden(t *testing.T, inbounds []store.InboundConfig, goldenName string) {
	t.Helper()
	got, err := Convert(inbounds)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	path := filepath.Join("testdata", goldenName)
	wantBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", path, err)
	}
	var gotObj, wantObj any
	if err := json.Unmarshal(got, &gotObj); err != nil {
		t.Fatalf("unmarshal got: %v\n%s", err, got)
	}
	if err := json.Unmarshal(wantBytes, &wantObj); err != nil {
		t.Fatalf("unmarshal golden: %v", err)
	}
	if !reflect.DeepEqual(gotObj, wantObj) {
		gotPretty, _ := json.MarshalIndent(gotObj, "", "  ")
		wantPretty, _ := json.MarshalIndent(wantObj, "", "  ")
		t.Fatalf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", goldenName, gotPretty, wantPretty)
	}
}
