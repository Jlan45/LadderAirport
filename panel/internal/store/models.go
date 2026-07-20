package store

// PortMapping rewrites an agent listen port to the client-facing NAT/public port.
// Used only when rendering subscriptions; agent configs still use the inbound listen port.
type PortMapping struct {
	// ListenPort is the port the agent/inbound actually binds (params.port).
	ListenPort int `json:"listen_port"`
	// PublicPort is the external NAT-mapped port clients dial.
	// 0 or equal to ListenPort means no rewrite.
	PublicPort int `json:"public_port"`
}

type Node struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Address       string   `json:"address"` // control dial host (Panel → Agent)
	GRPCPort      int      `json:"grpc_port"` // control dial port (external mapped port if NAT)
	Token         string   `json:"token,omitempty"`
	Labels        []string `json:"labels"`
	TLSSkipVerify bool     `json:"tls_skip_verify"`
	CACertPEM     string   `json:"ca_cert_pem,omitempty"`
	// PublicAddress is the client-facing host for subscriptions (Clash/sing-box server).
	// Empty means fall back to Address. Host only — client ports come from inbound params
	// or PortMappings when listen/public ports differ.
	PublicAddress string `json:"public_address"`
	// PortMappings maps agent listen ports → external NAT ports for subscription clients.
	// Empty means subscribe with the inbound listen port as-is.
	PortMappings []PortMapping `json:"port_mappings"`
	// EgressInterface is the host NIC name for sing-box direct bind_interface.
	// Empty means OS default routing.
	EgressInterface string `json:"egress_interface"`
	Status          string `json:"status"` // online | unreachable | unauthorized | unknown
	LastSeenUnix    int64  `json:"last_seen_unix"`
	ConfigHash      string `json:"config_hash"`
	// Live monitoring cache (updated by fleet refresh / probe).
	RuntimeState   string  `json:"runtime_state"` // running | stopped | error | ""
	AgentVersion   string  `json:"agent_version"`
	SingboxVersion string  `json:"singbox_version"`
	Connections    int64   `json:"connections"`
	UplinkBytes    int64   `json:"uplink_bytes"`
	DownlinkBytes  int64   `json:"downlink_bytes"`
	CPUPercent     float64 `json:"cpu_percent"`
	MemoryRSSBytes int64   `json:"memory_rss_bytes"`
	MetricsAtUnix  int64   `json:"metrics_at_unix"`
	LastError      string  `json:"last_error,omitempty"`
	InboundCount   int     `json:"inbound_count,omitempty"` // filled by overview, not persisted
	CreatedAtUnix  int64   `json:"created_at_unix"`
	UpdatedAtUnix  int64   `json:"updated_at_unix"`
}

// NormalizePortMappings drops invalid/identity rows and keeps the last mapping per listen_port.
func NormalizePortMappings(in []PortMapping) []PortMapping {
	if len(in) == 0 {
		return []PortMapping{}
	}
	// Preserve first-seen order; later rows with the same listen_port overwrite.
	order := make([]int, 0, len(in))
	byListen := map[int]PortMapping{}
	for _, m := range in {
		if m.ListenPort < 1 || m.ListenPort > 65535 {
			continue
		}
		if m.PublicPort < 1 || m.PublicPort > 65535 || m.PublicPort == m.ListenPort {
			// Invalid or identity: drop any previous mapping for this listen port.
			if _, exists := byListen[m.ListenPort]; exists {
				delete(byListen, m.ListenPort)
				for i, p := range order {
					if p == m.ListenPort {
						order = append(order[:i], order[i+1:]...)
						break
					}
				}
			}
			continue
		}
		if _, exists := byListen[m.ListenPort]; !exists {
			order = append(order, m.ListenPort)
		}
		byListen[m.ListenPort] = PortMapping{ListenPort: m.ListenPort, PublicPort: m.PublicPort}
	}
	out := make([]PortMapping, 0, len(order))
	for _, p := range order {
		if m, ok := byListen[p]; ok {
			out = append(out, m)
		}
	}
	return out
}

// MapPublicPort returns the client-facing port for an agent listen port.
// Falls back to listenPort when no mapping is configured.
// Deprecated for new UI: prefer per-inbound PublicPort on NodeInboundAttachment.
func MapPublicPort(mappings []PortMapping, listenPort int) int {
	for _, m := range mappings {
		if m.ListenPort == listenPort && m.PublicPort >= 1 && m.PublicPort <= 65535 {
			return m.PublicPort
		}
	}
	return listenPort
}

// NodeInboundAttachment is an inbound linked to a node, with optional client-facing NAT overrides.
// Agent still listens on inbound params.port; PublicAddress/PublicPort only affect subscriptions.
type NodeInboundAttachment struct {
	InboundConfig
	// PublicAddress overrides node.public_address / address for this inbound in subscriptions.
	// Empty falls back to node-level client host.
	PublicAddress string `json:"public_address"`
	// PublicPort is the external NAT-mapped port clients dial.
	// 0 means use inbound listen port (params.port), then node port_mappings if any.
	PublicPort int `json:"public_port"`
}

// NodeInboundBinding is the write payload for attaching an inbound with NAT overrides.
type NodeInboundBinding struct {
	InboundID     string `json:"inbound_id"`
	PublicAddress string `json:"public_address"`
	PublicPort    int    `json:"public_port"`
}

type InboundConfig struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Protocol      string         `json:"protocol"` // shadowsocks|trojan|vless|hysteria2|tuic|anytls|vmess
	Params        map[string]any `json:"params"`
	Enabled       bool           `json:"enabled"`
	CreatedAtUnix int64          `json:"created_at_unix"`
	UpdatedAtUnix int64          `json:"updated_at_unix"`
}

type Task struct {
	ID            string           `json:"id"`
	Type          string           `json:"type"`   // apply|start|stop
	Status        string           `json:"status"` // pending|running|success|partial|failed
	NodeIDs       []string         `json:"node_ids"`
	Results       []TaskNodeResult `json:"results"`
	CreatedAtUnix int64            `json:"created_at_unix"`
	UpdatedAtUnix int64            `json:"updated_at_unix"`
}

type TaskNodeResult struct {
	NodeID  string `json:"node_id"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

type Settings struct {
	AdminPasswordHash string `json:"-"`
	DefaultAgentToken string `json:"default_agent_token"`
	GRPCTimeoutSec    int    `json:"grpc_timeout_sec"`
	MaxConcurrency    int    `json:"max_concurrency"`
	ListenAddr        string `json:"listen_addr"`
	// PublicBaseURL is used to render full subscription links (e.g. https://panel.example.com).
	PublicBaseURL string `json:"public_base_url"`
}

// Subscription is a client-facing share link (Clash YAML or sing-box JSON).
type Subscription struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Format        string   `json:"format"`      // clash | singbox
	Token         string   `json:"token"`       // URL secret for public /sub/{token}
	InboundIDs    []string `json:"inbound_ids"` // empty = all enabled inbounds attached to any node
	Enabled       bool     `json:"enabled"`
	CreatedAtUnix int64    `json:"created_at_unix"`
	UpdatedAtUnix int64    `json:"updated_at_unix"`
}

type ConfigSnapshot struct {
	ID            string `json:"id"`
	NodeID        string `json:"node_id"`
	ConfigJSON    string `json:"config_json"`
	ConfigHash    string `json:"config_hash"`
	TaskID        string `json:"task_id,omitempty"`
	CreatedAtUnix int64  `json:"created_at_unix"`
}
