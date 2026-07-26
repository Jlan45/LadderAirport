package control_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/ladderairport/agent/internal/control"
	"github.com/ladderairport/pkg/auth"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

// stubRuntime is a minimal Runtime for gRPC server unit tests (not a product mock core).
type stubRuntime struct {
	mu         sync.Mutex
	state      control.State
	configHash string
	startedAt  int64
	probeTag   string
	probeURL   string
}

func (s *stubRuntime) Apply(_ context.Context, _ string, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configHash = hash
	s.state = control.StateRunning
	s.startedAt = time.Now().Unix()
	return nil
}

func (s *stubRuntime) Start(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = control.StateRunning
	if s.startedAt == 0 {
		s.startedAt = time.Now().Unix()
	}
	return nil
}

func (s *stubRuntime) Stop(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = control.StateStopped
	return nil
}

func (s *stubRuntime) Status(context.Context) control.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return control.Status{State: s.state, ConfigHash: s.configHash, StartedAtUnix: s.startedAt}
}

func (s *stubRuntime) Metrics(context.Context) control.Metrics {
	return control.Metrics{}
}

func (s *stubRuntime) ProbeOutbound(_ context.Context, tag, targetURL string) (uint32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probeTag = tag
	s.probeURL = targetURL
	return 1, nil
}

func startTestServer(t *testing.T, token string, rt control.Runtime) (agentv1.AgentControlClient, func()) {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	s := grpc.NewServer(
		grpc.UnaryInterceptor(auth.UnaryServerInterceptor(token)),
		grpc.StreamInterceptor(auth.StreamServerInterceptor(token)),
	)
	agentv1.RegisterAgentControlServer(s, control.NewServer(rt, "0.1.0-test", "sing-box-test", nil))
	go func() {
		_ = s.Serve(lis)
	}()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	return agentv1.NewAgentControlClient(conn), func() {
		_ = conn.Close()
		s.Stop()
	}
}

func TestApplyConfigRequiresToken(t *testing.T) {
	rt := &stubRuntime{state: control.StateStopped}
	client, cleanup := startTestServer(t, "secret", rt)
	defer cleanup()
	_, err := client.ApplyConfig(context.Background(), &agentv1.ApplyConfigRequest{
		ConfigJson: `{"inbounds":[],"outbounds":[{"type":"direct","tag":"direct"}]}`,
		ConfigHash: "abc",
		Replace:    true,
	})
	if err == nil {
		t.Fatal("expected unauthenticated")
	}
}

func TestApplyConfigOK(t *testing.T) {
	rt := &stubRuntime{state: control.StateStopped}
	client, cleanup := startTestServer(t, "secret", rt)
	defer cleanup()
	ctx := auth.AppendBearerToken(context.Background(), "secret")
	resp, err := client.ApplyConfig(ctx, &agentv1.ApplyConfigRequest{
		ConfigJson: `{"log":{"level":"info"},"inbounds":[],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`,
		ConfigHash: "abc",
		Replace:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Ok || resp.AppliedHash != "abc" {
		t.Fatalf("%+v", resp)
	}
	st := rt.Status(context.Background())
	if st.State != control.StateRunning || st.ConfigHash != "abc" {
		t.Fatalf("%+v", st)
	}
}

func TestPingCapabilityAndProbeOutbound(t *testing.T) {
	rt := &stubRuntime{state: control.StateRunning}
	client, cleanup := startTestServer(t, "secret", rt)
	defer cleanup()
	ctx := auth.AppendBearerToken(context.Background(), "secret")

	ping, err := client.Ping(ctx, &agentv1.PingRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ping.Capabilities) != 1 || ping.Capabilities[0] != "proxy_chain_v1" {
		t.Fatalf("capabilities = %v", ping.Capabilities)
	}
	probe, err := client.ProbeOutbound(ctx, &agentv1.ProbeOutboundRequest{
		OutboundTag: "chain-next",
		Url:         "https://probe.example/204",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !probe.Ok || probe.DelayMs != 1 {
		t.Fatalf("probe = %+v", probe)
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.probeTag != "chain-next" || rt.probeURL != "https://probe.example/204" {
		t.Fatalf("runtime probe args = %q %q", rt.probeTag, rt.probeURL)
	}
}
