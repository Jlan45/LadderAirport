package api

import (
	"net/http"
	"strings"

	"github.com/ladderairport/panel/internal/store"
)

type enrollAgentRequest struct {
	NodeID string `json:"node_id"`
}

type enrollAgentResponse struct {
	ControlToken string `json:"control_token"`
}

// handleAgentEnroll exchanges a one-time enrollment token for the long-lived
// control token. Uplink nodes (HTTP report + WebSocket) use this instead of
// the certificate issuance endpoint, so they never initialize management TLS.
func (s *Server) handleAgentEnroll(w http.ResponseWriter, r *http.Request) {
	var req enrollAgentRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	req.NodeID = strings.TrimSpace(req.NodeID)
	n, err := s.Store.GetNode(req.NodeID)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "节点不存在")
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if n.ControlMode != store.ControlModeUplink {
		writeError(w, http.StatusBadRequest, "只有 uplink 节点可以跳过管理面 TLS 注册")
		return
	}
	token := bearerToken(r)
	want, status, msg := s.acceptNodeCredential(n, token)
	if status != 0 {
		writeError(w, status, msg)
		return
	}
	if err := s.Store.MarkAgentEnrolled(n.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n.Status == "" || n.Status == "pending" || n.Status == "unauthorized" {
		updated, getErr := s.Store.GetNode(n.ID)
		if getErr == nil {
			updated.Status = "unknown"
			updated.LastError = ""
			_ = s.Store.UpdateNode(updated)
		}
	}
	writeJSON(w, http.StatusOK, enrollAgentResponse{ControlToken: want})
}
