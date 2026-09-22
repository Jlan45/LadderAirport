package proxychain

import (
	"context"
	"fmt"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/ladderairport/panel/internal/nodeclient"
	"github.com/ladderairport/panel/internal/nodeconfig"
	"github.com/ladderairport/panel/internal/pki"
	"github.com/ladderairport/panel/internal/store"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
)

const Capability = "proxy_chain_v1"

type Agent interface {
	Close() error
	Ping(context.Context) (*agentv1.PingResponse, error)
	ApplyConfig(context.Context, string, string, bool) (*agentv1.ApplyConfigResponse, error)
	ProbeOutbound(context.Context, string, string) (*agentv1.ProbeOutboundResponse, error)
}

type DialFunc func(context.Context, store.Node, string) (Agent, error)

// UplinkClientFunc resolves a live WS-backed Agent for a connected uplink node.
// When it returns ok=true the node has a real-time channel and the returned
// Agent drives it at parity with the push/gRPC control plane.
type UplinkClientFunc func(nodeID string) (Agent, bool)

type Service struct {
	Store       *store.Store
	Builder     *nodeconfig.Builder
	Dial        DialFunc
	Coordinator *sync.Mutex
	PKI         *pki.Manager
	// UplinkClient, when set, returns a live WS client for a connected uplink
	// node so immediate outbound probes reach it directly instead of failing.
	UplinkClient UplinkClientFunc
}

func NewService(st *store.Store, builder *nodeconfig.Builder, coordinator *sync.Mutex) *Service {
	if builder == nil {
		builder = &nodeconfig.Builder{Store: st}
	}
	if coordinator == nil {
		coordinator = &sync.Mutex{}
	}
	s := &Service{Store: st, Builder: builder, Coordinator: coordinator}
	s.Dial = s.defaultDial
	return s
}

func (s *Service) defaultDial(ctx context.Context, node store.Node, token string) (Agent, error) {
	if s.PKI == nil {
		return nil, fmt.Errorf("管理 PKI 不可用")
	}
	if node.PKICertSerial == "" || node.PKICABundlePEM == "" {
		return nil, fmt.Errorf("节点 %s 尚未完成 Panel PKI 注册", node.ID)
	}
	settings, err := s.Store.GetSettings()
	if err != nil {
		return nil, err
	}
	clientCert := s.PKI.ClientCertificate()
	cfg := nodeclient.DialConfig{
		Address:           net.JoinHostPort(node.Address, fmt.Sprintf("%d", node.GRPCPort)),
		Token:             token,
		Timeout:           time.Duration(settings.GRPCTimeoutSec) * time.Second,
		CACertPEM:         []byte(node.PKICABundlePEM),
		ClientCertificate: &clientCert,
		ExpectedPeerURI:   pki.AgentURI(node.ID),
		ExpectedSerial:    node.PKICertSerial,
	}
	return nodeclient.Dial(ctx, cfg)
}

func (s *Service) token(node store.Node) string {
	if node.Token != "" {
		return node.Token
	}
	settings, _ := s.Store.GetSettings()
	if settings != nil {
		return settings.DefaultAgentToken
	}
	return ""
}

func (s *Service) Preview(candidate store.ProxyChain) (map[string]nodeconfig.Result, error) {
	if candidate.ID == "" {
		candidate.ID = "preview-chain"
	}
	candidate.Enabled = true
	chains, err := s.effectiveChains(&candidate, false)
	if err != nil {
		return nil, err
	}
	results := map[string]nodeconfig.Result{}
	for _, hop := range candidate.Hops {
		if _, exists := results[hop.NodeID]; exists {
			continue
		}
		cfg, err := s.Builder.BuildWithChains(hop.NodeID, chains)
		if err != nil {
			return nil, err
		}
		results[hop.NodeID] = cfg
	}
	return results, nil
}

// Deploy enables a disabled chain or atomically replaces an active chain.
func (s *Service) Deploy(ctx context.Context, candidate store.ProxyChain) error {
	s.Coordinator.Lock()
	defer s.Coordinator.Unlock()

	old, err := s.Store.GetProxyChain(candidate.ID)
	if err != nil {
		return err
	}
	candidate.Enabled = true
	candidate.State = "degraded"
	if candidate.Name == "" {
		candidate.Name = old.Name
	}
	if len(candidate.Hops) == 0 {
		candidate.Hops = old.Hops
	}
	if err := s.Store.ValidateProxyChain(&candidate); err != nil {
		return err
	}
	oldChains, err := s.effectiveChains(nil, false)
	if err != nil {
		return err
	}
	newChains, err := s.effectiveChains(&candidate, false)
	if err != nil {
		return err
	}
	oldConfigs, newConfigs, order, err := s.planConfigs(old, &candidate, oldChains, newChains, false)
	if err != nil {
		return err
	}
	if err := s.preflight(ctx, candidate); err != nil {
		return err
	}
	changed, err := s.applyOrdered(ctx, order, newConfigs, "chain-deploy")
	if err != nil {
		rollbackErr := s.rollback(ctx, changed, oldConfigs)
		if rollbackErr != nil {
			_ = s.Store.SetProxyChainRuntime(old.ID, old.Enabled, "degraded", err.Error()+"; rollback: "+rollbackErr.Error())
			return fmt.Errorf("%w（回滚失败：%v）", err, rollbackErr)
		}
		return err
	}
	if err := s.Store.UpdateProxyChain(&candidate); err != nil {
		_ = s.rollback(ctx, changed, oldConfigs)
		return err
	}
	if err := s.Store.SetProxyChainRuntime(candidate.ID, true, "degraded", ""); err != nil {
		return err
	}
	_, _ = s.ProbeLocked(ctx, candidate.ID)
	return nil
}

func (s *Service) Disable(ctx context.Context, id string) error {
	s.Coordinator.Lock()
	defer s.Coordinator.Unlock()
	old, err := s.Store.GetProxyChain(id)
	if err != nil {
		return err
	}
	if !old.Enabled {
		return nil
	}
	oldChains, err := s.effectiveChains(nil, false)
	if err != nil {
		return err
	}
	newChains, err := s.effectiveChains(old, true)
	if err != nil {
		return err
	}
	oldConfigs, newConfigs, order, err := s.planConfigs(old, nil, oldChains, newChains, true)
	if err != nil {
		return err
	}
	changed, err := s.applyOrdered(ctx, order, newConfigs, "chain-disable")
	if err != nil {
		if rollbackErr := s.rollback(ctx, changed, oldConfigs); rollbackErr != nil {
			_ = s.Store.SetProxyChainRuntime(id, true, "degraded", err.Error()+"; rollback: "+rollbackErr.Error())
			return fmt.Errorf("%w（回滚失败：%v）", err, rollbackErr)
		}
		return err
	}
	return s.Store.SetProxyChainRuntime(id, false, "disabled", "")
}

func (s *Service) effectiveChains(replacement *store.ProxyChain, disable bool) ([]store.ProxyChain, error) {
	chains, err := s.Store.ListProxyChains()
	if err != nil {
		return nil, err
	}
	out := make([]store.ProxyChain, 0, len(chains)+1)
	replaced := false
	for _, chain := range chains {
		if replacement != nil && chain.ID == replacement.ID {
			replaced = true
			if !disable {
				next := *replacement
				next.Enabled = true
				out = append(out, next)
			}
			continue
		}
		if chain.Enabled {
			out = append(out, chain)
		}
	}
	if replacement != nil && !replaced && !disable {
		next := *replacement
		next.Enabled = true
		out = append(out, next)
	}
	return out, nil
}

func (s *Service) planConfigs(old, candidate *store.ProxyChain, oldChains, newChains []store.ProxyChain, disabling bool) (
	map[string]nodeconfig.Result, map[string]nodeconfig.Result, []string, error,
) {
	nodeIDs := map[string]bool{}
	if old != nil {
		for _, hop := range old.Hops {
			nodeIDs[hop.NodeID] = true
		}
	}
	if candidate != nil {
		for _, hop := range candidate.Hops {
			nodeIDs[hop.NodeID] = true
		}
	}
	oldConfigs := map[string]nodeconfig.Result{}
	newConfigs := map[string]nodeconfig.Result{}
	for nodeID := range nodeIDs {
		oldCfg, err := s.Builder.BuildWithChains(nodeID, oldChains)
		if err != nil {
			return nil, nil, nil, err
		}
		newCfg, err := s.Builder.BuildWithChains(nodeID, newChains)
		if err != nil {
			return nil, nil, nil, err
		}
		oldConfigs[nodeID], newConfigs[nodeID] = oldCfg, newCfg
	}
	order := []string{}
	base := old
	if candidate != nil {
		base = candidate
	}
	if disabling {
		for _, hop := range base.Hops {
			order = appendUnique(order, hop.NodeID)
		}
	} else {
		for i := len(base.Hops) - 1; i >= 0; i-- {
			order = appendUnique(order, base.Hops[i].NodeID)
		}
		if old != nil {
			for _, hop := range old.Hops {
				order = appendUnique(order, hop.NodeID)
			}
		}
	}
	return oldConfigs, newConfigs, order, nil
}

func appendUnique(values []string, value string) []string {
	if !slices.Contains(values, value) {
		return append(values, value)
	}
	return values
}

func (s *Service) preflight(ctx context.Context, chain store.ProxyChain) error {
	for i, hop := range chain.Hops {
		node, err := s.Store.GetNode(hop.NodeID)
		if err != nil {
			return err
		}
		if node.ControlMode == store.ControlModeUplink {
			if len(node.Capabilities) > 0 && !slices.Contains(node.Capabilities, Capability) {
				return fmt.Errorf("第 %d 跳的 Agent 必须升级：缺少能力 %s", i+1, Capability)
			}
			continue
		}
		client, err := s.Dial(ctx, *node, s.token(*node))
		if err != nil {
			return fmt.Errorf("第 %d 跳连接失败：%w", i+1, err)
		}
		ping, pingErr := client.Ping(ctx)
		_ = client.Close()
		if pingErr != nil {
			return fmt.Errorf("第 %d 跳探测失败：%w", i+1, pingErr)
		}
		if !slices.Contains(ping.GetCapabilities(), Capability) {
			return fmt.Errorf("第 %d 跳的 Agent 必须升级：缺少能力 %s", i+1, Capability)
		}
	}
	return nil
}

func (s *Service) applyOrdered(ctx context.Context, order []string, configs map[string]nodeconfig.Result, taskID string) ([]string, error) {
	changed := []string{}
	for _, nodeID := range order {
		cfg := configs[nodeID]
		node, err := s.Store.GetNode(nodeID)
		if err != nil {
			return changed, err
		}
		if node.ControlMode == store.ControlModeUplink {
			if err := s.Store.SaveSnapshot(&store.ConfigSnapshot{
				NodeID: nodeID, ConfigJSON: cfg.JSON, ConfigHash: cfg.Hash, TaskID: taskID,
			}); err != nil {
				return changed, err
			}
			continue
		}
		client, err := s.Dial(ctx, *node, s.token(*node))
		if err != nil {
			return changed, fmt.Errorf("连接节点 %s 失败：%w", nodeID, err)
		}
		resp, applyErr := client.ApplyConfig(ctx, cfg.JSON, cfg.Hash, true)
		_ = client.Close()
		if applyErr != nil {
			return changed, fmt.Errorf("向节点 %s 下发配置失败：%w", nodeID, applyErr)
		}
		if !resp.GetOk() {
			return changed, fmt.Errorf("向节点 %s 下发配置失败：%s", nodeID, resp.GetMessage())
		}
		changed = append(changed, nodeID)
		node.ConfigHash = cfg.Hash
		node.Status = "online"
		node.RuntimeState = "running"
		node.LastError = ""
		node.LastSeenUnix = time.Now().Unix()
		_ = s.Store.UpdateNode(node)
		_ = s.Store.SaveSnapshot(&store.ConfigSnapshot{
			NodeID: nodeID, ConfigJSON: cfg.JSON, ConfigHash: cfg.Hash, TaskID: taskID,
		})
	}
	return changed, nil
}

func (s *Service) rollback(ctx context.Context, changed []string, configs map[string]nodeconfig.Result) error {
	var first error
	for i := len(changed) - 1; i >= 0; i-- {
		nodeID := changed[i]
		node, err := s.Store.GetNode(nodeID)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		client, err := s.Dial(ctx, *node, s.token(*node))
		if err == nil {
			cfg := configs[nodeID]
			var resp *agentv1.ApplyConfigResponse
			resp, err = client.ApplyConfig(ctx, cfg.JSON, cfg.Hash, true)
			if err == nil && !resp.GetOk() {
				err = fmt.Errorf("%s", resp.GetMessage())
			}
			_ = client.Close()
		}
		if err != nil && first == nil {
			first = fmt.Errorf("节点 %s：%w", nodeID, err)
		}
	}
	return first
}

type ProbeResult struct {
	OK             bool   `json:"ok"`
	DelayMS        int    `json:"delay_ms"`
	Message        string `json:"message,omitempty"`
	FailedHopIndex int    `json:"failed_hop_index"`
}

func (s *Service) Probe(ctx context.Context, id string) (ProbeResult, error) {
	s.Coordinator.Lock()
	defer s.Coordinator.Unlock()
	return s.ProbeLocked(ctx, id)
}

func (s *Service) ProbeLocked(ctx context.Context, id string) (ProbeResult, error) {
	chain, err := s.Store.GetProxyChain(id)
	if err != nil {
		return ProbeResult{}, err
	}
	if !chain.Enabled {
		return ProbeResult{}, fmt.Errorf("代理链已禁用")
	}
	settings, err := s.Store.GetSettings()
	if err != nil {
		return ProbeResult{}, err
	}
	entry := chain.Hops[0]
	resp, err := s.probeNode(ctx, entry.NodeID, nodeconfig.ChainOutboundTag(chain.ID, 0), settings.ChainProbeURL)
	if err == nil && resp.GetOk() {
		result := ProbeResult{OK: true, DelayMS: int(resp.GetDelayMs()), FailedHopIndex: -1}
		_ = s.Store.SetProxyChainProbe(id, true, result.DelayMS, "", -1)
		_ = s.Store.MigrateSubscriptionsToChains()
		return result, nil
	}
	message := "探测失败"
	if err != nil {
		message = err.Error()
	} else if resp.GetMessage() != "" {
		message = resp.GetMessage()
	}
	failed := s.diagnose(ctx, *chain, settings.ChainProbeURL)
	_ = s.Store.SetProxyChainProbe(id, false, 0, message, failed)
	return ProbeResult{OK: false, Message: message, FailedHopIndex: failed}, nil
}

func (s *Service) diagnose(ctx context.Context, chain store.ProxyChain, targetURL string) int {
	for i := len(chain.Hops) - 1; i >= 0; i-- {
		tag := "direct"
		if i < len(chain.Hops)-1 {
			tag = nodeconfig.ChainOutboundTag(chain.ID, i)
		}
		resp, err := s.probeNode(ctx, chain.Hops[i].NodeID, tag, targetURL)
		if err != nil || !resp.GetOk() {
			if i == len(chain.Hops)-1 {
				return i
			}
			return i + 1
		}
	}
	return 0
}

func (s *Service) probeNode(ctx context.Context, nodeID, tag, targetURL string) (*agentv1.ProbeOutboundResponse, error) {
	node, err := s.Store.GetNode(nodeID)
	if err != nil {
		return nil, err
	}
	if node.ControlMode == store.ControlModeUplink {
		// Prefer the live WS uplink so the probe runs in real time, at parity
		// with a push node's gRPC control plane. Only fail when no socket exists.
		if s.UplinkClient != nil {
			if client, ok := s.UplinkClient(nodeID); ok {
				defer func() { _ = client.Close() }()
				return client.ProbeOutbound(ctx, tag, targetURL)
			}
		}
		return nil, fmt.Errorf("uplink 节点未建立实时通道，无法即时出站探测")
	}
	client, err := s.Dial(ctx, *node, s.token(*node))
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()
	return client.ProbeOutbound(ctx, tag, targetURL)
}

func (s *Service) RunProbeLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			settings, err := s.Store.GetSettings()
			if err != nil {
				continue
			}
			chains, err := s.Store.ListProxyChains()
			if err != nil {
				continue
			}
			now := time.Now().Unix()
			for _, chain := range chains {
				if !chain.Enabled || now-chain.LastProbeUnix < int64(settings.ChainProbeIntervalSec) {
					continue
				}
				timeout := time.Duration(settings.ChainProbeTimeoutSec) * time.Second
				probeCtx, cancel := context.WithTimeout(ctx, timeout)
				_, _ = s.Probe(probeCtx, chain.ID)
				cancel()
			}
		}
	}
}
