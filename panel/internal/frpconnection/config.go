package frpconnection

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	User                         string `json:"user"`
	ServerAddr                   string `json:"server_addr"`
	ServerPort                   int    `json:"server_port"`
	RemotePort                   int    `json:"remote_port"`
	Token                        string `json:"token"`
	ProxyName                    string `json:"proxy_name"`
	TLSEnable                    *bool  `json:"tls_enable"`
	TLSDisableCustomTLSFirstByte *bool  `json:"tls_disable_custom_tls_first_byte"`
}

type clientTOML struct {
	User       string `toml:"user"`
	ServerAddr string `toml:"serverAddr"`
	ServerPort int    `toml:"serverPort"`
	Auth       struct {
		Token string `toml:"token"`
	} `toml:"auth"`
	Transport struct {
		TLS struct {
			Enable                    *bool `toml:"enable"`
			DisableCustomTLSFirstByte *bool `toml:"disableCustomTLSFirstByte"`
		} `toml:"tls"`
	} `toml:"transport"`
	Proxies []struct {
		Name       string `toml:"name"`
		Type       string `toml:"type"`
		LocalIP    string `toml:"localIP"`
		LocalPort  int    `toml:"localPort"`
		RemotePort int    `toml:"remotePort"`
	} `toml:"proxies"`
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
	if strings.HasPrefix(strings.TrimSpace(raw), "{") {
		// v0.15.4 and earlier stored the generated field form as JSON.
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return Config{}, true, fmt.Errorf("旧版 FRPC JSON 格式无效：%w", err)
		}
	} else {
		var document clientTOML
		if err := toml.NewDecoder(strings.NewReader(raw)).DisallowUnknownFields().Decode(&document); err != nil {
			return Config{}, true, fmt.Errorf("FRPC TOML 含无效或暂不支持的字段：%w", err)
		}
		if len(document.Proxies) != 1 {
			return Config{}, true, fmt.Errorf("每个入站的 FRPC 配置必须恰好包含一个 [[proxies]]")
		}
		proxy := document.Proxies[0]
		if proxy.Type != "tcp" {
			return Config{}, true, fmt.Errorf("当前仅支持 type = \"tcp\" 的 FRPC 代理")
		}
		cfg = Config{
			User: document.User, ServerAddr: document.ServerAddr, ServerPort: document.ServerPort,
			RemotePort: proxy.RemotePort, Token: document.Auth.Token, ProxyName: proxy.Name,
			TLSEnable:                    document.Transport.TLS.Enable,
			TLSDisableCustomTLSFirstByte: document.Transport.TLS.DisableCustomTLSFirstByte,
		}
	}
	if strings.TrimSpace(cfg.ServerAddr) == "" || strings.TrimSpace(cfg.Token) == "" ||
		cfg.ServerPort < 1 || cfg.ServerPort > 65535 || cfg.RemotePort < 1 || cfg.RemotePort > 65535 {
		return Config{}, true, fmt.Errorf("FRPS 连接信息需要有效的服务端地址、控制端口、对外端口和 Token")
	}
	if strings.ContainsAny(cfg.User, " \t\r\n") || strings.ContainsAny(cfg.ProxyName, " \t\r\n") {
		return Config{}, true, fmt.Errorf("FRP user 和 proxy name 不能包含空白字符")
	}
	return cfg, true, nil
}
