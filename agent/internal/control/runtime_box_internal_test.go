package control

import (
	"context"
	"testing"
)

// Stop must shut down the traffic persist loop and wait for it; a later Apply
// restarts it so traffic keeps being persisted for the process lifetime.
func TestBoxRuntimeStopStopsTrafficPersistLoop(t *testing.T) {
	r := NewBoxRuntime(t.TempDir())
	done := r.trafficPersistDone
	if done == nil {
		t.Fatal("persist loop not started")
	}
	if err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	default:
		t.Fatal("persist loop did not exit on Stop")
	}
	if r.stopTrafficPersist != nil {
		t.Fatal("persist loop state not cleared")
	}
	// Second Stop is a no-op.
	if err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Apply (even a failing one) restarts the loop.
	if err := r.Apply(context.Background(), `{not-json`, "h"); err == nil {
		t.Fatal("expected parse error")
	}
	if r.stopTrafficPersist == nil {
		t.Fatal("persist loop not restarted after Stop")
	}
	if err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestBoxRuntimeFRPInboundStartsWithLoopbackListener(t *testing.T) {
	r := NewBoxRuntime(t.TempDir())
	configuration := `{
		"log": {"level": "error", "disabled": true},
		"inbounds": [{"type": "shadowsocks", "tag": "ss-frp", "listen": "127.0.0.1", "listen_port": 55001,
			"method": "aes-256-gcm", "password": "secret"}],
		"outbounds": [{"type": "direct", "tag": "direct"}],
		"ladder_frpc": {"ss-frp": "{\"server_addr\":\"127.0.0.1\",\"server_port\":1,\"remote_port\":20001,\"local_port\":55001,\"token\":\"secret\"}"}
	}`
	if err := r.Apply(context.Background(), configuration, "frp-test"); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureDefaultDNS(t *testing.T) {
	// Case 1: missing dns block gets default DNS injected
	cfg1 := `{"inbounds":[],"outbounds":[{"type":"direct","tag":"direct"}]}`
	res1, err := prepareConfigJSON(cfg1)
	if err != nil {
		t.Fatalf("prepareConfigJSON failed: %v", err)
	}
	r := NewBoxRuntime(t.TempDir())
	opts1, err := r.parseOptions(res1)
	if err != nil {
		t.Fatalf("parseOptions failed: %v", err)
	}
	if opts1.DNS == nil || len(opts1.DNS.Servers) == 0 {
		t.Fatalf("expected default DNS servers to be injected, got %#v", opts1.DNS)
	}
	if opts1.DNS.Servers[0].Tag != "default-dns-alidns" {
		t.Fatalf("expected first server to be default-dns-alidns, got %s", opts1.DNS.Servers[0].Tag)
	}

	// Case 2: existing custom dns block is preserved
	cfg2 := `{"dns":{"servers":[{"type":"udp","tag":"custom-dns","server":"8.8.4.4","server_port":53}]}}`
	res2, err := prepareConfigJSON(cfg2)
	if err != nil {
		t.Fatalf("prepareConfigJSON failed: %v", err)
	}
	opts2, err := r.parseOptions(res2)
	if err != nil {
		t.Fatalf("parseOptions failed: %v", err)
	}
	if len(opts2.DNS.Servers) != 1 || opts2.DNS.Servers[0].Tag != "custom-dns" {
		t.Fatalf("expected custom DNS to be preserved, got %#v", opts2.DNS.Servers)
	}

	// Case 3: prepare is idempotent (same input → stable prepared JSON)
	again, err := prepareConfigJSON(cfg1)
	if err != nil {
		t.Fatalf("prepareConfigJSON second pass: %v", err)
	}
	if again != res1 {
		t.Fatalf("prepareConfigJSON not idempotent:\nfirst=%s\nsecond=%s", res1, again)
	}
	reprepared, err := prepareConfigJSON(res1)
	if err != nil {
		t.Fatalf("prepareConfigJSON on prepared: %v", err)
	}
	if reprepared != res1 {
		t.Fatalf("prepareConfigJSON changed already-prepared JSON:\ngot=%s\nwant=%s", reprepared, res1)
	}
}


