package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/labberairport/panel/internal/converter"
	"github.com/labberairport/panel/internal/store"
)

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListNodes()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	var n store.Node
	if err := decodeJSON(r, &n); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if n.Name == "" || n.Address == "" {
		writeError(w, http.StatusBadRequest, "name and address required")
		return
	}
	if n.GRPCPort == 0 {
		n.GRPCPort = 50051
	}
	if n.Status == "" {
		n.Status = "unknown"
	}
	if err := s.Store.CreateNode(&n); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	existing, err := s.Store.GetNode(id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var body store.Node
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.ID = id
	body.CreatedAtUnix = existing.CreatedAtUnix
	// Preserve runtime fields if client omits them.
	if body.Status == "" {
		body.Status = existing.Status
	}
	if body.LastSeenUnix == 0 {
		body.LastSeenUnix = existing.LastSeenUnix
	}
	if body.ConfigHash == "" {
		body.ConfigHash = existing.ConfigHash
	}
	if body.Name == "" {
		body.Name = existing.Name
	}
	if body.Address == "" {
		body.Address = existing.Address
	}
	if body.GRPCPort == 0 {
		body.GRPCPort = existing.GRPCPort
	}
	if err := s.Store.UpdateNode(&body); err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := s.Store.GetNode(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := s.Store.DeleteNode(id); err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListNodeInbounds(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if _, err := s.Store.GetNode(id); err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	list, err := s.Store.ListInboundsForNode(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type setNodeInboundsBody struct {
	InboundIDs []string `json:"inbound_ids"`
}

func (s *Server) handleSetNodeInbounds(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	var body setNodeInboundsBody
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.InboundIDs == nil {
		body.InboundIDs = []string{}
	}
	if err := s.Store.SetNodeInbounds(id, body.InboundIDs); err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	list, err := s.Store.ListInboundsForNode(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleNodePreview(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if _, err := s.Store.GetNode(id); err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	inbounds, err := s.Store.ListInboundsForNode(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfgBytes, err := converter.Convert(inbounds)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var cfg any
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleNodeApply(w http.ResponseWriter, r *http.Request) {
	s.runSingleNodeTask(w, r, "apply")
}

func (s *Server) handleNodeStart(w http.ResponseWriter, r *http.Request) {
	s.runSingleNodeTask(w, r, "start")
}

func (s *Server) handleNodeStop(w http.ResponseWriter, r *http.Request) {
	s.runSingleNodeTask(w, r, "stop")
}

func (s *Server) runSingleNodeTask(w http.ResponseWriter, r *http.Request, taskType string) {
	id := pathID(r)
	if _, err := s.Store.GetNode(id); err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.Runner == nil {
		writeError(w, http.StatusServiceUnavailable, "runner not configured")
		return
	}
	task := &store.Task{
		Type:    taskType,
		Status:  "pending",
		NodeIDs: []string{id},
	}
	if err := s.Store.CreateTask(task); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.Runner.RunTask(r.Context(), task.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := s.Store.GetTask(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleProbeNode(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	node, err := s.Store.GetNode(id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.opTimeout())
	defer cancel()

	client, err := s.liveDial(ctx, *node, s.nodeToken(node))
	if err != nil {
		node.Status = "unreachable"
		_ = s.Store.UpdateNode(node)
		writeError(w, http.StatusBadGateway, fmt.Sprintf("dial: %v", err))
		return
	}
	defer func() { _ = client.Close() }()

	resp, err := client.Ping(ctx)
	if err != nil {
		node.Status = classifyRPCError(err)
		node.LastError = err.Error()
		_ = s.Store.UpdateNode(node)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	node.Status = "online"
	node.LastSeenUnix = time.Now().Unix()
	node.AgentVersion = resp.GetAgentVersion()
	node.SingboxVersion = resp.GetSingboxVersion()
	node.LastError = ""
	// Best-effort status/metrics on single probe.
	if st, err := client.GetStatus(ctx); err == nil {
		node.RuntimeState = st.GetState()
		if st.GetConfigHash() != "" {
			node.ConfigHash = st.GetConfigHash()
		}
	}
	if m, err := client.GetMetrics(ctx); err == nil {
		node.Connections = m.GetConnections()
		node.UplinkBytes = m.GetUplinkBytes()
		node.DownlinkBytes = m.GetDownlinkBytes()
		node.CPUPercent = m.GetCpuPercent()
		node.MemoryRSSBytes = m.GetMemoryRssBytes()
		node.MetricsAtUnix = time.Now().Unix()
	}
	if err := s.Store.UpdateNode(node); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"node":            node,
		"agent_version":   resp.GetAgentVersion(),
		"singbox_version": resp.GetSingboxVersion(),
	})
}

func (s *Server) handleNodeMetrics(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	node, err := s.Store.GetNode(id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.opTimeout())
	defer cancel()

	client, err := s.liveDial(ctx, *node, s.nodeToken(node))
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("dial: %v", err))
		return
	}
	defer func() { _ = client.Close() }()

	resp, err := client.GetMetrics(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"connections":       resp.GetConnections(),
		"uplink_bytes":      resp.GetUplinkBytes(),
		"downlink_bytes":    resp.GetDownlinkBytes(),
		"cpu_percent":       resp.GetCpuPercent(),
		"memory_rss_bytes":  resp.GetMemoryRssBytes(),
	})
}

func (s *Server) handleNodeLogs(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	node, err := s.Store.GetNode(id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	level := r.URL.Query().Get("level")
	var tail int32
	if t := r.URL.Query().Get("tail"); t != "" {
		n, err := strconv.Atoi(t)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid tail")
			return
		}
		tail = int32(n)
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	ctx := r.Context()
	client, err := s.liveDial(ctx, *node, s.nodeToken(node))
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("dial: %v", err))
		return
	}
	defer func() { _ = client.Close() }()

	stream, err := client.StreamLogs(ctx, level, tail)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Initial comment so clients see the stream open.
	_, _ = fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	for {
		line, err := stream.Recv()
		if err != nil {
			// Client disconnect or stream end — stop cleanly.
			return
		}
		payload, err := json.Marshal(map[string]any{
			"level":   line.GetLevel(),
			"message": line.GetMessage(),
			"ts":      line.GetTsUnixMs(),
		})
		if err != nil {
			return
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return
		}
		flusher.Flush()
	}
}
