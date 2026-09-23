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
