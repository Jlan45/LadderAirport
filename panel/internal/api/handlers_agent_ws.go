package api

import (
	"encoding/json"
	"net/http"

	"github.com/coder/websocket"
	"github.com/ladderairport/pkg/uplinkws"
)

// handleAgentUplinkWS upgrades an authenticated node connection to a WebSocket
// and registers it with the uplink Hub. Once connected, Panel issues the full
// AgentControl surface (probe, logs, protocol certificates, DNS public probe,
// FRPS, …) over the socket with parity to the push/gRPC control plane, and the
// Agent pushes unsolicited status reports on the same channel.
//
// Auth reuses the per-node Bearer token compare (authenticateAgentNode) via the
// standard HTTP handshake headers, so no admin session is required.
func (s *Server) handleAgentUplinkWS(w http.ResponseWriter, r *http.Request) {
	if s.Uplink == nil {
		writeError(w, http.StatusServiceUnavailable, "uplink WebSocket 尚未启用")
		return
	}
	node, ok := s.authenticateAgentNode(w, r, r.URL.Query().Get("node_id"))
	if !ok {
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: []string{uplinkws.Subprotocol},
	})
	if err != nil {
		// Accept already wrote the failure response.
		return
	}
	if conn.Subprotocol() != uplinkws.Subprotocol {
		conn.Close(websocket.StatusPolicyViolation, "子协议不匹配")
		return
	}

	// Serve blocks for the lifetime of the socket, pumping inbound frames and
	// dispatching unsolicited reports through onAgentReport.
	s.Uplink.Serve(node.ID, conn, s.onUplinkReport)
}

// onUplinkReport applies an unsolicited status report frame pushed by a
// connected uplink Agent. It mirrors the HTTP /agent/report handler so live and
// fallback transports converge node state identically. Malformed frames are
// dropped silently to avoid tearing down the socket.
func (s *Server) onUplinkReport(nodeID string, payload []byte) {
	var req agentReportRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return
	}
	if req.NodeID == "" {
		req.NodeID = nodeID
	}
	if req.NodeID != nodeID {
		// A socket may only report for its authenticated node.
		return
	}
	_, _ = s.Store.ApplyNodeReport(nodeID, reportFromRequest(req))
}
