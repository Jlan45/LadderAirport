// Package uplinkws implements the Agent side of the Agent↔Panel WebSocket
// uplink. It dials the Panel, then serves the full AgentControl surface over a
// single persistent socket: Panel issues RPC requests (probe, logs, protocol
// certificates, DNS public probe, FRPS, …) which the Agent executes against its
// local control.Server and replies to, achieving parity with the push/gRPC
// control plane. The Agent also pushes unsolicited status reports on the same
// socket.
//
// This live channel coexists with the HTTP report + config-sync + command-queue
// paths, which remain as a degraded fallback when the socket is unavailable.
package uplinkws

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/ladderairport/pkg/uplinkws"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
)

// Capability advertises WS uplink support to Panel via the Ping capabilities.
const Capability = "uplink-ws-v1"

const (
	// writeTimeout bounds a single frame write independently of any request
	// context so a stalled Panel cannot wedge a handler.
	writeTimeout = 15 * time.Second
	// maxFrameBytes caps a single inbound frame. ApplyConfig carries the full
	// sing-box config JSON, so the limit is generous.
	maxFrameBytes = 16 << 20
	// dialTimeout bounds the WebSocket handshake.
	dialTimeout = 20 * time.Second
	// maxReconnectBackoff caps the redial backoff after repeated failures.
	maxReconnectBackoff = 30 * time.Second
)

// Config wires the WS uplink to the Panel and the local control surface.
type Config struct {
	PanelURL    string
	NodeID      string
	Token       string
	ReportEvery time.Duration
	HTTPClient  *http.Client
	// Server is the local AgentControl implementation (*control.Server). Every
	// Panel request is dispatched against it.
	Server agentv1.AgentControlServer
}

// Client maintains one live uplink socket to the Panel, redialing with backoff
// on loss.
type Client struct {
	cfg Config
	url string
}

// New validates config and constructs a Client. The socket is not dialed until
// Run is called.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.PanelURL) == "" || strings.TrimSpace(cfg.NodeID) == "" || strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("必须提供 Panel URL、节点 ID 和令牌")
	}
	if cfg.Server == nil {
		return nil, fmt.Errorf("必须提供本地控制面")
	}
	if cfg.ReportEvery <= 0 {
		cfg.ReportEvery = 15 * time.Second
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	wsURL, err := buildWSURL(cfg.PanelURL, cfg.NodeID)
	if err != nil {
		return nil, err
	}
	return &Client{cfg: cfg, url: wsURL}, nil
}

// buildWSURL derives the ws(s):// uplink endpoint from the Panel base URL,
// carrying the node ID as a query parameter (the token travels in a handshake
// header).
func buildWSURL(panelURL, nodeID string) (string, error) {
	u, err := url.Parse(strings.TrimRight(panelURL, "/"))
	if err != nil {
		return "", fmt.Errorf("解析 Panel URL 失败：%w", err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "ws", "wss":
		// already a websocket scheme
	default:
		return "", fmt.Errorf("不支持的 Panel URL 协议 %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/agent/uplink"
	q := u.Query()
	q.Set("node_id", nodeID)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Run maintains the uplink until ctx is cancelled, reconnecting with capped
// backoff after any disconnect.
func (c *Client) Run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if err := c.connectAndServe(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("uplink WS 断开：%v（%s 后重连）", err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < maxReconnectBackoff {
				backoff *= 2
				if backoff > maxReconnectBackoff {
					backoff = maxReconnectBackoff
				}
			}
			continue
		}
		backoff = time.Second
	}
}

// connectAndServe dials the socket and serves it until it closes or ctx ends.
func (c *Client) connectAndServe(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	ws, _, err := websocket.Dial(dialCtx, c.url, &websocket.DialOptions{
		HTTPClient:   c.cfg.HTTPClient,
		HTTPHeader:   http.Header{"Authorization": []string{"Bearer " + c.cfg.Token}},
		Subprotocols: []string{uplinkws.Subprotocol},
	})
	cancel()
	if err != nil {
		return fmt.Errorf("拨号失败：%w", err)
	}
	if ws.Subprotocol() != uplinkws.Subprotocol {
		ws.Close(websocket.StatusPolicyViolation, "子协议不匹配")
		return fmt.Errorf("Panel 未协商子协议 %q", uplinkws.Subprotocol)
	}
	ws.SetReadLimit(maxFrameBytes)
	log.Printf("uplink WS 已连接 %s", c.url)

	sess := newSession(c, ws)
	return sess.serve(ctx)
}

// session owns one live socket: it reads frames, dispatches Panel requests to
// the control surface on their own goroutines, and pushes periodic reports.
type session struct {
	client *Client
	ws     *websocket.Conn

	writeMu sync.Mutex

	mu      sync.Mutex
	streams map[uint64]context.CancelFunc
}

func newSession(client *Client, ws *websocket.Conn) *session {
	return &session{
		client:  client,
		ws:      ws,
		streams: make(map[uint64]context.CancelFunc),
	}
}

func (s *session) serve(ctx context.Context) error {
	// serveCtx cancels all in-flight handlers when the socket ends.
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go s.reportLoop(serveCtx)

	for {
		typ, data, err := s.ws.Read(serveCtx)
		if err != nil {
			return err
		}
		if typ != websocket.MessageText {
			continue
		}
		var frame uplinkws.Frame
		if err := jsonUnmarshal(data, &frame); err != nil {
			// A malformed frame must not tear down the socket.
			continue
		}
		s.dispatch(serveCtx, &frame)
	}
}

func (s *session) dispatch(ctx context.Context, frame *uplinkws.Frame) {
	switch frame.Type {
	case uplinkws.FrameRequest:
		go s.handleRequest(ctx, frame)
	case uplinkws.FrameCancel:
		s.cancelStream(frame.ID)
	default:
		// Responses/streams/reports flow Agent→Panel only; ignore inbound.
	}
}
