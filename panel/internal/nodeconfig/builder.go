// Package nodeconfig assembles the complete desired sing-box configuration for
// one Agent, including standalone bindings and all enabled proxy chains.
package nodeconfig

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ladderairport/panel/internal/converter"
	"github.com/ladderairport/panel/internal/store"
	"github.com/ladderairport/panel/internal/subscription"
	"github.com/ladderairport/pkg/hashutil"
)

type Builder struct {
	Store *store.Store
}

type Result struct {
	JSON string
	Hash string
}

func (b *Builder) Build(nodeID string) (Result, error) {
	chains, err := b.Store.ListProxyChains()
	if err != nil {
		return Result{}, err
	}
	return b.BuildWithChains(nodeID, enabledChains(chains))
}

// BuildWithChains builds against an explicit effective chain set. It is used by
// ordered deployment to validate/apply a candidate topology before committing
// it to SQLite.
func (b *Builder) BuildWithChains(nodeID string, chains []store.ProxyChain) (Result, error) {
	if b == nil || b.Store == nil {
		return Result{}, fmt.Errorf("节点配置构建器尚未配置")
	}
	node, err := b.Store.GetNode(nodeID)
	if err != nil {
		return Result{}, err
	}
	inbounds, err := b.Store.ListInboundsForNode(nodeID)
	if err != nil {
		return Result{}, err
	}
	byID := make(map[string]bool, len(inbounds))
	for _, in := range inbounds {
		byID[in.ID] = true
	}
	routes := []converter.ChainRoute{}
	for _, chain := range chains {
		if !chain.Enabled {
			continue
		}
		for i, hop := range chain.Hops {
			if hop.NodeID != nodeID {
				continue
			}
			in, err := b.Store.GetInbound(hop.InboundID)
			if err != nil {
				return Result{}, fmt.Errorf("代理链 %q 第 %d 跳：%w", chain.Name, i+1, err)
			}
			if !in.Enabled {
				return Result{}, fmt.Errorf("代理链 %q 第 %d 跳的入站已禁用", chain.Name, i+1)
			}
			if byID[in.ID] {
				return Result{}, fmt.Errorf("代理链 %q 第 %d 跳与独立入站 %s 冲突", chain.Name, i+1, in.ID)
			}
			byID[in.ID] = true
			inbounds = append(inbounds, *in)
			if i == len(chain.Hops)-1 {
				continue
			}
			ep, err := b.ResolveHopEndpoint(chain, i+1)
			if err != nil {
				return Result{}, err
			}
			outbound, err := subscription.SingboxOutbound(ep)
			if err != nil {
				return Result{}, fmt.Errorf("代理链 %q 第 %d 跳的出站配置失败：%w", chain.Name, i+1, err)
			}
			outbound["tag"] = ChainOutboundTag(chain.ID, i)
			if iface := strings.TrimSpace(node.EgressInterface); iface != "" {
				outbound["bind_interface"] = iface
			}
			routes = append(routes, converter.ChainRoute{
				InboundID: hop.InboundID,
				Outbound:  outbound,
			})
		}
	}
	inbounds, err = b.applyManagedTLS(nodeID, inbounds)
	if err != nil {
		return Result{}, err
	}
	raw, err := converter.Convert(inbounds, converter.ConvertOptions{
		BindInterface: node.EgressInterface,
		AllowEmpty:    true,
		ChainRoutes:   routes,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{JSON: string(raw), Hash: hashutil.SHA256Hex(raw)}, nil
}

func (b *Builder) applyManagedTLS(nodeID string, inbounds []store.InboundConfig) ([]store.InboundConfig, error) {
	out := make([]store.InboundConfig, len(inbounds))
	copy(out, inbounds)
	for i := range out {
		resolved, _, _, err := b.ResolveManagedTLS(nodeID, out[i])
		if err != nil {
			return nil, err
		}
		out[i] = resolved
	}
	return out, nil
}

// ResolveManagedTLS overlays the per-node binding on a copy of inbound and
// returns the managed client-facing hostname when enabled.
func (b *Builder) ResolveManagedTLS(
	nodeID string,
	inbound store.InboundConfig,
) (store.InboundConfig, string, bool, error) {
	bindings, err := b.Store.ListNodeInboundTLSBindings(nodeID)
	if err != nil {
		return inbound, "", false, err
	}
	var binding *store.NodeInboundTLSBinding
	for i := range bindings {
		if bindings[i].InboundID == inbound.ID && bindings[i].Mode == "managed" {
			binding = &bindings[i]
			break
		}
	}
	if binding == nil {
		return inbound, "", false, nil
	}
	domain, err := b.Store.GetManagedDomain(binding.ManagedDomainID)
	if err != nil {
		return inbound, "", false, err
	}
	certificate, err := b.Store.GetProtocolCertificate(binding.CertificateID)
	if err != nil {
		return inbound, "", false, err
	}
	if domain.NodeID != nodeID || certificate.NodeID != nodeID ||
		certificate.ManagedDomainID != domain.ID {
		return inbound, "", false, fmt.Errorf("入站 %s 的托管 TLS 绑定归属不一致", inbound.Name)
	}
	if (certificate.Status != "active" && certificate.Status != "deploying") ||
		certificate.ActiveCertPath == "" || certificate.ActiveKeyPath == "" {
		return inbound, "", false, fmt.Errorf("入站 %s 的托管证书尚未就绪", inbound.Name)
	}
	if !supportsManagedTLS(inbound) {
		return inbound, "", false, fmt.Errorf("入站 %s 的协议 %s 不支持托管 TLS", inbound.Name, inbound.Protocol)
	}
	certPath := certificate.ActiveCertPath
	keyPath := certificate.ActiveKeyPath
	if certificate.Status == "deploying" {
		certPath = certificate.CandidateCertPath
		keyPath = certificate.CandidateKeyPath
	}
	if certPath == "" || keyPath == "" {
		return inbound, "", false, fmt.Errorf("入站 %s 的托管证书文件尚未就绪", inbound.Name)
	}
	params := cloneParams(inbound.Params)
	delete(params, "tls_cert_pem")
	delete(params, "tls_key_pem")
	params["tls_cert_path"] = certPath
	params["tls_key_path"] = keyPath
	params["server_name"] = domain.FQDN
	switch inbound.Protocol {
	case "vless", "vmess":
		params["tls_mode"] = "tls"
	}
	inbound.Params = params
	return inbound, domain.FQDN, true, nil
}

func supportsManagedTLS(inbound store.InboundConfig) bool {
	switch inbound.Protocol {
	case "trojan", "hysteria2", "tuic", "anytls":
		return true
	case "vless", "vmess":
		mode, _ := inbound.Params["tls_mode"].(string)
		return mode != "reality"
	default:
		return false
	}
}

func cloneParams(params map[string]any) map[string]any {
	out := make(map[string]any, len(params)+3)
	for key, value := range params {
		out[key] = value
	}
	return out
}

func enabledChains(chains []store.ProxyChain) []store.ProxyChain {
	out := make([]store.ProxyChain, 0, len(chains))
	for _, chain := range chains {
		if chain.Enabled {
			out = append(out, chain)
		}
	}
	return out
}

func ChainOutboundTag(chainID string, position int) string {
	id := strings.ReplaceAll(strings.ToLower(chainID), "-", "")
	if len(id) > 12 {
		id = id[:12]
	}
	return fmt.Sprintf("chain-%s-hop-%d-next", id, position)
}

// ResolveHopEndpoint resolves the address and credentials used to reach a hop.
func (b *Builder) ResolveHopEndpoint(chain store.ProxyChain, position int) (subscription.ProxyEndpoint, error) {
	if position < 0 || position >= len(chain.Hops) {
		return subscription.ProxyEndpoint{}, fmt.Errorf("代理链 %q 的跳点位置超出范围：%d", chain.Name, position+1)
	}
	hop := chain.Hops[position]
	node, err := b.Store.GetNode(hop.NodeID)
	if err != nil {
		return subscription.ProxyEndpoint{}, err
	}
	in, err := b.Store.GetInbound(hop.InboundID)
	if err != nil {
		return subscription.ProxyEndpoint{}, err
	}
	resolvedInbound, managedHostname, managed, err := b.ResolveManagedTLS(hop.NodeID, *in)
	if err != nil {
		return subscription.ProxyEndpoint{}, err
	}
	in = &resolvedInbound
	address := strings.TrimSpace(hop.DialAddress)
	if address == "" && managed {
		address = managedHostname
	}
	if address == "" {
		address = strings.TrimSpace(node.PublicAddress)
	}
	if address == "" {
		address = strings.TrimSpace(node.Address)
	}
	if address == "" {
		return subscription.ProxyEndpoint{}, fmt.Errorf("代理链 %q 第 %d 跳没有拨号地址", chain.Name, position+1)
	}
	port := hop.DialPort
	if port == 0 {
		listenPort, err := paramInt(in.Params, "port")
		if err != nil || listenPort < 1 || listenPort > 65535 {
			return subscription.ProxyEndpoint{}, fmt.Errorf("代理链 %q 第 %d 跳的入站端口无效", chain.Name, position+1)
		}
		port = store.MapPublicPort(node.PortMappings, listenPort)
	}
	skip := hop.TLSSkipVerify
	return subscription.ProxyEndpoint{
		Name:          chain.Name,
		Node:          *node,
		Inbound:       *in,
		Server:        address,
		Port:          port,
		Protocol:      in.Protocol,
		Params:        in.Params,
		TLSSkipVerify: &skip,
	}, nil
}

func paramInt(params map[string]any, key string) (int, error) {
	value, ok := params[key]
	if !ok {
		return 0, fmt.Errorf("缺少 %s", key)
	}
	switch n := value.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		return int(n), nil
	case json.Number:
		v, err := n.Int64()
		return int(v), err
	default:
		return 0, fmt.Errorf("%s 无效", key)
	}
}
