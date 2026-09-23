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
