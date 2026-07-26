package nodeclient_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/ladderairport/panel/internal/nodeclient"
	"github.com/ladderairport/panel/internal/pki"
	"github.com/ladderairport/pkg/auth"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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

func TestDialRequiresAddress(t *testing.T) {
	_, err := nodeclient.Dial(context.Background(), nodeclient.DialConfig{})
	if err == nil {
		t.Fatal("expected error for empty address")
	}
}

func TestDialRequiresManagedPKI(t *testing.T) {
	_, err := nodeclient.Dial(context.Background(), nodeclient.DialConfig{Address: "agent:50051"})
	if err == nil {
		t.Fatal("expected missing management PKI to be rejected")
	}
}

func TestMutualTLSIdentityAndSerial(t *testing.T) {
	ca, err := pki.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agentKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: "node-mtls"},
		DNSNames: []string{"bufnet"},
	}, agentKey)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := ca.SignAgentCSR("node-mtls", pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE REQUEST", Bytes: csrDER,
	}), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalPKCS8PrivateKey(agentKey)
	agentPair, err := tls.X509KeyPair(issued.CertPEM, pem.EncodeToMemory(&pem.Block{
		Type: "PRIVATE KEY", Bytes: keyDER,
	}))
	if err != nil {
		t.Fatal(err)
	}
	clientRoots := x509.NewCertPool()
	if !clientRoots.AppendCertsFromPEM(ca.BundlePEM()) {
		t.Fatal("parse client CA")
	}
	serverTLS := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{agentPair},
		ClientCAs:    clientRoots,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	lis := bufconn.Listen(bufSize)
	server := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(serverTLS)),
		grpc.UnaryInterceptor(auth.UnaryServerInterceptor(testToken)),
	)
	agentv1.RegisterAgentControlServer(server, &fakeAgent{
		state:       "stopped",
		connections: 3,
		uplink:      100,
		downlink:    200,
	})
	go func() { _ = server.Serve(lis) }()
	defer server.Stop()
	defer lis.Close()

	panelClient := ca.ClientCertificate()
	cfg := nodeclient.DialConfig{
		Address:           "passthrough:///bufnet",
		Token:             testToken,
		CACertPEM:         ca.BundlePEM(),
		ClientCertificate: &panelClient,
		ExpectedPeerURI:   pki.AgentURI("node-mtls"),
		ExpectedSerial:    issued.Serial,
		Dialer: func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		},
	}
	client, err := nodeclient.Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Ping(context.Background()); err != nil {
		t.Fatalf("mTLS Ping: %v", err)
	}
	applied, err := client.ApplyConfig(context.Background(), `{"inbounds":[]}`, "hash-abc", true)
	if err != nil || !applied.Ok {
		t.Fatalf("mTLS ApplyConfig: response=%+v err=%v", applied, err)
	}
	metrics, err := client.GetMetrics(context.Background())
	if err != nil || metrics.Connections != 3 {
		t.Fatalf("mTLS GetMetrics: response=%+v err=%v", metrics, err)
	}

	cfg.ExpectedSerial = "00000000000000000000000000000001"
	wrong, err := nodeclient.Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer wrong.Close()
	if _, err := wrong.Ping(context.Background()); err == nil {
		t.Fatal("expected certificate serial mismatch")
	}

	cfg.ExpectedSerial = issued.Serial
	cfg.Token = "wrong"
	badToken, err := nodeclient.Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer badToken.Close()
	if _, err := badToken.Ping(context.Background()); err == nil {
		t.Fatal("expected bearer token authentication failure")
	}
}
