package control_test

import (
	"context"
	"testing"

	"github.com/ladderairport/agent/internal/control"
)

func TestBoxRuntimeInvalidJSONKeepsStopped(t *testing.T) {
	rt := control.NewBoxRuntime(t.TempDir())
	err := rt.Apply(context.Background(), `{not-json`, "h")
	if err == nil {
		t.Fatal("expected error")
	}
	if rt.Status(context.Background()).State == control.StateRunning {
		t.Fatal("should not be running")
	}
}

func TestBoxRuntimeInvalidJSONKeepsPreviousRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real box start in short mode")
	}

	rt := control.NewBoxRuntime(t.TempDir())
	// Minimal valid config: no listen ports, direct-only.
	good := `{
		"log": {"level": "error", "disabled": true},
		"inbounds": [],
		"outbounds": [{"type": "direct", "tag": "direct"}]
	}`
	if err := rt.Apply(context.Background(), good, "good-hash"); err != nil {
		t.Fatalf("apply good config: %v", err)
	}
	defer rt.Stop(context.Background())

	if st := rt.Status(context.Background()); st.State != control.StateRunning {
		t.Fatalf("expected running after good apply, got %s err=%s", st.State, st.LastError)
	}

	err := rt.Apply(context.Background(), `{not-json`, "bad")
	if err == nil {
		t.Fatal("expected error on bad json")
	}
	st := rt.Status(context.Background())
	if st.State != control.StateRunning {
		t.Fatalf("should keep previous instance running, got state=%s", st.State)
	}
	if st.ConfigHash != "good-hash" {
		t.Fatalf("config hash should remain good-hash, got %q", st.ConfigHash)
	}
}

func TestBoxRuntimeStopStart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real box start in short mode")
	}

	rt := control.NewBoxRuntime(t.TempDir())
	good := `{
		"log": {"level": "error", "disabled": true},
		"inbounds": [],
		"outbounds": [{"type": "direct", "tag": "direct"}]
	}`
	if err := rt.Apply(context.Background(), good, "h1"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if st := rt.Status(context.Background()); st.State != control.StateStopped {
		t.Fatalf("expected stopped, got %s", st.State)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if st := rt.Status(context.Background()); st.State != control.StateRunning {
		t.Fatalf("expected running after start, got %s err=%s", st.State, st.LastError)
	}
	_ = rt.Stop(context.Background())
}
