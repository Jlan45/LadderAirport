package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ladderairport/panel/internal/converter"
	"github.com/ladderairport/panel/internal/store"
)

// nodeBootstrapRequest creates a node and returns a one-click agent install command.
type nodeBootstrapRequest struct {
	Name          string   `json:"name"`
	Address       string   `json:"address"` // optional control dial host; fill after install if empty
	GRPCPort      int      `json:"grpc_port"`
	PublicAddress string   `json:"public_address"` // optional subscription client host
	Token         string   `json:"token"`          // optional; auto-generated when empty
	Labels        []string `json:"labels"`
	EnableTLS     *bool    `json:"enable_tls"` // default true
	AgentVersion  string   `json:"agent_version"`
	InstallScript string   `json:"install_script_url"` // optional override
	TLSSkipVerify *bool    `json:"tls_skip_verify"`    // default: false when TLS, true when plain
	CACertPEM     string   `json:"ca_cert_pem"`
}

// nodeInstallResponse is returned after bootstrap or when regenerating the install command.
type nodeInstallResponse struct {
	Node             store.Node `json:"node"`
	Token            string     `json:"token"`
	EnableTLS        bool       `json:"enable_tls"`
	InstallCommand   string     `json:"install_command"`
	UpgradeCommand   string     `json:"upgrade_command,omitempty"`
	UninstallCommand string     `json:"uninstall_command,omitempty"`
	Steps            []string   `json:"steps"`
	PanelBaseURL     string     `json:"panel_base_url,omitempty"`
	EnrollEnabled    bool       `json:"enroll_enabled"`
	// RecommendedAgentVersion is the latest known release tag (may be empty).
	RecommendedAgentVersion string `json:"recommended_agent_version,omitempty"`
	// Outdated is true when the node's reported agent_version looks behind recommended.
	Outdated bool `json:"outdated,omitempty"`
}

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
	if n.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	// Address may be empty when the operator will install first and fill IP later.
	if n.GRPCPort == 0 {
		n.GRPCPort = 50051
	}
	if n.Status == "" {
		if strings.TrimSpace(n.Address) == "" {
			n.Status = "pending"
		} else {
			n.Status = "unknown"
		}
	}
	if err := s.Store.CreateNode(&n); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

// handleBootstrapNode creates a node with an auto token and returns a one-click install command.
// POST /api/v1/nodes/bootstrap
func (s *Server) handleBootstrapNode(w http.ResponseWriter, r *http.Request) {
	var req nodeBootstrapRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	enableTLS := true
	if req.EnableTLS != nil {
		enableTLS = *req.EnableTLS
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		var err error
		token, err = randomAgentToken()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "generate token: "+err.Error())
			return
		}
	}
	port := req.GRPCPort
	if port == 0 {
		port = 50051
	}
	tlsSkip := !enableTLS
	if req.TLSSkipVerify != nil {
		tlsSkip = *req.TLSSkipVerify
	}
	addr := strings.TrimSpace(req.Address)
	status := "unknown"
	if addr == "" {
		status = "pending"
	}
	n := store.Node{
		Name:          req.Name,
		Address:       addr,
		GRPCPort:      port,
		PublicAddress: strings.TrimSpace(req.PublicAddress),
		Token:         token,
		Labels:        req.Labels,
		TLSSkipVerify: tlsSkip,
		CACertPEM:     req.CACertPEM,
		Status:        status,
	}
	if err := s.Store.CreateNode(&n); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Re-read for assigned id/timestamps.
	created, err := s.Store.GetNode(n.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	panelBase := ""
	if st, err := s.Store.GetSettings(); err == nil {
		panelBase = panelBaseFromSettings(st.PublicBaseURL)
	}
	agentVer := strings.TrimSpace(req.AgentVersion)
	cmd := buildInstallCommand(installCommandOpts{
		ScriptURL:    req.InstallScript,
		Token:        token,
		AgentVersion: agentVer,
		EnableTLS:    enableTLS,
		PanelBaseURL: panelBase,
		NodeID:       created.ID,
		GRPCPort:     port,
	})
	rec, _, _ := resolveRecommendedAgentVersion()
	upgradeVer := agentVer
	if upgradeVer == "" || upgradeVer == "latest" {
		upgradeVer = rec
	}
	enrollOK := panelBase != ""
	writeJSON(w, http.StatusCreated, nodeInstallResponse{
		Node:                    *created,
		Token:                   token,
		EnableTLS:               enableTLS,
		InstallCommand:          cmd,
		UpgradeCommand:          buildUpgradeCommand(installCommandOpts{ScriptURL: req.InstallScript, AgentVersion: upgradeVer}),
		UninstallCommand:        buildUninstallCommand(installCommandOpts{ScriptURL: req.InstallScript}, false),
		Steps:                   installSteps(enableTLS, addr, port, panelBase, enrollOK),
		PanelBaseURL:            panelBase,
		EnrollEnabled:           enrollOK,
		RecommendedAgentVersion: rec,
		Outdated:                isAgentVersionOutdated(created.AgentVersion, rec),
	})
}

// handleNodeInstallCommand regenerates the one-click install command for an existing node.
// GET /api/v1/nodes/{id}/install-command
func (s *Server) handleNodeInstallCommand(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	n, err := s.Store.GetNode(id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	token := strings.TrimSpace(n.Token)
	if token == "" {
		// Fall back to panel default agent token.
		st, err := s.Store.GetSettings()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		token = strings.TrimSpace(st.DefaultAgentToken)
	}
	if token == "" {
		writeError(w, http.StatusBadRequest, "node has no token; set node token or default_agent_token in settings")
		return
	}
	// Transport is TLS whenever a CA is configured. TLSSkipVerify controls
	// certificate verification only; it must not switch the transport to plaintext.
	enableTLS := strings.TrimSpace(n.CACertPEM) != ""
	q := r.URL.Query()
	if v := q.Get("tls"); v == "0" || v == "false" {
		enableTLS = false
	} else if v == "1" || v == "true" {
		enableTLS = true
	}
	version := q.Get("version")
	panelBase := ""
	if st, err := s.Store.GetSettings(); err == nil {
		panelBase = panelBaseFromSettings(st.PublicBaseURL)
	}
	if v := strings.TrimSpace(q.Get("panel")); v != "" {
		panelBase = panelBaseFromSettings(v)
	}
	scriptURL := q.Get("script_url")
	cmd := buildInstallCommand(installCommandOpts{
		ScriptURL:    scriptURL,
		Token:        token,
		AgentVersion: version,
		EnableTLS:    enableTLS,
		PanelBaseURL: panelBase,
		NodeID:       n.ID,
		GRPCPort:     n.GRPCPort,
	})
	rec, _, _ := resolveRecommendedAgentVersion()
	upgradeVer := strings.TrimSpace(version)
	if upgradeVer == "" || upgradeVer == "latest" {
		upgradeVer = rec
	}
	enrollOK := panelBase != ""
	writeJSON(w, http.StatusOK, nodeInstallResponse{
		Node:                    *n,
		Token:                   token,
		EnableTLS:               enableTLS,
		InstallCommand:          cmd,
		UpgradeCommand:          buildUpgradeCommand(installCommandOpts{ScriptURL: scriptURL, AgentVersion: upgradeVer}),
		UninstallCommand:        buildUninstallCommand(installCommandOpts{ScriptURL: scriptURL}, false),
		Steps:                   installSteps(enableTLS, n.Address, n.GRPCPort, panelBase, enrollOK),
		PanelBaseURL:            panelBase,
		EnrollEnabled:           enrollOK,
		RecommendedAgentVersion: rec,
		Outdated:                isAgentVersionOutdated(n.AgentVersion, rec),
	})
}

func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	_, err := s.Store.GetNode(id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var body store.NodeOperatorUpdate
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Name != nil {
		name := strings.TrimSpace(*body.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "name must not be empty")
			return
		}
		body.Name = &name
	}
	if body.Address != nil {
		address := strings.TrimSpace(*body.Address)
		body.Address = &address
	}
	if body.GRPCPort != nil {
		if *body.GRPCPort < 1 || *body.GRPCPort > 65535 {
			writeError(w, http.StatusBadRequest, "grpc_port must be between 1 and 65535")
			return
		}
	}
	if body.Token != nil {
		token := strings.TrimSpace(*body.Token)
		body.Token = &token
	}
	if body.PublicAddress != nil {
		publicAddress := strings.TrimSpace(*body.PublicAddress)
		body.PublicAddress = &publicAddress
	}
	if body.EgressInterface != nil {
		egressInterface := strings.TrimSpace(*body.EgressInterface)
		body.EgressInterface = &egressInterface
	}
	if err := s.Store.UpdateNodeOperatorFields(id, body); err != nil {
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
	list, err := s.Store.ListNodeInboundAttachments(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type setNodeInboundsBody struct {
	// InboundIDs is the legacy field (no per-inbound NAT). Prefer Bindings.
	InboundIDs []string `json:"inbound_ids"`
	// Bindings attaches inbounds with optional public_address / public_port overrides.
	// When non-nil, takes precedence over InboundIDs.
	Bindings []store.NodeInboundBinding `json:"bindings"`
	// SkipDeploy when true only updates association (tests / advanced). Default: auto apply+start.
	SkipDeploy bool `json:"skip_deploy"`
}

// setNodeInboundsResponse is returned after attaching inbounds; deploy is implicit.
type setNodeInboundsResponse struct {
	// Inbounds keeps legacy shape for older clients (without NAT fields).
	Inbounds []store.InboundConfig `json:"inbounds"`
	// Attachments includes per-inbound public_address / public_port.
	Attachments   []store.NodeInboundAttachment `json:"attachments"`
	Deployed      bool                          `json:"deployed"`
	DeployMessage string                        `json:"deploy_message,omitempty"`
	ApplyTask     *store.Task                   `json:"apply_task,omitempty"`
	StartTask     *store.Task                   `json:"start_task,omitempty"`
}

func (s *Server) handleSetNodeInbounds(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	var body setNodeInboundsBody
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	bindings := body.Bindings
	if bindings == nil {
		// legacy: inbound_ids only
		if body.InboundIDs == nil {
			body.InboundIDs = []string{}
		}
		bindings = make([]store.NodeInboundBinding, 0, len(body.InboundIDs))
		for _, iid := range body.InboundIDs {
			bindings = append(bindings, store.NodeInboundBinding{InboundID: iid})
		}
	}
	if err := s.Store.SetNodeInboundBindings(id, bindings); err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	atts, err := s.Store.ListNodeInboundAttachments(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	list := make([]store.InboundConfig, 0, len(atts))
	for _, a := range atts {
		list = append(list, a.InboundConfig)
	}

	out := setNodeInboundsResponse{
		Inbounds:    list,
		Attachments: atts,
	}
	if body.SkipDeploy {
		out.DeployMessage = "关联已保存（未下发）"
		writeJSON(w, http.StatusOK, out)
		return
	}

	// Auto deploy via Apply only (agent starts core inside Apply; no separate Start).
	node, err := s.Store.GetNode(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if strings.TrimSpace(node.Address) == "" {
		out.DeployMessage = "关联已保存；节点地址未就绪，上线后由 bootstrap 同步"
		writeJSON(w, http.StatusOK, out)
		return
	}
	if len(bindings) == 0 {
		out.DeployMessage = "关联已清空；未向节点下发（无入站配置）"
		writeJSON(w, http.StatusOK, out)
		return
	}
	if s.Runner == nil {
		out.DeployMessage = "关联已保存；runner 未配置，未下发"
		writeJSON(w, http.StatusOK, out)
		return
	}

	applyTask, depMsg := s.deployNodeInbounds(r.Context(), id)
	out.ApplyTask = applyTask
	out.DeployMessage = depMsg
	if applyTask != nil {
		for _, res := range applyTask.Results {
			if res.OK {
				out.Deployed = true
				break
			}
		}
		if applyTask.Status == "success" {
			out.Deployed = true
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// deployNodeInbounds runs a single apply for one node (core starts with Apply).
func (s *Server) deployNodeInbounds(ctx context.Context, nodeID string) (applyTask *store.Task, msg string) {
	applyTask = &store.Task{
		Type:    "apply",
		Status:  "pending",
		NodeIDs: []string{nodeID},
	}
	if err := s.Store.CreateTask(applyTask); err != nil {
		return nil, "关联已保存；创建下发任务失败: " + err.Error()
	}
	if err := s.Runner.RunTask(ctx, applyTask.ID); err != nil {
		t, _ := s.Store.GetTask(applyTask.ID)
		return t, "关联已保存；下发配置失败: " + err.Error()
	}
	applyTask, _ = s.Store.GetTask(applyTask.ID)
	return applyTask, deployMsgFromApply(applyTask)
}

func deployMsgFromApply(applyTask *store.Task) string {
	if applyTask == nil {
		return "关联已保存；下发结果未知"
	}
	if applyTask.Status == "success" {
		return "已关联并下发配置（核心已启动）"
	}
	for _, res := range applyTask.Results {
		if res.OK {
			return "已关联并下发配置（核心已启动）"
		}
		if res.Message != "" {
			return "关联已保存；下发: " + res.Message
		}
	}
	return "关联已保存；下发状态=" + applyTask.Status
}

func (s *Server) handleNodePreview(w http.ResponseWriter, r *http.Request) {
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
	inbounds, err := s.Store.ListInboundsForNode(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfgBytes, err := converter.Convert(inbounds, converter.ConvertOptions{
		BindInterface: node.EgressInterface,
	})
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
		"connections":      resp.GetConnections(),
		"uplink_bytes":     resp.GetUplinkBytes(),
		"downlink_bytes":   resp.GetDownlinkBytes(),
		"cpu_percent":      resp.GetCpuPercent(),
		"memory_rss_bytes": resp.GetMemoryRssBytes(),
	})
}

func (s *Server) handleNodeInterfaces(w http.ResponseWriter, r *http.Request) {
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

	resp, err := client.ListInterfaces(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	type ifaceJSON struct {
		Name         string   `json:"name"`
		Addresses    []string `json:"addresses"`
		Up           bool     `json:"up"`
		Loopback     bool     `json:"loopback"`
		MTU          int32    `json:"mtu"`
		HardwareAddr string   `json:"hardware_addr"`
	}
	list := make([]ifaceJSON, 0, len(resp.GetInterfaces()))
	for _, iface := range resp.GetInterfaces() {
		addrs := iface.GetAddresses()
		if addrs == nil {
			addrs = []string{}
		}
		list = append(list, ifaceJSON{
			Name:         iface.GetName(),
			Addresses:    addrs,
			Up:           iface.GetUp(),
			Loopback:     iface.GetLoopback(),
			MTU:          iface.GetMtu(),
			HardwareAddr: iface.GetHardwareAddr(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"interfaces": list})
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

// handleNodeUpgrade asks the agent to stage a release binary; the node root helper
// replaces the binary and restarts ladder-agent. Panel should re-probe afterwards.
// POST /api/v1/nodes/{id}/upgrade
// Body (optional): {"version":"v0.7.2","repo":"Jlan45/LadderAirport","download_url":"","sha256":""}
func (s *Server) handleNodeUpgrade(w http.ResponseWriter, r *http.Request) {
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

	var body struct {
		Version     string `json:"version"`
		Repo        string `json:"repo"`
		DownloadURL string `json:"download_url"`
		SHA256      string `json:"sha256"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	version := strings.TrimSpace(body.Version)
	if version == "" {
		if tag, _, _ := resolveRecommendedAgentVersion(); tag != "" {
			version = tag
		} else {
			version = "latest"
		}
	}

	// Staging + download may take longer than a normal probe.
	timeout := s.opTimeout()
	if timeout < 3*time.Minute {
		timeout = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	client, err := s.liveDial(ctx, *node, s.nodeToken(node))
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("dial: %v", err))
		return
	}
	defer func() { _ = client.Close() }()

	resp, err := client.UpgradeAgent(ctx, version, strings.TrimSpace(body.Repo), strings.TrimSpace(body.DownloadURL), strings.TrimSpace(body.SHA256))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if resp == nil {
		writeError(w, http.StatusBadGateway, "empty upgrade response")
		return
	}
	statusCode := http.StatusOK
	if !resp.GetOk() {
		statusCode = http.StatusBadGateway
	}
	// Best-effort note on the node; do not flip status offline just for staging.
	if msg := resp.GetMessage(); msg != "" {
		node.LastError = ""
		if !resp.GetOk() {
			node.LastError = "upgrade: " + msg
		}
		_ = s.Store.UpdateNode(node)
	}
	writeJSON(w, statusCode, map[string]any{
		"ok":               resp.GetOk(),
		"message":          resp.GetMessage(),
		"version":          resp.GetVersion(),
		"staged_path":      resp.GetStagedPath(),
		"previous_version": resp.GetPreviousVersion(),
		"node_id":          node.ID,
		"hint":             "binary staged; helper restarts agent shortly — click 探测 to refresh agent_version",
	})
}
