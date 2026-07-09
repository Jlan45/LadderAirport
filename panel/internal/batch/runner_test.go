package batch_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ladderairport/panel/internal/batch"
	"github.com/ladderairport/panel/internal/store"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// mockRPC records method calls and returns configurable outcomes.
type mockRPC struct {
	mu      sync.Mutex
	calls   []string
	applyOK bool
	startOK bool
	stopOK  bool
	failMsg string
	closed  bool
}

func (m *mockRPC) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.calls = append(m.calls, "close")
	return nil
}

func (m *mockRPC) ApplyConfig(_ context.Context, _, _ string, _ bool) (*agentv1.ApplyConfigResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "apply")
	if !m.applyOK {
		msg := m.failMsg
		if msg == "" {
			msg = "apply failed"
		}
		return &agentv1.ApplyConfigResponse{Ok: false, Message: msg}, nil
	}
	return &agentv1.ApplyConfigResponse{Ok: true, Message: "applied", AppliedHash: "h"}, nil
}

func (m *mockRPC) Start(_ context.Context) (*agentv1.StartResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "start")
	if !m.startOK {
		msg := m.failMsg
		if msg == "" {
			msg = "start failed"
		}
		return &agentv1.StartResponse{Ok: false, Message: msg}, nil
	}
	return &agentv1.StartResponse{Ok: true, Message: "started"}, nil
}

func (m *mockRPC) Stop(_ context.Context) (*agentv1.StopResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "stop")
	if !m.stopOK {
		msg := m.failMsg
		if msg == "" {
			msg = "stop failed"
		}
		return &agentv1.StopResponse{Ok: false, Message: msg}, nil
	}
	return &agentv1.StopResponse{Ok: true, Message: "stopped"}, nil
}

func (m *mockRPC) hasCall(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.calls {
		if c == name {
			return true
		}
	}
	return false
}

func seedNode(t *testing.T, s *store.Store, name, addr string, port int, token string) *store.Node {
	t.Helper()
	n := &store.Node{
		Name:     name,
		Address:  addr,
		GRPCPort: port,
		Token:    token,
		Status:   "unknown",
	}
	if err := s.CreateNode(n); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	return n
}

func seedSSInbound(t *testing.T, s *store.Store, name string, port int) *store.InboundConfig {
	t.Helper()
	in := &store.InboundConfig{
		Name:     name,
		Protocol: "shadowsocks",
		Enabled:  true,
		Params: map[string]any{
			"listen":   "0.0.0.0",
			"port":     port,
			"method":   "aes-128-gcm",
			"password": "password123",
		},
	}
	if err := s.CreateInbound(in); err != nil {
		t.Fatalf("CreateInbound: %v", err)
	}
	return in
}

func TestRunTaskStartPartialSuccess(t *testing.T) {
	s := openTestStore(t)
	n1 := seedNode(t, s, "n1", "10.0.0.1", 50051, "tok1")
	n2 := seedNode(t, s, "n2", "10.0.0.2", 50051, "tok2")
	n3 := seedNode(t, s, "n3", "10.0.0.3", 50051, "")

	task := &store.Task{
		Type:    "start",
		Status:  "pending",
		NodeIDs: []string{n1.ID, n2.ID, n3.ID},
	}
	if err := s.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	clients := map[string]*mockRPC{
		n1.ID: {startOK: true},
		n2.ID: {startOK: false, failMsg: "boom"},
		n3.ID: {startOK: true},
	}
	var dialTokens sync.Map

	r := batch.NewRunner(s, func() string { return "default-token" })
	r.Dial = func(_ context.Context, n store.Node, token string) (batch.NodeRPC, error) {
		dialTokens.Store(n.ID, token)
		m, ok := clients[n.ID]
		if !ok {
			return nil, fmt.Errorf("no mock for %s", n.ID)
		}
		return m, nil
	}

	if err := r.RunTask(context.Background(), task.ID); err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	got, err := s.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Status != "partial" {
		t.Fatalf("status = %q, want partial", got.Status)
	}
	if len(got.Results) != 3 {
		t.Fatalf("results len = %d, want 3", len(got.Results))
	}

	byNode := map[string]store.TaskNodeResult{}
	for _, res := range got.Results {
		byNode[res.NodeID] = res
	}
	if !byNode[n1.ID].OK {
		t.Fatalf("n1 should succeed: %+v", byNode[n1.ID])
	}
	if byNode[n2.ID].OK || byNode[n2.ID].Message != "boom" {
		t.Fatalf("n2 should fail with boom: %+v", byNode[n2.ID])
	}
	if !byNode[n3.ID].OK {
		t.Fatalf("n3 should succeed: %+v", byNode[n3.ID])
	}

	// Token resolution: node token wins; empty falls back to DefaultToken.
	if v, _ := dialTokens.Load(n1.ID); v != "tok1" {
		t.Fatalf("n1 token = %v, want tok1", v)
	}
	if v, _ := dialTokens.Load(n3.ID); v != "default-token" {
		t.Fatalf("n3 token = %v, want default-token", v)
	}
	if !clients[n1.ID].hasCall("start") || !clients[n1.ID].hasCall("close") {
		t.Fatalf("n1 calls: %+v", clients[n1.ID].calls)
	}
}

func TestRunTaskAllFail(t *testing.T) {
	s := openTestStore(t)
	n1 := seedNode(t, s, "n1", "10.0.0.1", 50051, "t")
	n2 := seedNode(t, s, "n2", "10.0.0.2", 50051, "t")

	task := &store.Task{
		Type:    "stop",
		Status:  "pending",
		NodeIDs: []string{n1.ID, n2.ID},
	}
	if err := s.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	r := batch.NewRunner(s, func() string { return "t" })
	r.Dial = func(_ context.Context, n store.Node, _ string) (batch.NodeRPC, error) {
		return &mockRPC{stopOK: false, failMsg: "down"}, nil
	}

	if err := r.RunTask(context.Background(), task.ID); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	got, err := s.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Status != "failed" {
		t.Fatalf("status = %q, want failed", got.Status)
	}
}

func TestRunTaskAllSuccess(t *testing.T) {
	s := openTestStore(t)
	n1 := seedNode(t, s, "n1", "10.0.0.1", 50051, "t")
	n2 := seedNode(t, s, "n2", "10.0.0.2", 50051, "t")

	task := &store.Task{
		Type:    "start",
		Status:  "pending",
		NodeIDs: []string{n1.ID, n2.ID},
	}
	if err := s.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	r := batch.NewRunner(s, func() string { return "t" })
	r.Dial = func(_ context.Context, _ store.Node, _ string) (batch.NodeRPC, error) {
		return &mockRPC{startOK: true}, nil
	}

	if err := r.RunTask(context.Background(), task.ID); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	got, err := s.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Status != "success" {
		t.Fatalf("status = %q, want success", got.Status)
	}
}

func TestRunTaskApply(t *testing.T) {
	s := openTestStore(t)
	n := seedNode(t, s, "n1", "10.0.0.1", 50051, "t")
	in := seedSSInbound(t, s, "ss1", 8388)
	if err := s.SetNodeInbounds(n.ID, []string{in.ID}); err != nil {
		t.Fatalf("SetNodeInbounds: %v", err)
	}

	task := &store.Task{
		Type:    "apply",
		Status:  "pending",
		NodeIDs: []string{n.ID},
	}
	if err := s.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	mock := &mockRPC{applyOK: true}
	r := batch.NewRunner(s, func() string { return "t" })
	r.Dial = func(_ context.Context, _ store.Node, _ string) (batch.NodeRPC, error) {
		return mock, nil
	}

	if err := r.RunTask(context.Background(), task.ID); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	got, err := s.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Status != "success" {
		t.Fatalf("status = %q results=%+v", got.Status, got.Results)
	}
	if !mock.hasCall("apply") {
		t.Fatalf("expected apply call, got %+v", mock.calls)
	}

	snap, err := s.LatestSnapshot(n.ID)
	if err != nil {
		t.Fatalf("LatestSnapshot: %v", err)
	}
	if snap.ConfigJSON == "" || snap.ConfigHash == "" {
		t.Fatalf("empty snapshot: %+v", snap)
	}
	if snap.TaskID != task.ID {
		t.Fatalf("snapshot task_id = %q, want %q", snap.TaskID, task.ID)
	}

	node, err := s.GetNode(n.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if node.ConfigHash != snap.ConfigHash {
		t.Fatalf("node hash %q != snap hash %q", node.ConfigHash, snap.ConfigHash)
	}
}

func TestRunTaskDialError(t *testing.T) {
	s := openTestStore(t)
	n := seedNode(t, s, "n1", "10.0.0.1", 50051, "t")
	task := &store.Task{
		Type:    "start",
		Status:  "pending",
		NodeIDs: []string{n.ID},
	}
	if err := s.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	r := batch.NewRunner(s, func() string { return "t" })
	r.Timeout = time.Second
	r.Dial = func(_ context.Context, _ store.Node, _ string) (batch.NodeRPC, error) {
		return nil, fmt.Errorf("connection refused")
	}

	if err := r.RunTask(context.Background(), task.ID); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	got, err := s.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Status != "failed" {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if len(got.Results) != 1 || got.Results[0].OK {
		t.Fatalf("results: %+v", got.Results)
	}
}
