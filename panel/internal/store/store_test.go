package store

import (
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCreateAndListNodes(t *testing.T) {
	s := openTestStore(t)

	n := &Node{
		Name:     "node-a",
		Address:  "10.0.0.1",
		GRPCPort: 9090,
		Labels:   []string{"edge", "prod"},
		Status:   "unknown",
	}
	if err := s.CreateNode(n); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if n.ID == "" {
		t.Fatal("expected generated node ID")
	}
	if n.CreatedAtUnix == 0 || n.UpdatedAtUnix == 0 {
		t.Fatal("expected timestamps to be set")
	}

	nodes, err := s.ListNodes()
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("ListNodes len = %d, want 1", len(nodes))
	}
	got := nodes[0]
	if got.ID != n.ID || got.Name != "node-a" || got.Address != "10.0.0.1" || got.GRPCPort != 9090 {
		t.Fatalf("unexpected node: %+v", got)
	}
	if len(got.Labels) != 2 || got.Labels[0] != "edge" || got.Labels[1] != "prod" {
		t.Fatalf("unexpected labels: %v", got.Labels)
	}

	// List by labels (any match)
	matched, err := s.ListNodesByLabels([]string{"prod", "missing"})
	if err != nil {
		t.Fatalf("ListNodesByLabels: %v", err)
	}
	if len(matched) != 1 || matched[0].ID != n.ID {
		t.Fatalf("ListNodesByLabels = %+v", matched)
	}
	none, err := s.ListNodesByLabels([]string{"nope"})
	if err != nil {
		t.Fatalf("ListNodesByLabels empty: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("expected no match, got %d", len(none))
	}
}

func TestCreateInboundAndAttach(t *testing.T) {
	s := openTestStore(t)

	n := &Node{Name: "n1", Address: "127.0.0.1", GRPCPort: 9000}
	if err := s.CreateNode(n); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	in := &InboundConfig{
		Name:     "ss-main",
		Protocol: "shadowsocks",
		Params: map[string]any{
			"method":   "aes-256-gcm",
			"password": "secret",
			"port":     float64(8388),
		},
		Enabled: true,
	}
	if err := s.CreateInbound(in); err != nil {
		t.Fatalf("CreateInbound: %v", err)
	}
	if in.ID == "" {
		t.Fatal("expected generated inbound ID")
	}

	inbounds, err := s.ListInbounds()
	if err != nil {
		t.Fatalf("ListInbounds: %v", err)
	}
	if len(inbounds) != 1 || inbounds[0].Protocol != "shadowsocks" {
		t.Fatalf("ListInbounds = %+v", inbounds)
	}

	if err := s.SetNodeInbounds(n.ID, []string{in.ID}); err != nil {
		t.Fatalf("SetNodeInbounds: %v", err)
	}
	attached, err := s.ListInboundsForNode(n.ID)
	if err != nil {
		t.Fatalf("ListInboundsForNode: %v", err)
	}
	if len(attached) != 1 || attached[0].ID != in.ID {
		t.Fatalf("ListInboundsForNode = %+v", attached)
	}
	if method, ok := attached[0].Params["method"].(string); !ok || method != "aes-256-gcm" {
		t.Fatalf("params not preserved: %+v", attached[0].Params)
	}

	// Replace attachment with empty
	if err := s.SetNodeInbounds(n.ID, nil); err != nil {
		t.Fatalf("SetNodeInbounds clear: %v", err)
	}
	attached, err = s.ListInboundsForNode(n.ID)
	if err != nil {
		t.Fatalf("ListInboundsForNode after clear: %v", err)
	}
	if len(attached) != 0 {
		t.Fatalf("expected no attachments, got %d", len(attached))
	}
}

func TestCreateAndUpdateTask(t *testing.T) {
	s := openTestStore(t)

	task := &Task{
		Type:    "apply",
		Status:  "pending",
		NodeIDs: []string{"n1", "n2"},
	}
	if err := s.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.ID == "" {
		t.Fatal("expected generated task ID")
	}

	got, err := s.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Status != "pending" || len(got.NodeIDs) != 2 {
		t.Fatalf("unexpected task: %+v", got)
	}

	task.Status = "success"
	task.Results = []TaskNodeResult{
		{NodeID: "n1", OK: true, Message: "ok"},
		{NodeID: "n2", OK: false, Message: "timeout"},
	}
	if err := s.UpdateTask(task); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	got, err = s.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask after update: %v", err)
	}
	if got.Status != "success" {
		t.Fatalf("status = %q, want success", got.Status)
	}
	if len(got.Results) != 2 || !got.Results[0].OK || got.Results[1].OK {
		t.Fatalf("results = %+v", got.Results)
	}

	tasks, err := s.ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("ListTasks len = %d", len(tasks))
	}
}

func TestGetAndSaveSettings(t *testing.T) {
	s := openTestStore(t)

	st, err := s.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if st.GRPCTimeoutSec != 10 || st.MaxConcurrency != 10 || st.ListenAddr != ":8080" {
		t.Fatalf("unexpected defaults: %+v", st)
	}
	if st.DefaultAgentToken != "" {
		t.Fatalf("expected empty default token, got %q", st.DefaultAgentToken)
	}

	st.AdminPasswordHash = "hash"
	st.DefaultAgentToken = "agent-token"
	st.GRPCTimeoutSec = 30
	st.MaxConcurrency = 5
	st.ListenAddr = ":9090"
	if err := s.SaveSettings(st); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	got, err := s.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings after save: %v", err)
	}
	if got.AdminPasswordHash != "hash" ||
		got.DefaultAgentToken != "agent-token" ||
		got.GRPCTimeoutSec != 30 ||
		got.MaxConcurrency != 5 ||
		got.ListenAddr != ":9090" {
		t.Fatalf("settings not persisted: %+v", got)
	}
}

func TestUpdateNodeGetDelete(t *testing.T) {
	s := openTestStore(t)

	n := &Node{Name: "x", Address: "1.1.1.1", GRPCPort: 1, Labels: []string{"a"}}
	if err := s.CreateNode(n); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	n.Name = "y"
	n.Status = "online"
	n.ConfigHash = "abc"
	if err := s.UpdateNode(n); err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}
	got, err := s.GetNode(n.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got.Name != "y" || got.Status != "online" || got.ConfigHash != "abc" {
		t.Fatalf("GetNode = %+v", got)
	}
	if err := s.DeleteNode(n.ID); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	if _, err := s.GetNode(n.ID); err == nil {
		t.Fatal("expected GetNode error after delete")
	}
}

func TestSaveAndLatestSnapshot(t *testing.T) {
	s := openTestStore(t)

	if err := s.SaveSnapshot(&ConfigSnapshot{
		NodeID:     "node-1",
		ConfigJSON: `{"a":1}`,
		ConfigHash: "h1",
		TaskID:     "t1",
	}); err != nil {
		t.Fatalf("SaveSnapshot 1: %v", err)
	}
	// Ensure later timestamp
	if err := s.SaveSnapshot(&ConfigSnapshot{
		NodeID:        "node-1",
		ConfigJSON:    `{"a":2}`,
		ConfigHash:    "h2",
		CreatedAtUnix: 9999999999,
	}); err != nil {
		t.Fatalf("SaveSnapshot 2: %v", err)
	}

	snap, err := s.LatestSnapshot("node-1")
	if err != nil {
		t.Fatalf("LatestSnapshot: %v", err)
	}
	if snap.ConfigHash != "h2" || snap.ConfigJSON != `{"a":2}` {
		t.Fatalf("latest = %+v", snap)
	}
}

func TestNodeEgressInterface(t *testing.T) {
	s := openTestStore(t)
	n := &Node{Name: "egress", Address: "1.2.3.4", GRPCPort: 50051, EgressInterface: "eth1"}
	if err := s.CreateNode(n); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	got, err := s.GetNode(n.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got.EgressInterface != "eth1" {
		t.Fatalf("EgressInterface = %q", got.EgressInterface)
	}
	n.EgressInterface = ""
	if err := s.UpdateNode(n); err != nil {
		t.Fatalf("UpdateNode clear: %v", err)
	}
	got, err = s.GetNode(n.ID)
	if err != nil {
		t.Fatalf("GetNode after clear: %v", err)
	}
	if got.EgressInterface != "" {
		t.Fatalf("expected empty EgressInterface, got %q", got.EgressInterface)
	}
}
