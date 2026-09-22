package uplink

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
)

func TestNewRequiresIdentity(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected empty config to fail")
	}
}

func TestPostJSONUsesBearer(t *testing.T) {
	var gotAuth, gotPath string
	var body map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	c, err := New(Config{PanelURL: ts.URL, NodeID: "n1", Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.postJSON(context.Background(), "/api/v1/agent/report", map[string]string{"node_id": "n1"})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("body = %s", raw)
	}
	if gotAuth != "Bearer secret" || gotPath != "/api/v1/agent/report" {
		t.Fatalf("auth=%q path=%q", gotAuth, gotPath)
	}
	if body["node_id"] != "n1" {
		t.Fatalf("payload = %v", body)
	}
}

func TestPostJSONRejectsHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "节点令牌无效", http.StatusUnauthorized)
	}))
	defer ts.Close()
	c, err := New(Config{PanelURL: ts.URL, NodeID: "n1", Token: "bad"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.postJSON(context.Background(), "/api/v1/agent/report", map[string]string{}); err == nil {
		t.Fatal("expected unauthorized error")
	}
}

func TestApplySyncClearsRemovedFRPSHash(t *testing.T) {
	rt := &stubRuntime{state: "running"}
	c, err := New(Config{PanelURL: "http://127.0.0.1", NodeID: "n1", Token: "t", Control: rt})
	if err != nil {
		t.Fatal(err)
	}
	c.frpsHash = "old-frps"
	if err := c.applySync(context.Background(), syncResponse{Changed: true, DesiredState: "running"}); err != nil {
		t.Fatal(err)
	}
	if c.frpsHash != "" {
		t.Fatalf("frpsHash = %q, want empty", c.frpsHash)
	}
}

type stubRuntime struct {
	state string
}

func (s *stubRuntime) GetStatus(context.Context, *agentv1.GetStatusRequest) (*agentv1.GetStatusResponse, error) {
	return &agentv1.GetStatusResponse{State: s.state}, nil
}
func (s *stubRuntime) GetMetrics(context.Context, *agentv1.GetMetricsRequest) (*agentv1.GetMetricsResponse, error) {
	return &agentv1.GetMetricsResponse{}, nil
}
func (s *stubRuntime) Ping(context.Context, *agentv1.PingRequest) (*agentv1.PingResponse, error) {
	return &agentv1.PingResponse{}, nil
}
func (s *stubRuntime) ApplyConfig(context.Context, *agentv1.ApplyConfigRequest) (*agentv1.ApplyConfigResponse, error) {
	return &agentv1.ApplyConfigResponse{Ok: true}, nil
}
func (s *stubRuntime) ApplyFRPServerConfig(context.Context, *agentv1.ApplyFRPServerConfigRequest) (*agentv1.ApplyFRPServerConfigResponse, error) {
	return &agentv1.ApplyFRPServerConfigResponse{Ok: true}, nil
}
func (s *stubRuntime) Start(context.Context, *agentv1.StartRequest) (*agentv1.StartResponse, error) {
	s.state = "running"
	return &agentv1.StartResponse{Ok: true}, nil
}
func (s *stubRuntime) Stop(context.Context, *agentv1.StopRequest) (*agentv1.StopResponse, error) {
	s.state = "stopped"
	return &agentv1.StopResponse{Ok: true}, nil
}
func (s *stubRuntime) ProbeOutbound(context.Context, *agentv1.ProbeOutboundRequest) (*agentv1.ProbeOutboundResponse, error) {
	return &agentv1.ProbeOutboundResponse{Ok: true}, nil
}
func (s *stubRuntime) ListInterfaces(context.Context, *agentv1.ListInterfacesRequest) (*agentv1.ListInterfacesResponse, error) {
	return &agentv1.ListInterfacesResponse{}, nil
}
func (s *stubRuntime) UpgradeAgent(context.Context, *agentv1.UpgradeAgentRequest) (*agentv1.UpgradeAgentResponse, error) {
	return &agentv1.UpgradeAgentResponse{Ok: true}, nil
}
func (s *stubRuntime) GetNodeMetrics(context.Context, *agentv1.GetNodeMetricsRequest) (*agentv1.GetNodeMetricsResponse, error) {
	return &agentv1.GetNodeMetricsResponse{}, nil
}
func (s *stubRuntime) GetBBRStatus(context.Context, *agentv1.GetBBRStatusRequest) (*agentv1.GetBBRStatusResponse, error) {
	return &agentv1.GetBBRStatusResponse{}, nil
}
func (s *stubRuntime) SetBBR(context.Context, *agentv1.SetBBRRequest) (*agentv1.SetBBRResponse, error) {
	return &agentv1.SetBBRResponse{Ok: true}, nil
}
func (s *stubRuntime) GetFRPServerMappings(context.Context, *agentv1.GetFRPServerMappingsRequest) (*agentv1.GetFRPServerMappingsResponse, error) {
	return &agentv1.GetFRPServerMappingsResponse{}, nil
}
func (s *stubRuntime) StartFRPServer(context.Context, *agentv1.StartFRPServerRequest) (*agentv1.StartFRPServerResponse, error) {
	return &agentv1.StartFRPServerResponse{Ok: true}, nil
}
func (s *stubRuntime) StopFRPServer(context.Context, *agentv1.StopFRPServerRequest) (*agentv1.StopFRPServerResponse, error) {
	return &agentv1.StopFRPServerResponse{Ok: true}, nil
}
func (s *stubRuntime) GetFRPServerStatus(context.Context, *agentv1.GetFRPServerStatusRequest) (*agentv1.GetFRPServerStatusResponse, error) {
	return &agentv1.GetFRPServerStatusResponse{State: s.state}, nil
}

func TestHeadConfigReadsHashHeaders(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead || r.URL.Path != "/api/v1/agent/config-sync" {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Query().Get("node_id") != "n1" || r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set(headerConfigHash, "cfg-hash")
		w.Header().Set(headerFRPSHash, "frps-hash")
		w.Header().Set(headerDesiredState, "stopped")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	c, err := New(Config{PanelURL: ts.URL, NodeID: "n1", Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	head, err := c.headConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if head.ConfigHash != "cfg-hash" || head.FRPSHash != "frps-hash" || head.DesiredState != "stopped" {
		t.Fatalf("head = %+v", head)
	}
}

func TestNoteSyncFailureBackoff(t *testing.T) {
	c, err := New(Config{PanelURL: "http://127.0.0.1", NodeID: "n1", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	c.noteSyncFailure()
	if c.failStreak != 1 || c.nextConfigTry.Before(time.Now()) {
		t.Fatalf("streak=%d next=%v", c.failStreak, c.nextConfigTry)
	}
}
