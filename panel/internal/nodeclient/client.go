// Package nodeclient provides a gRPC client for agent control planes.
package nodeclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"time"

	"github.com/ladderairport/pkg/auth"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// DialConfig configures a connection to an agent node.
type DialConfig struct {
	Address       string // host:port (or passthrough URI for custom dialers)
	Token         string
	Timeout       time.Duration
	TLSSkipVerify bool
	CACertPEM     []byte

	// Dialer is optional. When set, used as the gRPC context dialer (e.g. bufconn in tests).
	Dialer func(ctx context.Context, addr string) (net.Conn, error)
}

// Client is a thin wrapper around the AgentControl gRPC client.
// All RPCs attach the bearer token via auth.AppendBearerToken.
type Client struct {
	conn  *grpc.ClientConn
	api   agentv1.AgentControlClient
	token string
}

// Dial connects to an agent control server.
//
// Transport selection:
//   - When CACertPEM is empty, uses plaintext insecure credentials (lab default;
//     the agent listens without TLS by default).
//   - When CACertPEM is non-empty, builds a TLS client config from the CA pool.
//     TLSSkipVerify, if set, enables InsecureSkipVerify on that TLS config.
func Dial(ctx context.Context, cfg DialConfig) (*Client, error) {
	if cfg.Address == "" {
		return nil, fmt.Errorf("address required")
	}

	creds, err := transportCredentials(cfg)
	if err != nil {
		return nil, err
	}

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
	}
	if cfg.Dialer != nil {
		opts = append(opts, grpc.WithContextDialer(cfg.Dialer))
	}

	// Optional dial deadline for the NewClient call context (non-blocking connect).
	dialCtx := ctx
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}
	_ = dialCtx // reserved for future blocking dial helpers

	conn, err := grpc.NewClient(cfg.Address, opts...)
	if err != nil {
		return nil, fmt.Errorf("grpc dial %s: %w", cfg.Address, err)
	}

	return &Client{
		conn:  conn,
		api:   agentv1.NewAgentControlClient(conn),
		token: cfg.Token,
	}, nil
}

// NewWithAPI constructs a Client around an existing AgentControl client.
// Useful for tests that inject a mock or bufconn-backed stub. Close is a no-op
// unless the Client was created via Dial (conn is nil).
func NewWithAPI(api agentv1.AgentControlClient, token string) *Client {
	return &Client{api: api, token: token}
}

func transportCredentials(cfg DialConfig) (credentials.TransportCredentials, error) {
	if len(cfg.CACertPEM) == 0 {
		// Lab default: agent serves plaintext gRPC.
		return insecure.NewCredentials(), nil
	}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	if cfg.TLSSkipVerify {
		tlsCfg.InsecureSkipVerify = true
	} else {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(cfg.CACertPEM) {
			return nil, fmt.Errorf("failed to parse CA certificate PEM")
		}
		tlsCfg.RootCAs = pool
	}
	return credentials.NewTLS(tlsCfg), nil
}

// Close closes the underlying gRPC connection.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) withAuth(ctx context.Context) context.Context {
	if c.token == "" {
		return ctx
	}
	return auth.AppendBearerToken(ctx, c.token)
}

// Ping calls the Ping RPC.
func (c *Client) Ping(ctx context.Context) (*agentv1.PingResponse, error) {
	return c.api.Ping(c.withAuth(ctx), &agentv1.PingRequest{})
}

// ApplyConfig pushes a full sing-box config to the agent.
func (c *Client) ApplyConfig(ctx context.Context, configJSON, hash string, replace bool) (*agentv1.ApplyConfigResponse, error) {
	return c.api.ApplyConfig(c.withAuth(ctx), &agentv1.ApplyConfigRequest{
		ConfigJson: configJSON,
		ConfigHash: hash,
		Replace:    replace,
	})
}

// Start starts the agent runtime.
func (c *Client) Start(ctx context.Context) (*agentv1.StartResponse, error) {
	return c.api.Start(c.withAuth(ctx), &agentv1.StartRequest{})
}

// Stop stops the agent runtime.
func (c *Client) Stop(ctx context.Context) (*agentv1.StopResponse, error) {
	return c.api.Stop(c.withAuth(ctx), &agentv1.StopRequest{})
}

// GetStatus fetches agent runtime status.
func (c *Client) GetStatus(ctx context.Context) (*agentv1.GetStatusResponse, error) {
	return c.api.GetStatus(c.withAuth(ctx), &agentv1.GetStatusRequest{})
}

// GetMetrics fetches agent runtime metrics.
func (c *Client) GetMetrics(ctx context.Context) (*agentv1.GetMetricsResponse, error) {
	return c.api.GetMetrics(c.withAuth(ctx), &agentv1.GetMetricsRequest{})
}

// StreamLogs opens a server-streaming log subscription.
func (c *Client) StreamLogs(ctx context.Context, level string, tail int32) (agentv1.AgentControl_StreamLogsClient, error) {
	return c.api.StreamLogs(c.withAuth(ctx), &agentv1.StreamLogsRequest{
		Level: level,
		Tail:  tail,
	})
}
