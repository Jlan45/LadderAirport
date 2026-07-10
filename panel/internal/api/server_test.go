package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ladderairport/panel/internal/api"
	"github.com/ladderairport/panel/internal/batch"
	"github.com/ladderairport/panel/internal/store"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
	"google.golang.org/grpc/metadata"
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

type mockRPC struct {
	mu      sync.Mutex
	calls   []string
	applyOK bool
	startOK bool
	stopOK  bool
}

func (m *mockRPC) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "close")
	return nil
}

func (m *mockRPC) ApplyConfig(_ context.Context, _, _ string, _ bool) (*agentv1.ApplyConfigResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "apply")
	if !m.applyOK {
		return &agentv1.ApplyConfigResponse{Ok: false, Message: "apply failed"}, nil
	}
	return &agentv1.ApplyConfigResponse{Ok: true, Message: "applied", AppliedHash: "h"}, nil
}

func (m *mockRPC) Start(_ context.Context) (*agentv1.StartResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "start")
	if !m.startOK {
		return &agentv1.StartResponse{Ok: false, Message: "start failed"}, nil
	}
	return &agentv1.StartResponse{Ok: true, Message: "started"}, nil
}

func (m *mockRPC) Stop(_ context.Context) (*agentv1.StopResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "stop")
	if !m.stopOK {
		return &agentv1.StopResponse{Ok: false, Message: "stop failed"}, nil
	}
	return &agentv1.StopResponse{Ok: true, Message: "stopped"}, nil
}

type mockLive struct {
	pingOK    bool
	metricsOK bool
}

func (m *mockLive) Close() error { return nil }

func (m *mockLive) Ping(_ context.Context) (*agentv1.PingResponse, error) {
	if !m.pingOK {
		return nil, context.DeadlineExceeded
	}
	return &agentv1.PingResponse{AgentVersion: "test-agent", SingboxVersion: "1.0"}, nil
}

func (m *mockLive) GetStatus(_ context.Context) (*agentv1.GetStatusResponse, error) {
	if !m.pingOK {
		return nil, context.DeadlineExceeded
	}
	return &agentv1.GetStatusResponse{State: "running", ConfigHash: "abc"}, nil
}

func (m *mockLive) GetMetrics(_ context.Context) (*agentv1.GetMetricsResponse, error) {
	if !m.metricsOK {
		return nil, context.DeadlineExceeded
	}
	return &agentv1.GetMetricsResponse{
		Connections:     3,
		UplinkBytes:     100,
		DownlinkBytes:   200,
		CpuPercent:      1.5,
		MemoryRssBytes:  4096,
	}, nil
}

// logStream is a minimal StreamLogs client for SSE tests.
type logStream struct {
	lines []*agentv1.LogLine
	idx   int
}

func (s *logStream) Recv() (*agentv1.LogLine, error) {
	if s.idx >= len(s.lines) {
		return nil, io.EOF
	}
	line := s.lines[s.idx]
	s.idx++
	return line, nil
}

func (s *logStream) Header() (metadata.MD, error) { return nil, nil }
func (s *logStream) Trailer() metadata.MD         { return nil }
func (s *logStream) CloseSend() error             { return nil }
func (s *logStream) Context() context.Context     { return context.Background() }
func (s *logStream) SendMsg(any) error            { return nil }
func (s *logStream) RecvMsg(any) error            { return io.EOF }

func (m *mockLive) StreamLogs(_ context.Context, _ string, _ int32) (agentv1.AgentControl_StreamLogsClient, error) {
	return &logStream{lines: []*agentv1.LogLine{
		{TsUnixMs: 1, Level: "info", Message: "hello"},
	}}, nil
}

func newTestServer(t *testing.T, dial batch.DialFunc, live api.LiveDialFunc) (*httptest.Server, *http.Client, *store.Store) {
	t.Helper()
	st := openTestStore(t)
	if err := api.EnsureAdminPassword(st); err != nil {
		t.Fatalf("EnsureAdminPassword: %v", err)
	}

	runner := batch.NewRunner(st, func() string {
		s, _ := st.GetSettings()
		if s == nil {
			return ""
		}
		return s.DefaultAgentToken
	})
	if dial != nil {
		runner.Dial = dial
	}

	srv := &api.Server{
		Store:  st,
		Runner: runner,
		Secret: []byte("test-session-secret-at-least-32b"),
		Dial:   live,
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{Jar: jar}
	return ts, client, st
}

func login(t *testing.T, client *http.Client, baseURL, password string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"password": password})
	resp, err := client.Post(baseURL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return resp
}

func doJSON(t *testing.T, client *http.Client, method, url string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(raw) == 0 {
		return resp, nil
	}
	var out map[string]any
	// May be array — try object first.
	if err := json.Unmarshal(raw, &out); err != nil {
		var arr []any
		if err2 := json.Unmarshal(raw, &arr); err2 != nil {
			t.Fatalf("decode response %q: %v", string(raw), err)
		}
		return resp, map[string]any{"_array": arr}
	}
	return resp, out
}

func TestAuthRequired(t *testing.T) {
	ts, client, _ := newTestServer(t, nil, nil)

	resp, err := client.Get(ts.URL + "/api/v1/nodes")
	if err != nil {
		t.Fatalf("GET nodes: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	ts, client, _ := newTestServer(t, nil, nil)
	resp := login(t, client, ts.URL, "wrong")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestFleetFlow(t *testing.T) {
	rpc := &mockRPC{applyOK: true, startOK: true, stopOK: true}
	live := &mockLive{pingOK: true, metricsOK: true}

	ts, client, _ := newTestServer(t,
		func(_ context.Context, n store.Node, _ string) (batch.NodeRPC, error) {
			return rpc, nil
		},
		func(_ context.Context, n store.Node, _ string) (api.NodeLive, error) {
			return live, nil
		},
	)

	// Login with default password.
	resp := login(t, client, ts.URL, "admin")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("login status = %d body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	// Create node.
	resp, nodeObj := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/nodes", map[string]any{
		"name":      "edge-1",
		"address":   "10.0.0.1",
		"grpc_port": 50051,
		"labels":    []string{"edge"},
		"token":     "tok",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create node status = %d body=%v", resp.StatusCode, nodeObj)
	}
	nodeID, _ := nodeObj["id"].(string)
	if nodeID == "" {
		t.Fatalf("missing node id: %v", nodeObj)
	}

	// Set public base URL so install command includes auto-enroll.
	resp, _ = doJSON(t, client, http.MethodPut, ts.URL+"/api/v1/settings", map[string]any{
		"public_base_url": ts.URL,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put settings for public_base_url status = %d", resp.StatusCode)
	}

	// Bootstrap node with install command.
	resp, boot := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/nodes/bootstrap", map[string]any{
		"name": "edge-boot", "grpc_port": 50051,
		"enable_tls": true,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap status = %d body=%v", resp.StatusCode, boot)
	}
	cmd, _ := boot["install_command"].(string)
	if !strings.Contains(cmd, "LADDER_TOKEN=") || !strings.Contains(cmd, "LADDER_TLS=1") {
		t.Fatalf("install_command = %q", cmd)
	}
	if !strings.Contains(cmd, "LADDER_PANEL=") {
		t.Fatalf("expected LADDER_PANEL in command: %q", cmd)
	}
	if boot["enroll_enabled"] != true {
		t.Fatalf("enroll_enabled = %v", boot["enroll_enabled"])
	}
	bootNode, _ := boot["node"].(map[string]any)
	bootID, _ := bootNode["id"].(string)
	bootToken, _ := boot["token"].(string)
	if bootID == "" || bootToken == "" {
		t.Fatalf("bootstrap node id/token missing: %v", boot)
	}
	resp, inst := doJSON(t, client, http.MethodGet, ts.URL+"/api/v1/nodes/"+bootID+"/install-command", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("install-command status = %d", resp.StatusCode)
	}
	if _, ok := inst["install_command"].(string); !ok {
		t.Fatalf("install-command body = %v", inst)
	}

	// Agent enroll without admin session (new client, no cookies).
	enrollClient := &http.Client{Timeout: 10 * time.Second}
	enrollBody := map[string]any{
		"token": bootToken, "node_id": bootID,
		"address": "203.0.113.50", "grpc_port": 50051,
		"ca_cert_pem": "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n",
		"tls_enabled": true,
	}
	raw, _ := json.Marshal(enrollBody)
	ereq, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/agent/enroll", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	ereq.Header.Set("Content-Type", "application/json")
	ereq.Header.Set("Authorization", "Bearer "+bootToken)
	eresp, err := enrollClient.Do(ereq)
	if err != nil {
		t.Fatal(err)
	}
	defer eresp.Body.Close()
	if eresp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(eresp.Body)
		t.Fatalf("enroll status = %d body=%s", eresp.StatusCode, b)
	}
	// Admin list should show updated address/CA.
	resp, nodesList := doJSON(t, client, http.MethodGet, ts.URL+"/api/v1/nodes", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list nodes after enroll: %d", resp.StatusCode)
	}
	// nodes may be array directly or wrapped — doJSON returns map for objects; arrays?
	// list returns JSON array — doJSON might fail. Check existing patterns.
	_ = nodesList
	// Fetch via install-command node re-get by updating — use probe path not needed.
	// Get node by re-bootstrap list: call GET node not exist — use store via bootstrap node fields after enroll.
	// Re-get install-command which embeds node
	resp, inst2 := doJSON(t, client, http.MethodGet, ts.URL+"/api/v1/nodes/"+bootID+"/install-command", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("install-command after enroll: %d", resp.StatusCode)
	}
	node2, _ := inst2["node"].(map[string]any)
	if node2["address"] != "203.0.113.50" {
		t.Fatalf("address after enroll = %v", node2["address"])
	}
	ca, _ := node2["ca_cert_pem"].(string)
	if !strings.Contains(ca, "BEGIN CERTIFICATE") {
		t.Fatalf("ca after enroll = %q", ca)
	}

	// Create inbound (shadowsocks).
	resp, inObj := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/inbounds", map[string]any{
		"name":     "ss-main",
		"protocol": "shadowsocks",
		"enabled":  true,
		"params": map[string]any{
			"listen":   "0.0.0.0",
			"port":     8388,
			"method":   "aes-128-gcm",
			"password": "password123",
		},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create inbound status = %d body=%v", resp.StatusCode, inObj)
	}
	inboundID, _ := inObj["id"].(string)
	if inboundID == "" {
		t.Fatalf("missing inbound id: %v", inObj)
	}

	// Attach inbound to node.
	resp, _ = doJSON(t, client, http.MethodPut, ts.URL+"/api/v1/nodes/"+nodeID+"/inbounds", map[string]any{
		"inbound_ids": []string{inboundID},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("attach status = %d", resp.StatusCode)
	}

	// Preview config.
	resp, preview := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/nodes/"+nodeID+"/config/preview", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("preview status = %d body=%v", resp.StatusCode, preview)
	}
	inbounds, ok := preview["inbounds"].([]any)
	if !ok || len(inbounds) != 1 {
		t.Fatalf("preview inbounds = %v", preview["inbounds"])
	}

	// Templates list.
	resp, tmpl := doJSON(t, client, http.MethodGet, ts.URL+"/api/v1/templates", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("templates status = %d", resp.StatusCode)
	}
	arr, _ := tmpl["_array"].([]any)
	if len(arr) < 7 {
		t.Fatalf("expected >=7 templates, got %v", tmpl)
	}

	// Unknown protocol rejected.
	resp, badIn := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/inbounds", map[string]any{
		"name": "nope", "protocol": "not-a-protocol", "enabled": true,
		"params": map[string]any{"port": 1},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown protocol status = %d body=%v", resp.StatusCode, badIn)
	}

	// Probe.
	resp, probe := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/nodes/"+nodeID+"/probe", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("probe status = %d body=%v", resp.StatusCode, probe)
	}
	if probe["agent_version"] != "test-agent" {
		t.Fatalf("probe agent_version = %v", probe["agent_version"])
	}

	// Metrics.
	resp, metrics := doJSON(t, client, http.MethodGet, ts.URL+"/api/v1/nodes/"+nodeID+"/metrics", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d body=%v", resp.StatusCode, metrics)
	}
	if metrics["connections"] != float64(3) {
		t.Fatalf("connections = %v", metrics["connections"])
	}

	// Batch apply via injectable runner dial.
	resp, taskObj := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/batch/apply", map[string]any{
		"node_ids": []string{nodeID},
		"labels":   []string{},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("batch apply status = %d body=%v", resp.StatusCode, taskObj)
	}
	if taskObj["status"] != "success" {
		t.Fatalf("task status = %v results=%v", taskObj["status"], taskObj["results"])
	}
	if taskObj["type"] != "apply" {
		t.Fatalf("task type = %v", taskObj["type"])
	}

	// Get task by id.
	taskID, _ := taskObj["id"].(string)
	resp, got := doJSON(t, client, http.MethodGet, ts.URL+"/api/v1/tasks/"+taskID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get task status = %d", resp.StatusCode)
	}
	if got["id"] != taskID {
		t.Fatalf("get task id = %v", got["id"])
	}

	// Settings GET/PUT.
	resp, settings := doJSON(t, client, http.MethodGet, ts.URL+"/api/v1/settings", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get settings status = %d", resp.StatusCode)
	}
	if _, hasHash := settings["admin_password_hash"]; hasHash {
		t.Fatal("admin_password_hash must not be exposed in JSON")
	}

	resp, settings = doJSON(t, client, http.MethodPut, ts.URL+"/api/v1/settings", map[string]any{
		"default_agent_token": "new-token",
		"grpc_timeout_sec":    15,
		"max_concurrency":     5,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put settings status = %d body=%v", resp.StatusCode, settings)
	}
	if settings["default_agent_token"] != "new-token" {
		t.Fatalf("token = %v", settings["default_agent_token"])
	}
	if settings["grpc_timeout_sec"] != float64(15) {
		t.Fatalf("timeout = %v", settings["grpc_timeout_sec"])
	}
}

func TestSingleNodeApply(t *testing.T) {
	rpc := &mockRPC{applyOK: true}
	ts, client, st := newTestServer(t,
		func(_ context.Context, _ store.Node, _ string) (batch.NodeRPC, error) {
			return rpc, nil
		},
		nil,
	)

	resp := login(t, client, ts.URL, "admin")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %d", resp.StatusCode)
	}

	n := &store.Node{Name: "n1", Address: "127.0.0.1", GRPCPort: 50051, Token: "t"}
	if err := st.CreateNode(n); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	in := &store.InboundConfig{
		Name: "ss", Protocol: "shadowsocks", Enabled: true,
		Params: map[string]any{"listen": "0.0.0.0", "port": 1000, "method": "aes-128-gcm", "password": "x"},
	}
	if err := st.CreateInbound(in); err != nil {
		t.Fatalf("CreateInbound: %v", err)
	}
	if err := st.SetNodeInbounds(n.ID, []string{in.ID}); err != nil {
		t.Fatalf("SetNodeInbounds: %v", err)
	}

	resp, task := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/nodes/"+n.ID+"/apply", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("apply status = %d body=%v", resp.StatusCode, task)
	}
	if task["status"] != "success" {
		t.Fatalf("status = %v", task)
	}
}

func TestBatchByLabels(t *testing.T) {
	rpc := &mockRPC{startOK: true, applyOK: true}
	ts, client, st := newTestServer(t,
		func(_ context.Context, _ store.Node, _ string) (batch.NodeRPC, error) {
			return rpc, nil
		},
		nil,
	)

	resp := login(t, client, ts.URL, "admin")
	resp.Body.Close()

	n1 := &store.Node{Name: "a", Address: "1.1.1.1", GRPCPort: 1, Labels: []string{"prod"}, Token: "t"}
	n2 := &store.Node{Name: "b", Address: "2.2.2.2", GRPCPort: 1, Labels: []string{"dev"}, Token: "t"}
	n3 := &store.Node{Name: "c", Address: "3.3.3.3", GRPCPort: 1, Labels: []string{"prod"}, Token: "t"}
	if err := st.CreateNode(n1); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateNode(n2); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateNode(n3); err != nil {
		t.Fatal(err)
	}

	// Start only matches the prod-labeled subset (n1).
	resp, task := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/batch/start", map[string]any{
		"node_ids": []string{},
		"labels":   []string{"prod"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("batch start status = %d body=%v", resp.StatusCode, task)
	}
	ids, _ := task["node_ids"].([]any)
	if len(ids) != 2 {
		t.Fatalf("node_ids = %v, want 2 (both prod)", ids)
	}
	if task["status"] != "success" {
		t.Fatalf("status = %v", task["status"])
	}

	// Batch apply by shared label should target both prod nodes with per-node results.
	in := &store.InboundConfig{
		Name: "ss-batch", Protocol: "shadowsocks", Enabled: true,
		Params: map[string]any{"listen": "0.0.0.0", "port": 2000, "method": "aes-128-gcm", "password": "x"},
	}
	if err := st.CreateInbound(in); err != nil {
		t.Fatalf("CreateInbound: %v", err)
	}
	if err := st.SetNodeInbounds(n1.ID, []string{in.ID}); err != nil {
		t.Fatalf("SetNodeInbounds n1: %v", err)
	}
	if err := st.SetNodeInbounds(n3.ID, []string{in.ID}); err != nil {
		t.Fatalf("SetNodeInbounds n3: %v", err)
	}

	resp, task = doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/batch/apply", map[string]any{
		"node_ids": []string{},
		"labels":   []string{"prod"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("batch apply status = %d body=%v", resp.StatusCode, task)
	}
	if task["status"] != "success" {
		t.Fatalf("batch apply status = %v results=%v", task["status"], task["results"])
	}
	ids, _ = task["node_ids"].([]any)
	if len(ids) != 2 {
		t.Fatalf("batch apply node_ids = %v, want 2", ids)
	}
	results, _ := task["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("batch apply results = %v, want 2", results)
	}
}
