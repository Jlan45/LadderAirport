package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"

	"github.com/ladderairport/panel/internal/store"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
)

type frpsConfigRequest struct {
	Enabled           bool                       `json:"enabled"`
	BindAddr          string                     `json:"bind_addr"`
	BindPort          int                        `json:"bind_port"`
	ProxyBindAddr     string                     `json:"proxy_bind_addr"`
	AllowPorts        []store.FRPServerPortRange `json:"allow_ports"`
	AuthToken         string                     `json:"auth_token"`
	RotateAuthToken   bool                       `json:"rotate_auth_token"`
	TLSForce          bool                       `json:"tls_force"`
	MaxPortsPerClient *int64                     `json:"max_ports_per_client"`
	// ManagedDomainID binds a managed domain as the frpc-facing server address.
	// Nil keeps the current binding; an empty string clears it.
	ManagedDomainID *string `json:"managed_domain_id"`
}

type frpsConfigResponse struct {
	*store.FRPServerConfig
	GeneratedAuthToken string `json:"generated_auth_token,omitempty"`
	// ServerAddr is the frpc-facing dial host: the bound managed domain FQDN,
	// or the node public/control address when no domain is bound.
	ServerAddr string `json:"server_addr"`
}

type frpsDesiredConfig struct {
	Enabled           bool                       `json:"enabled"`
	BindAddr          string                     `json:"bind_addr"`
	BindPort          int                        `json:"bind_port"`
	ProxyBindAddr     string                     `json:"proxy_bind_addr"`
	AllowPorts        []store.FRPServerPortRange `json:"allow_ports"`
	AuthToken         string                     `json:"auth_token"`
	TLSForce          bool                       `json:"tls_force"`
	MaxPortsPerClient int64                      `json:"max_ports_per_client"`
}

func defaultFRPSConfig(nodeID string) *store.FRPServerConfig {
	return &store.FRPServerConfig{
		NodeID:        nodeID,
		BindAddr:      "0.0.0.0",
		BindPort:      7000,
		ProxyBindAddr: "0.0.0.0",
		AllowPorts: []store.FRPServerPortRange{
			{Start: 20000, End: 30000},
		},
		TLSForce:          true,
		MaxPortsPerClient: 8,
		RuntimeState:      "stopped",
	}
}

func (s *Server) handleGetNodeFRPS(w http.ResponseWriter, r *http.Request) {
	nodeID := pathID(r)
	node, err := s.Store.GetNode(nodeID)
	if err != nil {
		status := http.StatusInternalServerError
		if isNotFound(err) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	config, err := s.Store.GetFRPServerConfig(nodeID)
	if err != nil {
		if isNotFound(err) {
			s.writeFRPSConfig(w, node, defaultFRPSConfig(nodeID), "")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeFRPSConfig(w, node, config, "")
}

func (s *Server) handlePutNodeFRPS(w http.ResponseWriter, r *http.Request) {
	nodeID := pathID(r)
	node, err := s.Store.GetNode(nodeID)
	if err != nil {
		status := http.StatusInternalServerError
		if isNotFound(err) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	if s.Secrets == nil {
		writeError(w, http.StatusServiceUnavailable, "FRPS 密钥存储尚未初始化")
		return
	}

	var request frpsConfigRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	current, err := s.Store.GetFRPServerConfig(nodeID)
	if err != nil && !isNotFound(err) {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	managedDomainID := ""
	if current != nil {
		managedDomainID = current.ManagedDomainID
	}
	if request.ManagedDomainID != nil {
		managedDomainID = strings.TrimSpace(*request.ManagedDomainID)
	}
	if managedDomainID != "" {
		if err := s.validateFRPSManagedDomain(nodeID, managedDomainID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	desired := normalizeFRPSRequest(request)
	ciphertext := ""
	generatedToken := ""
	if current != nil {
		ciphertext = current.AuthTokenCiphertext
	}
	tokenChanged := desired.AuthToken != ""
	if request.RotateAuthToken || (desired.Enabled && desired.AuthToken == "" && ciphertext == "") {
		generatedToken, err = randomToken(32)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "生成 FRPS 认证令牌失败")
			return
		}
		desired.AuthToken = generatedToken
		tokenChanged = true
	} else if desired.AuthToken == "" && ciphertext != "" {
		raw, decryptErr := s.Secrets.Decrypt(ciphertext, frpsTokenAAD(nodeID))
		if decryptErr != nil {
			writeError(w, http.StatusInternalServerError, "解密 FRPS 认证令牌失败")
			return
		}
		desired.AuthToken = string(raw)
	}
	if err := validateFRPSDesired(desired); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if tokenChanged {
		ciphertext, err = s.Secrets.Encrypt([]byte(desired.AuthToken), frpsTokenAAD(nodeID))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "加密 FRPS 认证令牌失败")
			return
		}
	}
	hash, err := hashFRPSDesired(desired)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	config := defaultFRPSConfig(nodeID)
	config.Enabled = desired.Enabled
	config.BindAddr = desired.BindAddr
	config.BindPort = desired.BindPort
	config.ProxyBindAddr = desired.ProxyBindAddr
	config.AllowPorts = desired.AllowPorts
	config.AuthTokenCiphertext = ciphertext
	config.HasAuthToken = ciphertext != ""
	config.TLSForce = desired.TLSForce
	config.MaxPortsPerClient = desired.MaxPortsPerClient
	config.ManagedDomainID = managedDomainID
	config.DesiredHash = hash
	if current != nil {
		config.AppliedHash = current.AppliedHash
		config.RuntimeState = current.RuntimeState
		config.FRPSVersion = current.FRPSVersion
		config.LastError = current.LastError
		config.StartedAtUnix = current.StartedAtUnix
		config.CreatedAtUnix = current.CreatedAtUnix
	}
	if err := s.Store.UpsertFRPServerConfig(config); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if node.ControlMode == store.ControlModeUplink {
		// Without a live WS socket the uplink node applies FRPS via config-sync;
		// just persist and return. With a socket we fall through to the shared
		// live apply path below (full gRPC parity).
		if _, connected := s.uplinkClient(node.ID); !connected {
			s.writeFRPSConfig(w, node, config, generatedToken)
			return
		}
	}

	if len(node.Capabilities) > 0 && !slices.Contains(node.Capabilities, "frps-v1") {
		config.LastError = "节点 Agent 不支持 FRPS 管理，请先升级 Agent"
		_ = s.Store.UpdateFRPServerRuntime(
			nodeID, config.AppliedHash, config.RuntimeState, config.FRPSVersion,
			config.LastError, config.StartedAtUnix,
		)
		writeError(w, http.StatusConflict, config.LastError)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.opTimeout())
	defer cancel()
	client, frpsClient, live, ok := s.dialNodeFRPS(w, ctx, node)
	if !ok {
		return
	}
	if !live {
		// Uplink node lost its socket between the check above and now.
		s.writeFRPSConfig(w, node, config, generatedToken)
		return
	}
	defer client.Close()

	response, err := frpsClient.ApplyFRPServerConfig(
		ctx, desired.toProto(), hash,
	)
	if err != nil {
		s.saveFRPSError(nodeID, config, err.Error())
		writeError(w, http.StatusBadGateway, fmt.Sprintf("下发 FRPS 配置失败：%v", err))
		return
	}
	if response == nil || !response.GetOk() {
		message := "Agent 未能应用 FRPS 配置"
		if response != nil && response.GetMessage() != "" {
			message = response.GetMessage()
		}
		s.saveFRPSError(nodeID, config, message)
		writeError(w, http.StatusBadGateway, message)
		return
	}
	config.AppliedHash = response.GetAppliedHash()
	config.RuntimeState = map[bool]string{true: "running", false: "stopped"}[desired.Enabled]
	config.LastError = ""
	if status, statusErr := frpsClient.GetFRPServerStatus(ctx); statusErr == nil && status != nil {
		applyFRPSStatus(config, status)
	}
	_ = s.Store.UpdateFRPServerRuntime(
		nodeID, config.AppliedHash, config.RuntimeState, config.FRPSVersion,
		config.LastError, config.StartedAtUnix,
	)
	s.writeFRPSConfig(w, node, config, generatedToken)
}

func (s *Server) handleGetNodeFRPSStatus(w http.ResponseWriter, r *http.Request) {
	s.handleNodeFRPSAction(w, r, "status")
}

func (s *Server) handleGetNodeFRPSMappings(w http.ResponseWriter, r *http.Request) {
	nodeID := pathID(r)
	node, err := s.Store.GetNode(nodeID)
	if err != nil {
		status := http.StatusInternalServerError
		if isNotFound(err) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	if node.ControlMode == store.ControlModeUplink {
		if len(node.Capabilities) > 0 && !slices.Contains(node.Capabilities, "frps-mappings-v1") {
			writeError(w, http.StatusConflict, "节点 Agent 不支持 FRPS 在线映射，请先升级 Agent")
			return
		}
		// Prefer the live WS socket; only enqueue a queued command when the node
		// has no real-time channel.
		if _, connected := s.uplinkClient(node.ID); !connected {
			s.enqueueUplinkCommand(w, node.ID, cmdFRPSMappings, nil)
			return
		}
	}
	if len(node.Capabilities) > 0 && !slices.Contains(node.Capabilities, "frps-mappings-v1") {
		writeError(w, http.StatusConflict, "节点 Agent 不支持 FRPS 在线映射，请先升级 Agent")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.opTimeout())
	defer cancel()
	client, _, live, ok := s.dialNodeFRPS(w, ctx, node)
	if !ok {
		return
	}
	if !live {
		s.enqueueUplinkCommand(w, node.ID, cmdFRPSMappings, nil)
		return
	}
	defer client.Close()
	mappingsClient, ok := client.(NodeFRPSMappings)
	if !ok {
		writeError(w, http.StatusConflict, "节点 Agent 不支持 FRPS 在线映射，请先升级 Agent")
		return
	}
	response, err := mappingsClient.GetFRPServerMappings(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("读取 FRPS 在线映射失败：%v", err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleRevealNodeFRPSToken(w http.ResponseWriter, r *http.Request) {
	nodeID := pathID(r)
	if _, err := s.Store.GetNode(nodeID); err != nil {
		status := http.StatusInternalServerError
		if isNotFound(err) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	if s.Secrets == nil {
		writeError(w, http.StatusServiceUnavailable, "FRPS 密钥存储尚未初始化")
		return
	}
	config, err := s.Store.GetFRPServerConfig(nodeID)
	if err != nil {
		status := http.StatusInternalServerError
		if isNotFound(err) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	if config.AuthTokenCiphertext == "" {
		writeError(w, http.StatusNotFound, "节点尚未生成 FRPS 认证令牌")
		return
	}
	raw, err := s.Secrets.Decrypt(config.AuthTokenCiphertext, frpsTokenAAD(nodeID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "解密 FRPS 认证令牌失败")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"auth_token": string(raw)})
}

func (s *Server) handleStartNodeFRPS(w http.ResponseWriter, r *http.Request) {
	s.handleNodeFRPSAction(w, r, "start")
}

func (s *Server) handleStopNodeFRPS(w http.ResponseWriter, r *http.Request) {
	s.handleNodeFRPSAction(w, r, "stop")
}

func (s *Server) handleNodeFRPSAction(w http.ResponseWriter, r *http.Request, action string) {
	nodeID := pathID(r)
	node, err := s.Store.GetNode(nodeID)
	if err != nil {
		status := http.StatusInternalServerError
		if isNotFound(err) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	if node.ControlMode == store.ControlModeUplink && action != "status" {
		// start/stop are immediate runtime ops. Prefer the live WS socket; only
		// enqueue a queued command when the node has no real-time channel.
		if _, connected := s.uplinkClient(node.ID); !connected {
			cmdType := cmdFRPSStart
			if action == "stop" {
				cmdType = cmdFRPSStop
			}
			s.enqueueUplinkCommand(w, node.ID, cmdType, nil)
			return
		}
	}
	config, err := s.Store.GetFRPServerConfig(nodeID)
	if err != nil {
		status := http.StatusInternalServerError
		if isNotFound(err) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	if node.ControlMode == store.ControlModeUplink {
		// A "status" read (or an uplink node without a live socket for start/stop)
		// serves the cached config; live start/stop below runs over the WS socket.
		if action == "status" {
			s.writeFRPSConfig(w, node, config, "")
			return
		}
		if _, connected := s.uplinkClient(node.ID); !connected {
			s.writeFRPSConfig(w, node, config, "")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.opTimeout())
	defer cancel()
	client, frpsClient, live, ok := s.dialNodeFRPS(w, ctx, node)
	if !ok {
		return
	}
	if !live {
		s.writeFRPSConfig(w, node, config, "")
		return
	}
	defer client.Close()

	switch action {
	case "start":
		response, callErr := frpsClient.StartFRPServer(ctx)
		if callErr != nil {
			err = callErr
		} else if response == nil || !response.GetOk() {
			err = fmt.Errorf("%s", frpsActionMessage(response))
		}
	case "stop":
		response, callErr := frpsClient.StopFRPServer(ctx)
		if callErr != nil {
			err = callErr
		} else if response == nil || !response.GetOk() {
			err = fmt.Errorf("%s", frpsActionMessage(response))
		}
	}
	if err != nil {
		s.saveFRPSError(nodeID, config, err.Error())
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	statusResponse, err := frpsClient.GetFRPServerStatus(ctx)
	if err != nil {
		s.saveFRPSError(nodeID, config, err.Error())
		writeError(w, http.StatusBadGateway, fmt.Sprintf("读取 FRPS 状态失败：%v", err))
		return
	}
	applyFRPSStatus(config, statusResponse)
	if err := s.Store.UpdateFRPServerRuntime(
		nodeID, config.AppliedHash, config.RuntimeState, config.FRPSVersion,
		config.LastError, config.StartedAtUnix,
	); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeFRPSConfig(w, node, config, "")
}

// dialNodeFRPS resolves a live FRPS-capable client for node. For push nodes it
// dials the gRPC control port; for uplink nodes with a live WS socket it returns
// the WS-backed client (full gRPC parity). When the node is uplink WITHOUT a
// live socket it returns ok=true, live=false so the caller runs its degraded
// path (persist + config-sync). ok=false means an error response was written.
func (s *Server) dialNodeFRPS(
	w http.ResponseWriter,
	ctx context.Context,
	node *store.Node,
) (NodeLive, NodeFRPS, bool, bool) {
	client, live, err := s.liveClientFor(ctx, node)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("连接节点失败：%v", err))
		return nil, nil, false, false
	}
	if !live {
		return nil, nil, true, false
	}
	frpsClient, ok := client.(NodeFRPS)
	if !ok {
		_ = client.Close()
		writeError(w, http.StatusConflict, "节点 Agent 不支持 FRPS 管理，请先升级 Agent")
		return nil, nil, false, false
	}
	return client, frpsClient, true, true
}

func normalizeFRPSRequest(request frpsConfigRequest) frpsDesiredConfig {
	bindAddr := strings.TrimSpace(request.BindAddr)
	if bindAddr == "" {
		bindAddr = "0.0.0.0"
	}
	proxyBindAddr := strings.TrimSpace(request.ProxyBindAddr)
	if proxyBindAddr == "" {
		proxyBindAddr = "0.0.0.0"
	}
	bindPort := request.BindPort
	if bindPort == 0 {
		bindPort = 7000
	}
	allowPorts := append([]store.FRPServerPortRange{}, request.AllowPorts...)
	if len(allowPorts) == 0 {
		allowPorts = []store.FRPServerPortRange{{Start: 20000, End: 30000}}
	}
	maxPortsPerClient := int64(8)
	if request.MaxPortsPerClient != nil {
		maxPortsPerClient = *request.MaxPortsPerClient
	}
	return frpsDesiredConfig{
		Enabled: request.Enabled, BindAddr: bindAddr, BindPort: bindPort,
		ProxyBindAddr: proxyBindAddr, AllowPorts: allowPorts,
		AuthToken: strings.TrimSpace(request.AuthToken),
		TLSForce:  true, MaxPortsPerClient: maxPortsPerClient,
	}
}

func validateFRPSDesired(config frpsDesiredConfig) error {
	if !config.Enabled {
		return nil
	}
	if net.ParseIP(config.BindAddr) == nil {
		return fmt.Errorf("FRPS 绑定地址必须是 IP 地址")
	}
	if net.ParseIP(config.ProxyBindAddr) == nil {
		return fmt.Errorf("FRPS 代理绑定地址必须是 IP 地址")
	}
	if config.BindPort < 1 || config.BindPort > 65535 {
		return fmt.Errorf("FRPS 绑定端口必须在 1 到 65535 之间")
	}
	if config.AuthToken == "" {
		return fmt.Errorf("启用 FRPS 时必须提供认证令牌")
	}
	if !config.TLSForce {
		return fmt.Errorf("FRPS 必须启用 TLS 强制模式")
	}
	if len(config.AllowPorts) == 0 {
		return fmt.Errorf("必须配置至少一个 FRPS 允许端口范围")
	}
	if config.MaxPortsPerClient < 0 {
		return fmt.Errorf("每客户端最大端口数不能为负数")
	}
	for _, portRange := range config.AllowPorts {
		if portRange.Start < 1 || portRange.End > 65535 || portRange.Start > portRange.End {
			return fmt.Errorf("FRPS 允许端口范围必须在 1 到 65535 之间")
		}
		if config.BindPort >= portRange.Start && config.BindPort <= portRange.End {
			return fmt.Errorf("FRPS 控制端口不能包含在允许端口范围内")
		}
	}
	return nil
}

func hashFRPSDesired(config frpsDesiredConfig) (string, error) {
	raw, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("编码 FRPS 配置失败：%w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (config frpsDesiredConfig) toProto() *agentv1.FRPServerConfig {
	out := &agentv1.FRPServerConfig{
		Enabled: config.Enabled, BindAddr: config.BindAddr,
		BindPort: uint32(config.BindPort), ProxyBindAddr: config.ProxyBindAddr,
		AuthToken: config.AuthToken, TlsForce: config.TLSForce,
		MaxPortsPerClient: config.MaxPortsPerClient,
	}
	for _, portRange := range config.AllowPorts {
		out.AllowPorts = append(out.AllowPorts, &agentv1.FRPServerPortRange{
			Start: uint32(portRange.Start), End: uint32(portRange.End),
		})
	}
	return out
}

func applyFRPSStatus(config *store.FRPServerConfig, status *agentv1.GetFRPServerStatusResponse) {
	if config == nil || status == nil {
		return
	}
	config.RuntimeState = status.GetState()
	config.AppliedHash = status.GetConfigHash()
	config.StartedAtUnix = status.GetStartedAtUnix()
	config.LastError = status.GetLastError()
	config.FRPSVersion = status.GetFrpsVersion()
}

func (s *Server) saveFRPSError(nodeID string, config *store.FRPServerConfig, message string) {
	if config == nil {
		return
	}
	config.LastError = message
	_ = s.Store.UpdateFRPServerRuntime(
		nodeID, config.AppliedHash, config.RuntimeState, config.FRPSVersion,
		message, config.StartedAtUnix,
	)
}

func frpsTokenAAD(nodeID string) string {
	return "node-frps:" + nodeID
}

// validateFRPSManagedDomain ensures the domain is usable as this node's FRPS
// server address: it must exist, be enabled, and belong to the same node. Any
// address source qualifies — agent_public/node_address track the node IP by
// construction and manual addresses are operator-managed.
func (s *Server) validateFRPSManagedDomain(nodeID, domainID string) error {
	domain, err := s.Store.GetManagedDomain(domainID)
	if err != nil {
		return fmt.Errorf("绑定的托管域名不存在：%s", domainID)
	}
	if !domain.Enabled {
		return fmt.Errorf("绑定的托管域名已禁用：%s", domain.FQDN)
	}
	if domain.NodeID != nodeID {
		return fmt.Errorf("托管域名 %s 关联的是其他节点，无法绑定到本节点 FRPS", domain.FQDN)
	}
	return nil
}

// frpsServerAddr is the display-layer frpc dial host. The managed domain only
// rewrites what clients connect to; the agent-side bind config is untouched.
func (s *Server) frpsServerAddr(node *store.Node, config *store.FRPServerConfig) string {
	if config != nil && config.ManagedDomainID != "" {
		if domain, err := s.Store.GetManagedDomain(config.ManagedDomainID); err == nil {
			return domain.FQDN
		}
	}
	if node == nil {
		return ""
	}
	if public := strings.TrimSpace(node.PublicAddress); public != "" {
		return public
	}
	return strings.TrimSpace(node.Address)
}

func (s *Server) writeFRPSConfig(w http.ResponseWriter, node *store.Node, config *store.FRPServerConfig, generatedToken string) {
	writeJSON(w, http.StatusOK, frpsConfigResponse{
		FRPServerConfig:    config,
		GeneratedAuthToken: generatedToken,
		ServerAddr:         s.frpsServerAddr(node, config),
	})
}

func frpsActionMessage(response interface{ GetMessage() string }) string {
	if response == nil || response.GetMessage() == "" {
		return "Agent 未能执行 FRPS 操作"
	}
	return response.GetMessage()
}
