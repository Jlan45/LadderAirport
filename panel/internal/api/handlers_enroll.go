package api

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/ladderairport/panel/internal/store"
)

// agentEnrollRequest is posted by install-agent.sh after TLS material is ready.
// Auth: Bearer <node token> (or token field). No admin session required.
type agentEnrollRequest struct {
	Token      string `json:"token"`
	NodeID     string `json:"node_id"`
	Address    string `json:"address"`
	GRPCPort   int    `json:"grpc_port"`
	CACertPEM  string `json:"ca_cert_pem"`
	Hostname   string `json:"hostname"`
	TLSEnabled *bool  `json:"tls_enabled"` // default true when ca present
}

type agentEnrollResponse struct {
	OK      bool       `json:"ok"`
	Message string     `json:"message"`
	Node    store.Node `json:"node"`
}

// handleAgentEnroll lets a newly installed agent report reachability + CA to Panel.
// POST /api/v1/agent/enroll (public, node-token auth)
func (s *Server) handleAgentEnroll(w http.ResponseWriter, r *http.Request) {
	var req agentEnrollRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	token := strings.TrimSpace(req.Token)
	if token == "" {
		// Authorization: Bearer <token>
		authz := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if strings.HasPrefix(authz, prefix) {
			token = strings.TrimSpace(strings.TrimPrefix(authz, prefix))
		}
	}
	if token == "" {
		writeError(w, http.StatusUnauthorized, "missing node token")
		return
	}

	var n *store.Node
	var err error
	if id := strings.TrimSpace(req.NodeID); id != "" {
		n, err = s.Store.GetNode(id)
		if err != nil {
			if isNotFound(err) {
				writeError(w, http.StatusNotFound, "node not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Constant-time compare against node token (or empty → reject).
		want := strings.TrimSpace(n.Token)
		if want == "" || subtle.ConstantTimeCompare([]byte(want), []byte(token)) != 1 {
			writeError(w, http.StatusUnauthorized, "invalid token for node")
			return
		}
	} else {
		n, err = s.Store.GetNodeByToken(token)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				writeError(w, http.StatusUnauthorized, "invalid token")
				return
			}
			if strings.Contains(err.Error(), "multiple") {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	addr := strings.TrimSpace(req.Address)
	ca := strings.TrimSpace(req.CACertPEM)
	if addr == "" && ca == "" {
		writeError(w, http.StatusBadRequest, "address or ca_cert_pem required")
		return
	}

	// Manual-first: only fill empty control dial fields so NAT/public overrides
	// set in the Panel are not clobbered by install-time private IP detection.
	// public_address is never written by enroll (operator/UI only).
	if addr != "" && strings.TrimSpace(n.Address) == "" {
		n.Address = addr
	}
	if req.GRPCPort > 0 && req.GRPCPort <= 65535 && n.GRPCPort == 0 {
		n.GRPCPort = req.GRPCPort
	}
	if ca != "" {
		n.CACertPEM = ca
		n.TLSSkipVerify = false
	}
	tlsEnabled := ca != ""
	if req.TLSEnabled != nil {
		tlsEnabled = *req.TLSEnabled
	}
	if !tlsEnabled {
		// Plain install: no CA, skip verify so Panel can still dial.
		n.TLSSkipVerify = true
		if ca == "" {
			// leave existing CA or clear? keep as-is if operator had set one
		}
	}
	if n.Status == "pending" || n.Status == "" {
		n.Status = "unknown"
	}
	// Optional hostname as label annotation only if empty labels and hostname given — skip to avoid surprise.
	_ = req.Hostname

	if err := s.Store.UpdateNode(n); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := s.Store.GetNode(n.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agentEnrollResponse{
		OK:      true,
		Message: "enrolled",
		Node:    *updated,
	})
}
