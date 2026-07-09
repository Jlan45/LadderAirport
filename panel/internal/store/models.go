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
	Status        string   `json:"status"`
	LastSeenUnix  int64    `json:"last_seen_unix"`
	ConfigHash    string   `json:"config_hash"`
	CreatedAtUnix int64    `json:"created_at_unix"`
	UpdatedAtUnix int64    `json:"updated_at_unix"`
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
}

type ConfigSnapshot struct {
	ID            string `json:"id"`
	NodeID        string `json:"node_id"`
	ConfigJSON    string `json:"config_json"`
	ConfigHash    string `json:"config_hash"`
	TaskID        string `json:"task_id,omitempty"`
	CreatedAtUnix int64  `json:"created_at_unix"`
}
