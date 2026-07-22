package control_test

import (
	"context"
	"os"
	"path/filepath"
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

func TestBoxRuntimeIdempotentSameHash(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real box start in short mode")
	}
	rt := control.NewBoxRuntime(t.TempDir())
	good := `{
		"log": {"level": "error", "disabled": true},
		"inbounds": [],
		"outbounds": [{"type": "direct", "tag": "direct"}]
	}`
	if err := rt.Apply(context.Background(), good, "same"); err != nil {
		t.Fatalf("apply1: %v", err)
	}
	defer rt.Stop(context.Background())
	first := rt.Status(context.Background()).StartedAtUnix
	// Second apply with identical JSON+hash must be a no-op (no restart).
	if err := rt.Apply(context.Background(), good, "same"); err != nil {
		t.Fatalf("apply2: %v", err)
	}
	second := rt.Status(context.Background()).StartedAtUnix
	if first == 0 || first != second {
		t.Fatalf("expected same started_at for idempotent apply, got %d then %d", first, second)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("start while running: %v", err)
	}
	if rt.Status(context.Background()).StartedAtUnix != first {
		t.Fatal("Start while running should not restart box")
	}
}

func TestBoxRuntimeReplaceConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real box start in short mode")
	}
	rt := control.NewBoxRuntime(t.TempDir())
	cfg1 := `{
		"log": {"level": "error", "disabled": true},
		"inbounds": [],
		"outbounds": [{"type": "direct", "tag": "direct"}]
	}`
	cfg2 := `{
		"log": {"level": "warn", "disabled": true},
		"inbounds": [],
		"outbounds": [{"type": "direct", "tag": "direct"}]
	}`
	if err := rt.Apply(context.Background(), cfg1, "h1"); err != nil {
		t.Fatalf("apply1: %v", err)
	}
	defer rt.Stop(context.Background())
	if err := rt.Apply(context.Background(), cfg2, "h2"); err != nil {
		t.Fatalf("apply2: %v", err)
	}
	st := rt.Status(context.Background())
	if st.State != control.StateRunning || st.ConfigHash != "h2" {
		t.Fatalf("after replace: state=%s hash=%s", st.State, st.ConfigHash)
	}
}

func TestBoxRuntimeTrafficPersistence(t *testing.T) {
	dir := t.TempDir()

	// Write mock traffic.json
	path := filepath.Join(dir, "traffic.json")
	content := `{"uplink":12345,"downlink":67890}`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write mock traffic.json: %v", err)
	}

	rt := control.NewBoxRuntime(dir)
	defer rt.Stop(context.Background())

	metrics := rt.Metrics(context.Background())
	if metrics.UplinkBytes != 12345 || metrics.DownlinkBytes != 67890 {
		t.Fatalf("expected uplink=12345 downlink=67890, got uplink=%d downlink=%d",
			metrics.UplinkBytes, metrics.DownlinkBytes)
	}
}
