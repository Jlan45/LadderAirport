package uplinkhub

import (
	"sync"

	"github.com/coder/websocket"
)

// Hub is the registry of live uplink connections, one per node. Panel live
// handlers look up a node's Conn here to issue AgentControl RPCs over the
// WebSocket instead of dialing a gRPC control port, so a connected uplink node
// behaves exactly like a push node.
type Hub struct {
	mu    sync.RWMutex
	conns map[string]*Conn
}

// NewHub constructs an empty Hub.
func NewHub() *Hub {
	return &Hub{conns: make(map[string]*Conn)}
}

// Register installs conn as the live connection for its node, displacing and
// closing any previous connection for the same node so a reconnect supersedes a
// stale socket.
func (h *Hub) Register(conn *Conn) {
	if h == nil || conn == nil {
		return
	}
	h.mu.Lock()
	prev := h.conns[conn.nodeID]
	h.conns[conn.nodeID] = conn
	h.mu.Unlock()
	if prev != nil && prev != conn {
		prev.Close()
	}
}

// Remove unregisters conn only if it is still the current connection for its
// node; a newer reconnect must not be evicted by an older socket's teardown.
func (h *Hub) Remove(conn *Conn) {
	if h == nil || conn == nil {
		return
	}
	h.mu.Lock()
	if h.conns[conn.nodeID] == conn {
		delete(h.conns, conn.nodeID)
	}
	h.mu.Unlock()
}

// Get returns the live connection for a node, if any.
func (h *Hub) Get(nodeID string) (*Conn, bool) {
	if h == nil {
		return nil, false
	}
	h.mu.RLock()
	conn, ok := h.conns[nodeID]
	h.mu.RUnlock()
	return conn, ok
}

// Connected reports whether a node currently has a live uplink socket.
func (h *Hub) Connected(nodeID string) bool {
	_, ok := h.Get(nodeID)
	return ok
}

// Client returns a WS-backed AgentControl client for a connected node. The
// returned *Client mirrors *nodeclient.Client so live handlers use it exactly
// like a gRPC dial result.
func (h *Hub) Client(nodeID string) (*Client, bool) {
	conn, ok := h.Get(nodeID)
	if !ok {
		return nil, false
	}
	return newClient(conn), true
}

// Serve registers a freshly accepted socket, pumps inbound frames until it
// closes, then unregisters. It blocks for the lifetime of the connection and is
// meant to run on the WebSocket upgrade handler's goroutine. onReport receives
// unsolicited status-report frames pushed by the Agent.
func (h *Hub) Serve(nodeID string, ws *websocket.Conn, onReport func(nodeID string, payload []byte)) {
	conn := newConn(nodeID, ws, onReport)
	h.Register(conn)
	defer h.Remove(conn)
	conn.readLoop()
}
