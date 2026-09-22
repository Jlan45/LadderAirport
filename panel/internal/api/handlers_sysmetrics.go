package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NodeSysMetrics is implemented by live clients for Agents with the
// node-metrics-v1 capability. Separate from NodeLive so existing test doubles
// remain source-compatible (same pattern as NodeFRPS).
type NodeSysMetrics interface {
	GetNodeMetrics(ctx context.Context) (*agentv1.GetNodeMetricsResponse, error)
}

// NodeBBR is implemented by live clients for Agents with the bbr-v1 capability.
type NodeBBR interface {
	GetBBRStatus(ctx context.Context) (*agentv1.GetBBRStatusResponse, error)
	SetBBR(ctx context.Context, enabled bool) (*agentv1.SetBBRResponse, error)
}

const (
	capabilityNodeMetrics = "node-metrics-v1"
	capabilityBBR         = "bbr-v1"

	errAgentTooOld = "agent 版本过旧，不支持该功能，请先升级节点"
)

// dialNodeCapability fetches the node, enforces the capability gate and
// resolves a live client. For push nodes it dials the gRPC control port; for
// uplink nodes with a live WS socket it returns the WS-backed client (full
// gRPC parity). Uplink nodes without a socket are handled earlier by
// enqueueIfUplink, so reaching here without a live socket yields a 409. It
// writes the error response and returns ok=false on failure; the caller must
// then return.
func (s *Server) dialNodeCapability(
	w http.ResponseWriter,
	r *http.Request,
	capability string,
) (NodeLive, bool) {
	node, err := s.Store.GetNode(pathID(r))
	if err != nil {
		status := http.StatusInternalServerError
		if isNotFound(err) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return nil, false
	}
	if len(node.Capabilities) > 0 && !slices.Contains(node.Capabilities, capability) {
		writeError(w, http.StatusBadRequest, errAgentTooOld)
		return nil, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.opTimeout())
	defer cancel()
	client, live, err := s.liveClientFor(ctx, node)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("连接节点失败：%v", err))
		return nil, false
	}
	if !live {
		writeError(w, http.StatusConflict, errUplinkNoLiveRPC)
		return nil, false
	}
	return client, true
}

// writeIfUnimplemented maps codes.Unimplemented (older agent binary) to the
// same 400 response as a missing capability. ok=false means the response was
// written and the caller must return.
func writeIfUnimplemented(w http.ResponseWriter, err error) (handled bool) {
	if status.Code(err) == codes.Unimplemented {
		writeError(w, http.StatusBadRequest, errAgentTooOld)
		return true
	}
	return false
}

type sysMetricsResponse struct {
	CPUPercent      float64 `json:"cpu_percent"`
	MemoryTotal     uint64  `json:"memory_total_bytes"`
	MemoryUsed      uint64  `json:"memory_used_bytes"`
	DiskTotal       uint64  `json:"disk_total_bytes"`
	DiskUsed        uint64  `json:"disk_used_bytes"`
	UplinkBps       uint64  `json:"uplink_bps"`
	DownlinkBps     uint64  `json:"downlink_bps"`
	CollectedAtUnix int64   `json:"collected_at_unix"`
}

func (s *Server) handleNodeSysMetrics(w http.ResponseWriter, r *http.Request) {
	if s.enqueueIfUplink(w, r, capabilityNodeMetrics, cmdSysMetrics, nil) {
		return
	}
	client, ok := s.dialNodeCapability(w, r, capabilityNodeMetrics)
	if !ok {
		return
	}
	defer func() { _ = client.Close() }()
	metricsClient, ok := client.(NodeSysMetrics)
	if !ok {
		writeError(w, http.StatusBadRequest, errAgentTooOld)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.opTimeout())
	defer cancel()
	resp, err := metricsClient.GetNodeMetrics(ctx)
	if err != nil {
		if writeIfUnimplemented(w, err) {
			return
		}
		writeError(w, http.StatusBadGateway, fmt.Sprintf("读取节点系统指标失败：%v", err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, sysMetricsResponse{
		CPUPercent:      resp.GetCpuPercent(),
		MemoryTotal:     resp.GetMemoryTotalBytes(),
		MemoryUsed:      resp.GetMemoryUsedBytes(),
		DiskTotal:       resp.GetDiskTotalBytes(),
		DiskUsed:        resp.GetDiskUsedBytes(),
		UplinkBps:       resp.GetUplinkBps(),
		DownlinkBps:     resp.GetDownlinkBps(),
		CollectedAtUnix: resp.GetCollectedAtUnix(),
	})
}

type bbrStatusResponse struct {
	BBRAvailable      bool     `json:"bbr_available"`
	BBREnabled        bool     `json:"bbr_enabled"`
	CurrentControl    string   `json:"current_congestion_control"`
	CurrentQdisc      string   `json:"current_qdisc"`
	SupportedControls []string `json:"supported_controls"`
}

func (s *Server) dialNodeBBR(w http.ResponseWriter, r *http.Request) (NodeBBR, NodeLive, bool) {
	client, ok := s.dialNodeCapability(w, r, capabilityBBR)
	if !ok {
		return nil, nil, false
	}
	bbrClient, ok := client.(NodeBBR)
	if !ok {
		_ = client.Close()
		writeError(w, http.StatusBadRequest, errAgentTooOld)
		return nil, nil, false
	}
	return bbrClient, client, true
}

func (s *Server) handleGetNodeBBR(w http.ResponseWriter, r *http.Request) {
	if s.enqueueIfUplink(w, r, capabilityBBR, cmdBBRStatus, nil) {
		return
	}
	bbrClient, client, ok := s.dialNodeBBR(w, r)
	if !ok {
		return
	}
	defer func() { _ = client.Close() }()
	ctx, cancel := context.WithTimeout(r.Context(), s.opTimeout())
	defer cancel()
	resp, err := bbrClient.GetBBRStatus(ctx)
	if err != nil {
		if writeIfUnimplemented(w, err) {
			return
		}
		writeError(w, http.StatusBadGateway, fmt.Sprintf("读取 BBR 状态失败：%v", err))
		return
	}
	supported := resp.GetSupportedControls()
	if supported == nil {
		supported = []string{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, bbrStatusResponse{
		BBRAvailable:      resp.GetBbrAvailable(),
		BBREnabled:        resp.GetBbrEnabled(),
		CurrentControl:    resp.GetCurrentCongestionControl(),
		CurrentQdisc:      resp.GetCurrentQdisc(),
		SupportedControls: supported,
	})
}

func (s *Server) handleSetNodeBBR(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	if s.enqueueIfUplink(w, r, capabilityBBR, cmdBBRSet, bbrSetCommandPayload{Enabled: body.Enabled}) {
		return
	}
	bbrClient, client, ok := s.dialNodeBBR(w, r)
	if !ok {
		return
	}
	defer func() { _ = client.Close() }()
	ctx, cancel := context.WithTimeout(r.Context(), s.opTimeout())
	defer cancel()
	resp, err := bbrClient.SetBBR(ctx, body.Enabled)
	if err != nil {
		if writeIfUnimplemented(w, err) {
			return
		}
		writeError(w, http.StatusBadGateway, fmt.Sprintf("设置 BBR 失败：%v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      resp.GetOk(),
		"message": resp.GetMessage(),
	})
}
