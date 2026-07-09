package nodeclient_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/labberairport/panel/internal/nodeclient"
	"github.com/labberairport/pkg/auth"
	agentv1 "github.com/labberairport/proto/gen/go/agent/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024
const testToken = "secret"

// fakeAgent mirrors the agent control surface for bufconn tests without importing
// agent/internal (cross-module internal packages are not importable).
type fakeAgent struct {
	agentv1.UnimplementedAgentControlServer

	mu            sync.Mutex
	state         string
	configJSON    string
	configHash    string
	startedAtUnix int64
	connections   int64
	uplink        int64
	downlink      int64
}

func (f *fakeAgent) Ping(context.Context, *agentv1.PingRequest) (*agentv1.PingResponse, error) {
	return &agentv1.PingResponse{
		AgentVersion:   "0.1.0-test",
		SingboxVersion: "sing-box-test",
	}, nil
}

func (f *fakeAgent) ApplyConfig(_ context.Context, req *agentv1.ApplyConfigRequest) (*agentv1.ApplyConfigResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.configJSON = req.GetConfigJson()
	f.configHash = req.GetConfigHash()
	f.state = "running"
	f.startedAtUnix = time.Now().Unix()
	return &agentv1.ApplyConfigResponse{
		Ok:          true,
		Message:     "applied",
		AppliedHash: req.GetConfigHash(),
	}, nil
}

func (f *fakeAgent) Start(context.Context, *agentv1.StartRequest) (*agentv1.StartResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = "running"
	if f.startedAtUnix == 0 {
		f.startedAtUnix = time.Now().Unix()
	}
	return &agentv1.StartResponse{Ok: true, Message: "started"}, nil
}

func (f *fakeAgent) Stop(context.Context, *agentv1.StopRequest) (*agentv1.StopResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = "stopped"
	return &agentv1.StopResponse{Ok: true, Message: "stopped"}, nil
}

func (f *fakeAgent) GetStatus(context.Context, *agentv1.GetStatusRequest) (*agentv1.GetStatusResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &agentv1.GetStatusResponse{
		State:         f.state,
		ConfigHash:    f.configHash,
		StartedAtUnix: f.startedAtUnix,
	}, nil
}

func (f *fakeAgent) GetMetrics(context.Context, *agentv1.GetMetricsRequest) (*agentv1.GetMetricsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &agentv1.GetMetricsResponse{
		Connections:   f.connections,
		UplinkBytes:   f.uplink,
		DownlinkBytes: f.downlink,
		CpuPercent:    1.5,
	}, nil
}

func startBufServer(t *testing.T, token string, srv agentv1.AgentControlServer) (*bufconn.Listener, func()) {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	s := grpc.NewServer(
		grpc.UnaryInterceptor(auth.UnaryServerInterceptor(token)),
		grpc.StreamInterceptor(auth.StreamServerInterceptor(token)),
	)
	agentv1.RegisterAgentControlServer(s, srv)
	go func() {
		_ = s.Serve(lis)
	}()
	return lis, func() {
		s.Stop()
		_ = lis.Close()
	}
}

func dialBuf(t *testing.T, lis *bufconn.Listener, token string) *nodeclient.Client {
	t.Helper()
	client, err := nodeclient.Dial(context.Background(), nodeclient.DialConfig{
		Address: "passthrough:///bufnet",
		Token:   token,
		Timeout: 5 * time.Second,
		Dialer: func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		},
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestPingAndApplyConfig(t *testing.T) {
	fa := &fakeAgent{state: "stopped"}
	lis, cleanup := startBufServer(t, testToken, fa)
	defer cleanup()

	client := dialBuf(t, lis, testToken)

	ping, err := client.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if ping.AgentVersion != "0.1.0-test" || ping.SingboxVersion != "sing-box-test" {
		t.Fatalf("unexpected ping: %+v", ping)
	}

	cfgJSON := `{"log":{"level":"info"},"inbounds":[],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`
	resp, err := client.ApplyConfig(context.Background(), cfgJSON, "hash-abc", true)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if !resp.Ok || resp.AppliedHash != "hash-abc" {
		t.Fatalf("ApplyConfig resp: %+v", resp)
	}

	st, err := client.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if st.State != "running" || st.ConfigHash != "hash-abc" {
		t.Fatalf("status: %+v", st)
	}
}

func TestAuthRequired(t *testing.T) {
	fa := &fakeAgent{state: "stopped"}
	lis, cleanup := startBufServer(t, testToken, fa)
	defer cleanup()

	// Wrong token must fail.
	bad := dialBuf(t, lis, "wrong")
	_, err := bad.Ping(context.Background())
	if err == nil {
		t.Fatal("expected unauthenticated with wrong token")
	}

	// Empty token must fail.
	empty, err := nodeclient.Dial(context.Background(), nodeclient.DialConfig{
		Address: "passthrough:///bufnet",
		Token:   "",
		Dialer: func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		},
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer empty.Close()
	_, err = empty.Ping(context.Background())
	if err == nil {
		t.Fatal("expected unauthenticated with empty token")
	}
}

func TestStartStopMetrics(t *testing.T) {
	fa := &fakeAgent{
		state:       "stopped",
		connections: 3,
		uplink:      100,
		downlink:    200,
	}
	lis, cleanup := startBufServer(t, testToken, fa)
	defer cleanup()

	client := dialBuf(t, lis, testToken)

	startResp, err := client.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !startResp.Ok {
		t.Fatalf("Start not ok: %+v", startResp)
	}

	m, err := client.GetMetrics(context.Background())
	if err != nil {
		t.Fatalf("GetMetrics: %v", err)
	}
	if m.Connections != 3 || m.UplinkBytes != 100 || m.DownlinkBytes != 200 {
		t.Fatalf("metrics: %+v", m)
	}

	stopResp, err := client.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !stopResp.Ok {
		t.Fatalf("Stop not ok: %+v", stopResp)
	}

	st, err := client.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if st.State != "stopped" {
		t.Fatalf("expected stopped, got %s", st.State)
	}
}

func TestDialRequiresAddress(t *testing.T) {
	_, err := nodeclient.Dial(context.Background(), nodeclient.DialConfig{})
	if err == nil {
		t.Fatal("expected error for empty address")
	}
}
