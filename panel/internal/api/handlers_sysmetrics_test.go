package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/ladderairport/panel/internal/api"
	"github.com/ladderairport/panel/internal/store"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type sysMockLive struct {
	*mockLive
	unimplemented bool
	lastBBRSet    *bool
}

func (m *sysMockLive) GetNodeMetrics(context.Context) (*agentv1.GetNodeMetricsResponse, error) {
	if m.unimplemented {
		return nil, status.Error(codes.Unimplemented, "unknown method GetNodeMetrics")
	}
	return &agentv1.GetNodeMetricsResponse{
		CpuPercent:       12.5,
		MemoryTotalBytes: 1024,
		MemoryUsedBytes:  512,
		DiskTotalBytes:   4096,
		DiskUsedBytes:    2048,
		UplinkBps:        1000,
		DownlinkBps:      2000,
		CollectedAtUnix:  1700000000,
	}, nil
}

func (m *sysMockLive) GetBBRStatus(context.Context) (*agentv1.GetBBRStatusResponse, error) {
	if m.unimplemented {
		return nil, status.Error(codes.Unimplemented, "unknown method GetBBRStatus")
	}
	return &agentv1.GetBBRStatusResponse{
		BbrAvailable:             true,
		BbrEnabled:               true,
		CurrentCongestionControl: "bbr",
		CurrentQdisc:             "fq",
		SupportedControls:        []string{"cubic", "bbr"},
	}, nil
}

func (m *sysMockLive) SetBBR(_ context.Context, enabled bool) (*agentv1.SetBBRResponse, error) {
	if m.unimplemented {
		return nil, status.Error(codes.Unimplemented, "unknown method SetBBR")
	}
	m.lastBBRSet = &enabled
	return &agentv1.SetBBRResponse{Ok: true, Message: "bbr enabled"}, nil
}

func newSysTestNode(t *testing.T, st *store.Store, capabilities []string) *store.Node {
	t.Helper()
	node := &store.Node{
		Name: "sys-node", Address: "127.0.0.1", GRPCPort: 50051,
		Status: "online", Capabilities: capabilities,
	}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	return node
}

func TestNodeSysMetricsOK(t *testing.T) {
	live := &sysMockLive{mockLive: &mockLive{pingOK: true}}
	ts, client, st := newTestServer(t, nil, func(
		context.Context, store.Node, string,
	) (api.NodeLive, error) {
		return live, nil
	})
	resp := login(t, client, ts.URL, "admin")
	resp.Body.Close()
	node := newSysTestNode(t, st, []string{"node-metrics-v1", "bbr-v1"})

	response, payload := doJSON(
		t, client, http.MethodGet, ts.URL+"/api/v1/nodes/"+node.ID+"/sysmetrics", nil,
	)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, payload = %v", response.StatusCode, payload)
	}
	if payload["cpu_percent"] != 12.5 || payload["memory_total_bytes"] != float64(1024) ||
		payload["memory_used_bytes"] != float64(512) || payload["disk_total_bytes"] != float64(4096) ||
		payload["disk_used_bytes"] != float64(2048) || payload["uplink_bps"] != float64(1000) ||
		payload["downlink_bps"] != float64(2000) || payload["collected_at_unix"] != float64(1700000000) {
		t.Fatalf("payload = %v", payload)
	}
}

func TestNodeSysMetricsCapabilityGate(t *testing.T) {
	live := &sysMockLive{mockLive: &mockLive{pingOK: true}}
	ts, client, st := newTestServer(t, nil, func(
		context.Context, store.Node, string,
	) (api.NodeLive, error) {
		return live, nil
	})
	resp := login(t, client, ts.URL, "admin")
	resp.Body.Close()
	node := newSysTestNode(t, st, []string{"frps-v1"})

	response, payload := doJSON(
		t, client, http.MethodGet, ts.URL+"/api/v1/nodes/"+node.ID+"/sysmetrics", nil,
	)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, payload = %v", response.StatusCode, payload)
	}
	if payload["error"] != "agent 版本过旧，不支持该功能，请先升级节点" {
		t.Fatalf("payload = %v", payload)
	}

	// Unknown capabilities (legacy agent never probed) must not be blocked.
	node.Capabilities = nil
	if err := st.UpdateNode(node); err != nil {
		t.Fatal(err)
	}
	response, _ = doJSON(
		t, client, http.MethodGet, ts.URL+"/api/v1/nodes/"+node.ID+"/sysmetrics", nil,
	)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestNodeSysMetricsUnimplementedMaps400(t *testing.T) {
	live := &sysMockLive{mockLive: &mockLive{pingOK: true}, unimplemented: true}
	ts, client, st := newTestServer(t, nil, func(
		context.Context, store.Node, string,
	) (api.NodeLive, error) {
		return live, nil
	})
	resp := login(t, client, ts.URL, "admin")
	resp.Body.Close()
	node := newSysTestNode(t, st, []string{"node-metrics-v1", "bbr-v1"})

	response, payload := doJSON(
		t, client, http.MethodGet, ts.URL+"/api/v1/nodes/"+node.ID+"/sysmetrics", nil,
	)
	if response.StatusCode != http.StatusBadRequest ||
		payload["error"] != "agent 版本过旧，不支持该功能，请先升级节点" {
		t.Fatalf("status = %d, payload = %v", response.StatusCode, payload)
	}
	response, payload = doJSON(
		t, client, http.MethodGet, ts.URL+"/api/v1/nodes/"+node.ID+"/bbr", nil,
	)
	if response.StatusCode != http.StatusBadRequest ||
		payload["error"] != "agent 版本过旧，不支持该功能，请先升级节点" {
		t.Fatalf("status = %d, payload = %v", response.StatusCode, payload)
	}
}

func TestNodeBBREndpoints(t *testing.T) {
	live := &sysMockLive{mockLive: &mockLive{pingOK: true}}
	ts, client, st := newTestServer(t, nil, func(
		context.Context, store.Node, string,
	) (api.NodeLive, error) {
		return live, nil
	})
	resp := login(t, client, ts.URL, "admin")
	resp.Body.Close()
	node := newSysTestNode(t, st, []string{"bbr-v1"})

	response, payload := doJSON(
		t, client, http.MethodGet, ts.URL+"/api/v1/nodes/"+node.ID+"/bbr", nil,
	)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, payload = %v", response.StatusCode, payload)
	}
	if payload["bbr_available"] != true || payload["bbr_enabled"] != true ||
		payload["current_congestion_control"] != "bbr" || payload["current_qdisc"] != "fq" {
		t.Fatalf("payload = %v", payload)
	}
	controls, ok := payload["supported_controls"].([]any)
	if !ok || len(controls) != 2 || controls[0] != "cubic" || controls[1] != "bbr" {
		t.Fatalf("supported_controls = %v", payload["supported_controls"])
	}

	response, payload = doJSON(
		t, client, http.MethodPost, ts.URL+"/api/v1/nodes/"+node.ID+"/bbr",
		map[string]any{"enabled": true},
	)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, payload = %v", response.StatusCode, payload)
	}
	if payload["ok"] != true || payload["message"] != "bbr enabled" {
		t.Fatalf("payload = %v", payload)
	}
	if live.lastBBRSet == nil || !*live.lastBBRSet {
		t.Fatalf("agent SetBBR enabled = %v", live.lastBBRSet)
	}

	// Missing bbr-v1 capability → 400 on both verbs.
	node.Capabilities = []string{"node-metrics-v1"}
	if err := st.UpdateNode(node); err != nil {
		t.Fatal(err)
	}
	response, _ = doJSON(
		t, client, http.MethodGet, ts.URL+"/api/v1/nodes/"+node.ID+"/bbr", nil,
	)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", response.StatusCode)
	}
	response, _ = doJSON(
		t, client, http.MethodPost, ts.URL+"/api/v1/nodes/"+node.ID+"/bbr",
		map[string]any{"enabled": false},
	)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", response.StatusCode)
	}
}
