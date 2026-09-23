package frpconnection

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Config struct {
	ServerAddr string `json:"server_addr"`
	ServerPort int    `json:"server_port"`
	RemotePort int    `json:"remote_port"`
	Token      string `json:"token"`
}

func ParseParams(params map[string]any) (Config, bool, error) {
	if params["frp_enabled"] != true {
		return Config{}, false, nil
	}
	raw, ok := params["frpc_config"].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return Config{}, true, fmt.Errorf("启用 FRP 时必须填写 FRPS 连接信息")
	}
	var cfg Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return Config{}, true, fmt.Errorf("FRPS 连接信息格式无效：%w", err)
	}
	if strings.TrimSpace(cfg.ServerAddr) == "" || strings.TrimSpace(cfg.Token) == "" ||
		cfg.ServerPort < 1 || cfg.ServerPort > 65535 || cfg.RemotePort < 1 || cfg.RemotePort > 65535 {
		return Config{}, true, fmt.Errorf("FRPS 连接信息需要有效的服务端地址、控制端口、对外端口和 Token")
	}
	return cfg, true, nil
}
