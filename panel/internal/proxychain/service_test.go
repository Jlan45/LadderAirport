package proxychain

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ladderairport/panel/internal/store"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
)

type fakeAgent struct {
	nodeID string
	fleet  *fakeFleet
}

type fakeFleet struct {
	mu       sync.Mutex
	applies  []string
	failOnce map[string]bool
}

func (a *fakeAgent) Close() error { return nil }

func (a *fakeAgent) Ping(context.Context) (*agentv1.PingResponse, error) {
	return &agentv1.PingResponse{Capabilities: []string{Capability}}, nil
}

func (a *fakeAgent) ApplyConfig(_ context.Context, _, _ string, _ bool) (*agentv1.ApplyConfigResponse, error) {
	a.fleet.mu.Lock()
	defer a.fleet.mu.Unlock()
	a.fleet.applies = append(a.fleet.applies, a.nodeID)
	if a.fleet.failOnce[a.nodeID] {
		delete(a.fleet.failOnce, a.nodeID)
		return &agentv1.ApplyConfigResponse{Ok: false, Message: "injected failure"}, nil
	}
	return &agentv1.ApplyConfigResponse{Ok: true}, nil
}

func (a *fakeAgent) ProbeOutbound(context.Context, string, string) (*agentv1.ProbeOutboundResponse, error) {
	return &agentv1.ProbeOutboundResponse{Ok: true, DelayMs: 7}, nil
}

func TestDeployAppliesExitToEntry(t *testing.T) {
	st, chain, nodeIDs := chainFixture(t)
	fleet := &fakeFleet{failOnce: map[string]bool{}}
	service := NewService(st, nil, nil)
	service.Dial = func(_ context.Context, node store.Node, _ string) (Agent, error) {
		return &fakeAgent{nodeID: node.ID, fleet: fleet}, nil
	}

	if err := service.Deploy(context.Background(), *chain); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if got, want := fleet.applies, []string{nodeIDs[1], nodeIDs[0]}; !equalStrings(got, want) {
		t.Fatalf("apply order = %v, want %v", got, want)
	}
	deployed, err := st.GetProxyChain(chain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !deployed.Enabled || deployed.State != "healthy" || deployed.LastProbeDelayMS != 7 {
		t.Fatalf("deployed chain = %+v", deployed)
	}
}

func TestDeployFailureRollsBackChangedNodes(t *testing.T) {
	st, chain, nodeIDs := chainFixture(t)
	fleet := &fakeFleet{failOnce: map[string]bool{nodeIDs[0]: true}}
	service := NewService(st, nil, nil)
	service.Dial = func(_ context.Context, node store.Node, _ string) (Agent, error) {
		return &fakeAgent{nodeID: node.ID, fleet: fleet}, nil
	}

	if err := service.Deploy(context.Background(), *chain); err == nil {
		t.Fatal("Deploy succeeded, want injected failure")
	}
	want := []string{nodeIDs[1], nodeIDs[0], nodeIDs[1]}
	if !equalStrings(fleet.applies, want) {
		t.Fatalf("apply/rollback order = %v, want %v", fleet.applies, want)
	}
	unchanged, err := st.GetProxyChain(chain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Enabled || unchanged.State != "disabled" {
		t.Fatalf("failed deployment committed runtime state: %+v", unchanged)
	}
}

func chainFixture(t *testing.T) (*store.Store, *store.ProxyChain, []string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "chain.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	nodes := []*store.Node{
		{Name: "entry", Address: "10.0.0.1", PublicAddress: "entry.example", GRPCPort: 9090},
		{Name: "exit", Address: "10.0.0.2", PublicAddress: "exit.example", GRPCPort: 9090},
	}
	inbounds := []*store.InboundConfig{
		{Name: "entry-in", Protocol: "shadowsocks", Enabled: true, Params: map[string]any{
			"listen": "0.0.0.0", "port": 8388, "method": "aes-256-gcm", "password": "entry-secret", "network": "tcp",
		}},
		{Name: "exit-in", Protocol: "shadowsocks", Enabled: true, Params: map[string]any{
			"listen": "0.0.0.0", "port": 8389, "method": "aes-256-gcm", "password": "exit-secret", "network": "tcp",
		}},
	}
	for _, node := range nodes {
		if err := st.CreateNode(node); err != nil {
			t.Fatal(err)
		}
	}
	for _, inbound := range inbounds {
		if err := st.CreateInbound(inbound); err != nil {
			t.Fatal(err)
		}
	}
	chain := &store.ProxyChain{Name: "test-chain", Hops: []store.ProxyChainHop{
		{NodeID: nodes[0].ID, InboundID: inbounds[0].ID},
		{NodeID: nodes[1].ID, InboundID: inbounds[1].ID},
	}}
	if err := st.CreateProxyChain(chain); err != nil {
		t.Fatal(err)
	}
	return st, chain, []string{nodes[0].ID, nodes[1].ID}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
