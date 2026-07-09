package store

type Node struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Address       string   `json:"address"`
	GRPCPort      int      `json:"grpc_port"`
	Token         string   `json:"token,omitempty"`
	Labels        []string `json:"labels"`
	TLSSkipVerify bool     `json:"tls_skip_verify"`
	CACertPEM     string   `json:"ca_cert_pem,omitempty"`
	Status        string   `json:"status"` // online | unreachable | unauthorized | unknown
	LastSeenUnix  int64    `json:"last_seen_unix"`
	ConfigHash    string   `json:"config_hash"`
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

type InboundConfig struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Protocol      string         `json:"protocol"` // shadowsocks|trojan|vless|hysteria2
	Params        map[string]any `json:"params"`
	Enabled       bool           `json:"enabled"`
	CreatedAtUnix int64          `json:"created_at_unix"`
	UpdatedAtUnix int64          `json:"updated_at_unix"`
}

type Task struct {
	ID            string           `json:"id"`
	Type          string           `json:"type"` // apply|start|stop
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
	Format        string   `json:"format"` // clash | singbox
	Token         string   `json:"token"`  // URL secret for public /sub/{token}
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
