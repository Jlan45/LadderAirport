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
