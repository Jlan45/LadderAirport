package batch_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ladderairport/panel/internal/batch"
	"github.com/ladderairport/panel/internal/store"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
)

type bootRPC struct {
	applies int
	starts  int
}

func (b *bootRPC) Close() error { return nil }
func (b *bootRPC) ApplyConfig(context.Context, string, string, bool) (*agentv1.ApplyConfigResponse, error) {
	b.applies++
	return &agentv1.ApplyConfigResponse{Ok: true, Message: "applied", AppliedHash: "h"}, nil
}
func (b *bootRPC) Start(context.Context) (*agentv1.StartResponse, error) {
	b.starts++
	return &agentv1.StartResponse{Ok: true, Message: "started"}, nil
}
func (b *bootRPC) Stop(context.Context) (*agentv1.StopResponse, error) {
	return &agentv1.StopResponse{Ok: true}, nil
}

func TestBootstrapAllApplyOnly(t *testing.T) {
	db := filepath.Join(t.TempDir(), "t.db")
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	n := &store.Node{Name: "n1", Address: "127.0.0.1", GRPCPort: 50051, Status: "unknown"}
	if err := st.CreateNode(n); err != nil {
		t.Fatal(err)
	}
	in := &store.InboundConfig{
		Name: "ss", Protocol: "shadowsocks", Enabled: true,
		Params: map[string]any{"listen": "0.0.0.0", "port": float64(19000), "method": "aes-256-gcm", "password": "p"},
	}
	if err := st.CreateInbound(in); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeInbounds(n.ID, []string{in.ID}); err != nil {
		t.Fatal(err)
	}

	rpc := &bootRPC{}
	r := batch.NewRunner(st, func() string { return "t" })
	r.Dial = func(ctx context.Context, node store.Node, token string) (batch.NodeRPC, error) {
		return rpc, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.BootstrapAll(ctx); err != nil {
		t.Fatal(err)
	}
	// Apply alone starts the agent core; no separate Start RPC.
	if rpc.applies != 1 || rpc.starts != 0 {
		t.Fatalf("applies=%d starts=%d want 1/0", rpc.applies, rpc.starts)
	}
	tasks, err := st.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Type != "apply" {
		t.Fatalf("expected single apply task, got %+v", tasks)
	}
}

func TestBootstrapPendingSkipsSynced(t *testing.T) {
	db := filepath.Join(t.TempDir(), "t.db")
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	synced := &store.Node{
		Name: "ok", Address: "127.0.0.1", GRPCPort: 1,
		Status: "online", RuntimeState: "running",
	}
	if err := st.CreateNode(synced); err != nil {
		t.Fatal(err)
	}
	pending := &store.Node{
		Name: "late", Address: "127.0.0.1", GRPCPort: 2,
		Status: "unreachable",
	}
	if err := st.CreateNode(pending); err != nil {
		t.Fatal(err)
	}
	in := &store.InboundConfig{
		Name: "ss", Protocol: "shadowsocks", Enabled: true,
		Params: map[string]any{"listen": "0.0.0.0", "port": float64(19001), "method": "aes-256-gcm", "password": "p"},
	}
	if err := st.CreateInbound(in); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeInbounds(synced.ID, []string{in.ID}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeInbounds(pending.ID, []string{in.ID}); err != nil {
		t.Fatal(err)
	}

	rpc := &bootRPC{}
	r := batch.NewRunner(st, func() string { return "t" })
	r.Dial = func(ctx context.Context, node store.Node, token string) (batch.NodeRPC, error) {
		return rpc, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.BootstrapPending(ctx); err != nil {
		t.Fatal(err)
	}
	// Only the unsynced node should be applied (no Start).
	if rpc.applies != 1 || rpc.starts != 0 {
		t.Fatalf("applies=%d starts=%d want 1/0", rpc.applies, rpc.starts)
	}
}
