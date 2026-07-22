package store

import (
	"database/sql"
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

func TestSubscriptionInboundModeMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE subscriptions (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			format TEXT NOT NULL,
			token TEXT NOT NULL UNIQUE,
			inbound_ids_json TEXT NOT NULL DEFAULT '[]',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at_unix INTEGER NOT NULL,
			updated_at_unix INTEGER NOT NULL
		);
		INSERT INTO subscriptions VALUES ('all', 'all', 'clash', 'token-all', '[]', 1, 1, 1);
		INSERT INTO subscriptions VALUES ('selected', 'selected', 'clash', 'token-selected', '["in-1"]', 1, 1, 1);
		INSERT INTO subscriptions VALUES ('null-ids', 'null ids', 'clash', 'token-null', 'null', 1, 1, 1);
	`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	all, err := s.GetSubscription("all")
	if err != nil {
		t.Fatal(err)
	}
	if !all.IncludeAllInbounds || len(all.InboundIDs) != 0 {
		t.Fatalf("legacy empty filter migration = %+v, want include all", all)
	}
	selected, err := s.GetSubscription("selected")
	if err != nil {
		t.Fatal(err)
	}
	if selected.IncludeAllInbounds || len(selected.InboundIDs) != 1 || selected.InboundIDs[0] != "in-1" {
		t.Fatalf("legacy selected filter migration = %+v", selected)
	}
	nullIDs, err := s.GetSubscription("null-ids")
	if err != nil {
		t.Fatal(err)
	}
	if !nullIDs.IncludeAllInbounds || nullIDs.InboundIDs == nil || len(nullIDs.InboundIDs) != 0 {
		t.Fatalf("legacy null filter migration = %+v, want non-nil empty include-all filter", nullIDs)
	}
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

func TestUpdateNodeOperatorFieldsPreservesNewerRuntimeState(t *testing.T) {
	s := openTestStore(t)
	n := &Node{
		Name:           "edge",
		Address:        "192.0.2.10",
		GRPCPort:       50051,
		Token:          "old-token",
		Labels:         []string{"prod"},
		Status:         "unknown",
		RuntimeState:   "stopped",
		AgentVersion:   "v1",
		SingboxVersion: "1.0",
		Connections:    1,
		UplinkBytes:    2,
		DownlinkBytes:  3,
		CPUPercent:     4,
		MemoryRSSBytes: 5,
		MetricsAtUnix:  6,
		LastSeenUnix:   7,
		ConfigHash:     "old-hash",
		LastError:      "old-error",
	}
	if err := s.CreateNode(n); err != nil {
		t.Fatal(err)
	}

	// The operator starts editing this stale snapshot before a live refresh lands.
	stale, err := s.GetNode(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	live := *stale
	live.Status = "online"
	live.RuntimeState = "running"
	live.AgentVersion = "v2"
	live.SingboxVersion = "2.0"
	live.Connections = 101
	live.UplinkBytes = 102
	live.DownlinkBytes = 103
	live.CPUPercent = 10.5
	live.MemoryRSSBytes = 104
	live.MetricsAtUnix = 105
	live.LastSeenUnix = 106
	live.ConfigHash = "new-hash"
	live.LastError = ""
	if err := s.UpdateNode(&live); err != nil {
		t.Fatal(err)
	}

	name := stale.Name + "-renamed"
	token := "new-token"
	labels := append(stale.Labels, "edited")
	if err := s.UpdateNodeOperatorFields(n.ID, NodeOperatorUpdate{
		Name:   &name,
		Token:  &token,
		Labels: &labels,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetNode(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != name || got.Token != token || len(got.Labels) != 2 {
		t.Fatalf("operator fields were not updated: %+v", got)
	}
	if got.Status != live.Status || got.RuntimeState != live.RuntimeState ||
		got.AgentVersion != live.AgentVersion || got.SingboxVersion != live.SingboxVersion ||
		got.Connections != live.Connections || got.UplinkBytes != live.UplinkBytes ||
		got.DownlinkBytes != live.DownlinkBytes || got.CPUPercent != live.CPUPercent ||
		got.MemoryRSSBytes != live.MemoryRSSBytes || got.MetricsAtUnix != live.MetricsAtUnix ||
		got.LastSeenUnix != live.LastSeenUnix || got.ConfigHash != live.ConfigHash ||
		got.LastError != live.LastError {
		t.Fatalf("operator update overwrote newer runtime state: got=%+v live=%+v", got, live)
	}
}

func TestUpdateExternalSourceInvalidatesCacheOnFetchInputChange(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mutate    func(*ExternalSource)
		wantClear bool
	}{
		{
			name: "url",
			mutate: func(src *ExternalSource) {
				src.URL = "https://new.example/sub"
			},
			wantClear: true,
		},
		{
			name: "headers",
			mutate: func(src *ExternalSource) {
				src.Headers = map[string]string{"Authorization": "Bearer new"}
			},
			wantClear: true,
		},
		{
			name: "metadata-only",
			mutate: func(src *ExternalSource) {
				src.Name = "renamed"
				src.RefreshIntervalSec = 3600
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			src := &ExternalSource{
				Name:               "source",
				URL:                "https://old.example/sub",
				Headers:            map[string]string{"Authorization": "Bearer old"},
				Enabled:            true,
				RefreshIntervalSec: 60,
				LastFetchUnix:      10,
				LastSuccessUnix:    9,
				LastError:          "cached warning",
				ContentType:        "clash_yaml",
				CachedBody:         "proxies: []",
				CachedProxyCount:   4,
			}
			if err := s.CreateExternalSource(src); err != nil {
				t.Fatal(err)
			}
			tc.mutate(src)
			if err := s.UpdateExternalSource(src); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetExternalSource(src.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantClear {
				if got.CachedBody != "" || got.CachedProxyCount != 0 || got.ContentType != "" ||
					got.LastFetchUnix != 0 || got.LastSuccessUnix != 0 || got.LastError != "" {
					t.Fatalf("fetch input change retained cache: %+v", got)
				}
				return
			}
			if got.CachedBody != "proxies: []" || got.CachedProxyCount != 4 ||
				got.ContentType != "clash_yaml" || got.LastFetchUnix != 10 ||
				got.LastSuccessUnix != 9 || got.LastError != "cached warning" {
				t.Fatalf("metadata-only change cleared cache: %+v", got)
			}
		})
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

func TestNodePublicAddress(t *testing.T) {
	s := openTestStore(t)
	n := &Node{
		Name:          "nat",
		Address:       "10.0.0.8",
		GRPCPort:      50051,
		PublicAddress: "edge.example.com",
	}
	if err := s.CreateNode(n); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	got, err := s.GetNode(n.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got.PublicAddress != "edge.example.com" {
		t.Fatalf("PublicAddress = %q", got.PublicAddress)
	}
	if got.Address != "10.0.0.8" {
		t.Fatalf("Address = %q", got.Address)
	}
	n.PublicAddress = ""
	if err := s.UpdateNode(n); err != nil {
		t.Fatalf("UpdateNode clear: %v", err)
	}
	got, err = s.GetNode(n.ID)
	if err != nil {
		t.Fatalf("GetNode after clear: %v", err)
	}
	if got.PublicAddress != "" {
		t.Fatalf("expected empty PublicAddress, got %q", got.PublicAddress)
	}
}

func TestNodePortMappings(t *testing.T) {
	s := openTestStore(t)
	n := &Node{
		Name:          "nat-map",
		Address:       "10.0.0.8",
		GRPCPort:      50051,
		PublicAddress: "203.0.113.9",
		PortMappings: []PortMapping{
			{ListenPort: 8443, PublicPort: 443},
			{ListenPort: 9000, PublicPort: 9000}, // identity dropped
			{ListenPort: -1, PublicPort: 1},      // invalid dropped
			{ListenPort: 1000, PublicPort: 2000},
			{ListenPort: 1000, PublicPort: 3000}, // last wins
		},
	}
	if err := s.CreateNode(n); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	got, err := s.GetNode(n.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if len(got.PortMappings) != 2 {
		t.Fatalf("PortMappings = %+v, want 2 entries", got.PortMappings)
	}
	if got.PortMappings[0] != (PortMapping{ListenPort: 8443, PublicPort: 443}) {
		t.Fatalf("first = %+v", got.PortMappings[0])
	}
	if got.PortMappings[1] != (PortMapping{ListenPort: 1000, PublicPort: 3000}) {
		t.Fatalf("second = %+v", got.PortMappings[1])
	}
	if MapPublicPort(got.PortMappings, 8443) != 443 {
		t.Fatalf("map 8443")
	}
	if MapPublicPort(got.PortMappings, 5555) != 5555 {
		t.Fatalf("fallback")
	}

	// Clear mappings
	n.PortMappings = nil
	if err := s.UpdateNode(n); err != nil {
		t.Fatalf("UpdateNode clear: %v", err)
	}
	got, err = s.GetNode(n.ID)
	if err != nil {
		t.Fatalf("GetNode clear: %v", err)
	}
	if len(got.PortMappings) != 0 {
		t.Fatalf("expected empty mappings, got %+v", got.PortMappings)
	}
}

func TestNormalizePortMappings(t *testing.T) {
	in := []PortMapping{
		{ListenPort: 1, PublicPort: 2},
		{ListenPort: 3, PublicPort: 3},
		{ListenPort: 0, PublicPort: 9},
	}
	out := NormalizePortMappings(in)
	if len(out) != 1 || out[0].ListenPort != 1 || out[0].PublicPort != 2 {
		t.Fatalf("%+v", out)
	}
}

func TestNodeInboundNATBindings(t *testing.T) {
	s := openTestStore(t)
	n := &Node{Name: "nat", Address: "10.0.0.8", GRPCPort: 50051, PublicAddress: "node.example.com"}
	if err := s.CreateNode(n); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	in1 := &InboundConfig{Name: "ss1", Protocol: "shadowsocks", Enabled: true, Params: map[string]any{"port": float64(8443), "method": "aes-128-gcm", "password": "p"}}
	in2 := &InboundConfig{Name: "ss2", Protocol: "shadowsocks", Enabled: true, Params: map[string]any{"port": float64(9000), "method": "aes-128-gcm", "password": "p"}}
	if err := s.CreateInbound(in1); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateInbound(in2); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeInboundBindings(n.ID, []NodeInboundBinding{
		{InboundID: in1.ID, PublicAddress: "edge.example.com", PublicPort: 443},
		{InboundID: in2.ID, PublicAddress: "", PublicPort: 0},
	}); err != nil {
		t.Fatalf("SetNodeInboundBindings: %v", err)
	}
	atts, err := s.ListNodeInboundAttachments(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 2 {
		t.Fatalf("len=%d", len(atts))
	}
	byID := map[string]NodeInboundAttachment{}
	for _, a := range atts {
		byID[a.ID] = a
	}
	if byID[in1.ID].PublicAddress != "edge.example.com" || byID[in1.ID].PublicPort != 443 {
		t.Fatalf("in1 = %+v", byID[in1.ID])
	}
	if byID[in2.ID].PublicAddress != "" || byID[in2.ID].PublicPort != 0 {
		t.Fatalf("in2 = %+v", byID[in2.ID])
	}
	// compat list still works
	ins, err := s.ListInboundsForNode(n.ID)
	if err != nil || len(ins) != 2 {
		t.Fatalf("ListInboundsForNode: %v %#v", err, ins)
	}
}
