package frpsruntime

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/fatedier/frp/pkg/config/types"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/config/v1/validation"
	"github.com/fatedier/frp/pkg/policy/security"
)

const (
	DefaultBindAddr = "0.0.0.0"
	DefaultBindPort = 7000
)

type PortRange struct {
	Start int `json:"start,omitempty"`
	End   int `json:"end,omitempty"`
}

type Config struct {
	Enabled           bool        `json:"enabled"`
	BindAddr          string      `json:"bind_addr"`
	BindPort          int         `json:"bind_port"`
	ProxyBindAddr     string      `json:"proxy_bind_addr"`
	AllowPorts        []PortRange `json:"allow_ports"`
	AuthToken         string      `json:"auth_token"`
	TLSForce          bool        `json:"tls_force"`
	MaxPortsPerClient int64       `json:"max_ports_per_client"`
}

func (c Config) Normalize() Config {
	c.BindAddr = strings.TrimSpace(c.BindAddr)
	if c.BindAddr == "" {
		c.BindAddr = DefaultBindAddr
	}
	if c.BindPort == 0 {
		c.BindPort = DefaultBindPort
	}
	c.ProxyBindAddr = strings.TrimSpace(c.ProxyBindAddr)
	if c.ProxyBindAddr == "" {
		c.ProxyBindAddr = c.BindAddr
	}
	c.AuthToken = strings.TrimSpace(c.AuthToken)
	if c.AllowPorts == nil {
		c.AllowPorts = []PortRange{}
	}
	return c
}

func (c Config) Validate() error {
	c = c.Normalize()
	if !c.Enabled {
		return nil
	}
	if net.ParseIP(c.BindAddr) == nil {
		return fmt.Errorf("FRPS bind_addr 必须是有效 IP 地址")
	}
	if net.ParseIP(c.ProxyBindAddr) == nil {
		return fmt.Errorf("FRPS proxy_bind_addr 必须是有效 IP 地址")
	}
	if err := validatePort(c.BindPort, "bind_port"); err != nil {
		return err
	}
	if c.AuthToken == "" {
		return fmt.Errorf("FRPS auth_token 不能为空")
	}
	if !c.TLSForce {
		return fmt.Errorf("FRPS 必须启用 tls_force")
	}
	if len(c.AllowPorts) == 0 {
		return fmt.Errorf("FRPS allow_ports 不能为空")
	}
	if c.MaxPortsPerClient < 0 {
		return fmt.Errorf("FRPS max_ports_per_client 不能小于 0")
	}
	for i, portRange := range c.AllowPorts {
		if err := portRange.Validate(); err != nil {
			return fmt.Errorf("FRPS allow_ports 第 %d 项无效：%w", i+1, err)
		}
		if portRange.Contains(c.BindPort) {
			return fmt.Errorf("FRPS allow_ports 不能包含控制端口 %d", c.BindPort)
		}
	}
	_, err := c.serverConfig()
	return err
}

func (r PortRange) Validate() error {
	if err := validatePort(r.Start, "start"); err != nil {
		return err
	}
	if r.End == 0 {
		r.End = r.Start
	}
	if err := validatePort(r.End, "end"); err != nil {
		return err
	}
	if r.End < r.Start {
		return fmt.Errorf("end 不能小于 start")
	}
	return nil
}

func (r PortRange) Contains(port int) bool {
	end := r.End
	if end == 0 {
		end = r.Start
	}
	return port >= r.Start && port <= end
}

func validatePort(port int, name string) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("%s 必须在 1 到 65535 之间", name)
	}
	return nil
}

func (c Config) serverConfig() (*v1.ServerConfig, error) {
	c = c.Normalize()
	detailedErrors := false
	cfg := &v1.ServerConfig{
		BindAddr:               c.BindAddr,
		BindPort:               c.BindPort,
		ProxyBindAddr:          c.ProxyBindAddr,
		MaxPortsPerClient:      c.MaxPortsPerClient,
		DetailedErrorsToClient: &detailedErrors,
		Auth: v1.AuthServerConfig{
			Method: v1.AuthMethodToken,
			AdditionalScopes: []v1.AuthScope{
				v1.AuthScopeHeartBeats,
				v1.AuthScopeNewWorkConns,
			},
			Token: c.AuthToken,
		},
		Transport: v1.ServerTransportConfig{
			TLS: v1.TLSServerConfig{Force: c.TLSForce},
		},
		Log: v1.LogConfig{
			To:                "console",
			Level:             "info",
			DisablePrintColor: true,
		},
	}
	for _, portRange := range c.AllowPorts {
		if portRange.End == 0 || portRange.End == portRange.Start {
			cfg.AllowPorts = append(cfg.AllowPorts, types.PortsRange{Single: portRange.Start})
		} else {
			cfg.AllowPorts = append(cfg.AllowPorts, types.PortsRange{
				Start: portRange.Start,
				End:   portRange.End,
			})
		}
	}
	if err := cfg.Complete(); err != nil {
		return nil, fmt.Errorf("补全 FRPS 配置失败：%w", err)
	}
	validator := validation.NewConfigValidator(security.NewUnsafeFeatures(nil))
	if warning, err := validator.ValidateServerConfig(cfg); err != nil {
		return nil, fmt.Errorf("校验 FRPS 配置失败：%w", err)
	} else if warning != nil {
		return nil, fmt.Errorf("FRPS 配置警告：%w", warning)
	}
	return cfg, nil
}

func (c Config) canonicalJSON() ([]byte, error) {
	normalized := c.Normalize()
	return json.Marshal(normalized)
}
