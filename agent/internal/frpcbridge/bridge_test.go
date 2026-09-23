package frpcbridge

import "testing"

func TestBuildConfigsMapsServiceStyleFRPCFields(t *testing.T) {
	falseValue := false
	common, proxy, err := buildConfigs("in-test", ConnectionConfig{
		User: "s-account", ServerAddr: "frp.example.com", ServerPort: 8088,
		RemotePort: 57115, LocalPort: 64192, Token: "test-token", ProxyName: "LadderPro",
		TLSEnable: &falseValue, TLSDisableCustomTLSFirstByte: &falseValue,
	})
	if err != nil {
		t.Fatal(err)
	}
	if common.User != "s-account" || common.Auth.Token != "test-token" || common.ServerPort != 8088 {
		t.Fatalf("FRPC common config = %+v", common)
	}
	if common.Transport.TLS.Enable == nil || *common.Transport.TLS.Enable ||
		common.Transport.TLS.DisableCustomTLSFirstByte == nil || *common.Transport.TLS.DisableCustomTLSFirstByte {
		t.Fatalf("TLS flags were not kept false: %+v", common.Transport.TLS)
	}
	if proxy.Name != "LadderPro" || proxy.Type != "tcp" || proxy.LocalIP != "127.0.0.1" ||
		proxy.LocalPort != 64192 || proxy.RemotePort != 57115 {
		t.Fatalf("FRPC proxy config = %+v", proxy)
	}
}

func TestBuildConfigsPreservesLegacyDefaults(t *testing.T) {
	common, proxy, err := buildConfigs("in-test", ConnectionConfig{
		ServerAddr: "frp.example.com", ServerPort: 7000,
		RemotePort: 20001, LocalPort: 55001, Token: "test-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if common.Transport.TLS.Enable == nil || !*common.Transport.TLS.Enable ||
		common.Transport.TLS.DisableCustomTLSFirstByte == nil || !*common.Transport.TLS.DisableCustomTLSFirstByte ||
		proxy.Name != "in-test" {
		t.Fatalf("legacy defaults changed: TLS=%+v proxy=%s", common.Transport.TLS, proxy.Name)
	}
}
