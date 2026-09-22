package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/ladderairport/panel/internal/store"
)

// Uplink command types. These name the immediate operations an uplink node
// executes locally and mirror the live gRPC methods on control.Server.
const (
	cmdProbe        = "probe"
	cmdInterfaces   = "interfaces"
	cmdUpgrade      = "upgrade"
	cmdSysMetrics   = "sysmetrics"
	cmdBBRStatus    = "bbr-status"
	cmdBBRSet       = "bbr-set"
	cmdFRPSMappings = "frps-mappings"
	cmdFRPSStart    = "frps-start"
	cmdFRPSStop     = "frps-stop"
)

// upgradeCommandPayload is the argument envelope for a cmdUpgrade command.
type upgradeCommandPayload struct {
	Version     string `json:"version"`
	Repo        string `json:"repo"`
	DownloadURL string `json:"download_url"`
	SHA256      string `json:"sha256"`
}

// bbrSetCommandPayload is the argument envelope for a cmdBBRSet command.
type bbrSetCommandPayload struct {
	Enabled bool `json:"enabled"`
}

// Command-queue tuning. maxPollWait is bounded well under the HTTP server
// ReadTimeout/IdleTimeout so a held long-poll never trips a transport timeout.
const (
	maxPollWait     = 25 * time.Second
	defaultPollWait = 25 * time.Second
	maxLeaseBatch   = 16
)

// agentCommandWire is the shape delivered to a polling node. Only the fields an
// agent needs to execute are exposed; internal bookkeeping stays server-side.
type agentCommandWire struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Payload string `json:"payload"`
}

type agentCommandsResponse struct {
	Commands []agentCommandWire `json:"commands"`
}

type agentCommandResultRequest struct {
	OK     bool   `json:"ok"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// notifyNode wakes any long-poll waiter for a node so a freshly enqueued
// command dispatches within milliseconds instead of on the next poll cycle.
// The channel is buffered/size-1 semantics via non-blocking send: a pending
// signal already guarantees the waiter will re-check the queue.
func (s *Server) notifyNode(nodeID string) {
	s.cmdMu.Lock()
	ch := s.cmdWaiters[nodeID]
	s.cmdMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

// waiterFor returns the per-node wakeup channel, creating it on first use.
func (s *Server) waiterFor(nodeID string) chan struct{} {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	if s.cmdWaiters == nil {
		s.cmdWaiters = make(map[string]chan struct{})
	}
	ch := s.cmdWaiters[nodeID]
	if ch == nil {
		ch = make(chan struct{}, 1)
		s.cmdWaiters[nodeID] = ch
	}
	return ch
}

// enqueueCommand stores a pending command for a node and wakes any waiter.
// It returns the created command so callers can surface its ID to the browser.
func (s *Server) enqueueCommand(nodeID, cmdType, payload string) (*store.AgentCommand, error) {
	cmd := &store.AgentCommand{
		NodeID:  nodeID,
		Type:    cmdType,
		Payload: payload,
	}
	if err := s.Store.CreateAgentCommand(cmd); err != nil {
		return nil, err
	}
	s.notifyNode(nodeID)
	return cmd, nil
}

// enqueueUplinkCommand is the uplink branch for a live-RPC handler: it queues
// the operation and replies 202 with the command ID so the browser can poll
// GET /api/v1/commands/{id} for the async result. payload may be nil for
// argument-less commands. Returns false (after writing an error) on failure.
func (s *Server) enqueueUplinkCommand(w http.ResponseWriter, nodeID, cmdType string, payload any) bool {
	encoded := "{}"
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return false
		}
		encoded = string(raw)
	}
	cmd, err := s.enqueueCommand(nodeID, cmdType, encoded)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"queued":     true,
		"command_id": cmd.ID,
		"type":       cmdType,
		"message":    "uplink 节点已入队命令，请轮询结果",
	})
	return true
}

// enqueueIfUplink is the uplink fallback for capability-gated live handlers
// (sysmetrics/bbr). It loads the node by path ID; when the node runs in uplink
// mode WITHOUT a live WS socket it enforces the capability gate and enqueues the
// command, returning true so the caller returns immediately. When the uplink
// node has a live socket it returns false so the caller runs the shared live
// path over WS (full gRPC parity). For push nodes it returns false. Any error
// response is written here and also returns true.
func (s *Server) enqueueIfUplink(w http.ResponseWriter, r *http.Request, capability, cmdType string, payload any) bool {
	node, err := s.Store.GetNode(pathID(r))
	if err != nil {
		status := http.StatusInternalServerError
		if isNotFound(err) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return true
	}
	if node.ControlMode != store.ControlModeUplink {
		return false
	}
	// A live WS socket makes the uplink node behave like a push node: let the
	// caller run the real-time RPC path instead of queuing a command.
	if _, connected := s.uplinkClient(node.ID); connected {
		return false
	}
	if capability != "" && len(node.Capabilities) > 0 && !slices.Contains(node.Capabilities, capability) {
		writeError(w, http.StatusBadRequest, errAgentTooOld)
		return true
	}
	s.enqueueUplinkCommand(w, node.ID, cmdType, payload)
	return true
}

// commands for the authenticated node, and when none are ready it parks until
// a command arrives, the wait budget elapses, or the client disconnects.
func (s *Server) handleAgentPollCommands(w http.ResponseWriter, r *http.Request) {
	node, ok := s.authenticateAgentNode(w, r, r.URL.Query().Get("node_id"))
	if !ok {
		return
	}
	wait := parsePollWait(r.URL.Query().Get("wait"))

	// Subscribe before the first lease so an enqueue racing this poll is not
	// lost between the DB read and the park.
	ch := s.waiterFor(node.ID)
	deadline := time.Now().Add(wait)
	for {
		cmds, err := s.Store.LeaseAgentCommands(node.ID, maxLeaseBatch, 0)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if len(cmds) > 0 {
			writeJSON(w, http.StatusOK, agentCommandsResponse{Commands: toWire(cmds)})
			return
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			writeJSON(w, http.StatusOK, agentCommandsResponse{Commands: []agentCommandWire{}})
			return
		}
		timer := time.NewTimer(remaining)
		select {
		case <-r.Context().Done():
			timer.Stop()
			return
		case <-ch:
			timer.Stop()
			// Loop and re-lease.
		case <-timer.C:
			writeJSON(w, http.StatusOK, agentCommandsResponse{Commands: []agentCommandWire{}})
			return
		}
	}
}

// handleAgentCommandResult records a node's terminal result for a command. It
// is idempotent so at-least-once redeliveries do not corrupt an existing
// outcome. A node may only complete commands addressed to it.
func (s *Server) handleAgentCommandResult(w http.ResponseWriter, r *http.Request) {
	var req agentCommandResultRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	id := pathID(r)
	cmd, err := s.Store.GetAgentCommand(id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, ok := s.authenticateAgentNode(w, r, cmd.NodeID); !ok {
		return
	}
	updated, applied, err := s.Store.CompleteAgentCommand(id, req.OK, req.Result, req.Error)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"applied": applied,
		"status":  updated.Status,
	})
}

// handleGetCommand is the admin/browser-facing read used to poll an enqueued
// command's status and result after an uplink operation returned 202 + an ID.
func (s *Server) handleGetCommand(w http.ResponseWriter, r *http.Request) {
	cmd, err := s.Store.GetAgentCommand(pathID(r))
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, cmd)
}

func toWire(cmds []store.AgentCommand) []agentCommandWire {
	out := make([]agentCommandWire, 0, len(cmds))
	for i := range cmds {
		out = append(out, agentCommandWire{
			ID:      cmds[i].ID,
			Type:    cmds[i].Type,
			Payload: cmds[i].Payload,
		})
	}
	return out
}

func parsePollWait(raw string) time.Duration {
	if raw == "" {
		return defaultPollWait
	}
	if d, err := time.ParseDuration(raw); err == nil {
		if d < 0 {
			return 0
		}
		if d > maxPollWait {
			return maxPollWait
		}
		return d
	}
	// Bare integer is interpreted as seconds.
	if secs, err := strconv.Atoi(raw); err == nil {
		d := time.Duration(secs) * time.Second
		if d < 0 {
			return 0
		}
		if d > maxPollWait {
			return maxPollWait
		}
		return d
	}
	return defaultPollWait
}
