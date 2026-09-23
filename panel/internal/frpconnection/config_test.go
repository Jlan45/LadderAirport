package frpconnection

import "testing"

func TestParseParams(t *testing.T) {
	params := map[string]any{
		"frp_enabled": true,
		"frpc_config": `{"server_addr":"frps.example.com","server_port":7000,"remote_port":20001,"token":"secret"}`,
	}
	cfg, enabled, err := ParseParams(params)
	if err != nil || !enabled || cfg.ServerAddr != "frps.example.com" || cfg.RemotePort != 20001 {
		t.Fatalf("ParseParams = %+v, %v, %v", cfg, enabled, err)
	}
	params["frpc_config"] = `{"server_addr":"frps.example.com","server_port":7000,"remote_port":0,"token":"secret"}`
	if _, _, err := ParseParams(params); err == nil {
		t.Fatal("expected invalid remote port to be rejected")
	}
}

func TestParseParamsServiceStyleFields(t *testing.T) {
	params := map[string]any{
		"frp_enabled": true,
		"frpc_config": `user = "s-account"
auth.token = "test-token"
serverAddr = "frp.example.com"
serverPort = 8088
transport.tls.enable = false
transport.tls.disableCustomTLSFirstByte = false

[[proxies]]
# id = 28312132
name = "LadderPro"
type = "tcp"
localIP = "192.168.123.141"
localPort = 64192
remotePort = 57115`,
	}
	cfg, enabled, err := ParseParams(params)
	if err != nil || !enabled || cfg.User != "s-account" || cfg.ProxyName != "LadderPro" || cfg.RemotePort != 57115 ||
		cfg.TLSEnable == nil || *cfg.TLSEnable || cfg.TLSDisableCustomTLSFirstByte == nil || *cfg.TLSDisableCustomTLSFirstByte {
		t.Fatalf("service-style FRPC config = %+v, %v, %v", cfg, enabled, err)
	}
	params["frpc_config"] = `serverAddr = "frp.example.com"
serverPort = 8088
auth.token = "test-token"
[[proxies]]
name = "a"
type = "tcp"
remotePort = 57115
[[proxies]]
name = "b"
type = "tcp"
remotePort = 57116`
	if _, _, err := ParseParams(params); err == nil {
		t.Fatal("expected multiple proxies to be rejected")
	}
	params["frpc_config"] = `serverAddr = "frp.example.com"
serverPort = 8088
auth.token = "test-token"
unsupportedOption = true
[[proxies]]
name = "a"
type = "tcp"
remotePort = 57115`
	if _, _, err := ParseParams(params); err == nil {
		t.Fatal("expected unsupported options to be rejected rather than silently ignored")
	}
}
