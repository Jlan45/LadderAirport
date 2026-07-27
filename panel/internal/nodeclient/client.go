// Package nodeclient provides a gRPC client for agent control planes.
package nodeclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/ladderairport/pkg/auth"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// DialConfig configures a connection to an agent node.
type DialConfig struct {
	Address           string // host:port (or passthrough URI for custom dialers)
	Token             string
	Timeout           time.Duration
	CACertPEM         []byte
	ClientCertificate *tls.Certificate
	ExpectedPeerURI   string
	ExpectedSerial    string

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
// Panel-managed mTLS is mandatory. The CA bundle, Panel client certificate,
// Agent URI identity and bound certificate serial must all be present.
func Dial(ctx context.Context, cfg DialConfig) (*Client, error) {
	if cfg.Address == "" {
		return nil, fmt.Errorf("必须提供地址")
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
		return nil, fmt.Errorf("连接 gRPC 地址 %s 失败：%w", cfg.Address, err)
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
		return nil, fmt.Errorf("必须提供管理 CA 证书包")
	}
	if cfg.ClientCertificate == nil {
		return nil, fmt.Errorf("必须提供 Panel 客户端证书")
	}
	if cfg.ExpectedPeerURI == "" || cfg.ExpectedSerial == "" {
		return nil, fmt.Errorf("必须提供 Agent 证书身份和序列号")
	}

	tlsCfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{*cfg.ClientCertificate},
	}
	if host, _, err := net.SplitHostPort(cfg.Address); err == nil && !strings.Contains(cfg.Address, "://") {
		host = strings.Trim(host, "[]")
		if zone := strings.LastIndex(host, "%"); zone > 0 && strings.Contains(host[:zone], ":") {
			host = host[:zone]
		}
		tlsCfg.ServerName = host
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(cfg.CACertPEM) {
		return nil, fmt.Errorf("解析 CA 证书 PEM 失败")
	}
	tlsCfg.RootCAs = pool
	tlsCfg.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return fmt.Errorf("Agent 未提供证书")
		}
		leaf := state.PeerCertificates[0]
		got := fmt.Sprintf("%032x", leaf.SerialNumber)
		if got != cfg.ExpectedSerial {
			return fmt.Errorf("Agent 证书序列号不匹配")
		}
		found := false
		for _, uri := range leaf.URIs {
			if uri.String() == cfg.ExpectedPeerURI {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("Agent 证书身份不匹配")
		}
		return nil
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

func (c *Client) ProbeOutbound(ctx context.Context, outboundTag, targetURL string) (*agentv1.ProbeOutboundResponse, error) {
	return c.api.ProbeOutbound(c.withAuth(ctx), &agentv1.ProbeOutboundRequest{
		OutboundTag: outboundTag,
		Url:         targetURL,
	})
}

// ListInterfaces lists host network interfaces on the agent for egress selection.
func (c *Client) ListInterfaces(ctx context.Context) (*agentv1.ListInterfacesResponse, error) {
	return c.api.ListInterfaces(c.withAuth(ctx), &agentv1.ListInterfacesRequest{})
}

func (c *Client) GetPublicAddresses(ctx context.Context, ipv4, ipv6 bool) (*agentv1.GetPublicAddressesResponse, error) {
	return c.api.GetPublicAddresses(c.withAuth(ctx), &agentv1.GetPublicAddressesRequest{
		Ipv4: ipv4, Ipv6: ipv6,
	})
}

func (c *Client) PrepareProtocolCertificate(
	ctx context.Context,
	certificateID, generationID string,
	dnsNames []string,
) (*agentv1.PrepareProtocolCertificateResponse, error) {
	return c.api.PrepareProtocolCertificate(c.withAuth(ctx), &agentv1.PrepareProtocolCertificateRequest{
		CertificateId: certificateID, GenerationId: generationID, DnsNames: dnsNames,
	})
}

func (c *Client) InstallProtocolCertificate(
	ctx context.Context,
	certificateID, generationID, keyID, certificatePEM string,
	dnsNames []string,
) (*agentv1.InstallProtocolCertificateResponse, error) {
	return c.api.InstallProtocolCertificate(c.withAuth(ctx), &agentv1.InstallProtocolCertificateRequest{
		CertificateId: certificateID, GenerationId: generationID, KeyId: keyID,
		CertificatePem: certificatePEM, DnsNames: dnsNames,
	})
}

func (c *Client) GetProtocolCertificateStatus(
	ctx context.Context,
	certificateID, generationID string,
) (*agentv1.GetProtocolCertificateStatusResponse, error) {
	return c.api.GetProtocolCertificateStatus(c.withAuth(ctx), &agentv1.GetProtocolCertificateStatusRequest{
		CertificateId: certificateID, GenerationId: generationID,
	})
}

func (c *Client) DeleteProtocolCertificateGeneration(
	ctx context.Context,
	certificateID, generationID string,
) (*agentv1.DeleteProtocolCertificateGenerationResponse, error) {
	return c.api.DeleteProtocolCertificateGeneration(
		c.withAuth(ctx),
		&agentv1.DeleteProtocolCertificateGenerationRequest{
			CertificateId: certificateID, GenerationId: generationID,
		},
	)
}

// UpgradeAgent stages a new agent binary on the node for the root upgrade helper.
func (c *Client) UpgradeAgent(ctx context.Context, version, repo, downloadURL, sha256 string) (*agentv1.UpgradeAgentResponse, error) {
	return c.api.UpgradeAgent(c.withAuth(ctx), &agentv1.UpgradeAgentRequest{
		Version:     version,
		Repo:        repo,
		DownloadUrl: downloadURL,
		Sha256:      sha256,
	})
}

// StreamLogs opens a server-streaming log subscription.
func (c *Client) StreamLogs(ctx context.Context, level string, tail int32) (agentv1.AgentControl_StreamLogsClient, error) {
	return c.api.StreamLogs(c.withAuth(ctx), &agentv1.StreamLogsRequest{
		Level: level,
		Tail:  tail,
	})
}
