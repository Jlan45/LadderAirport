package frpcbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/fatedier/frp/client"
	"github.com/fatedier/frp/pkg/config/source"
	v1 "github.com/fatedier/frp/pkg/config/v1"
)

type ConnectionConfig struct {
	ServerAddr string `json:"server_addr"`
	ServerPort int    `json:"server_port"`
	RemotePort int    `json:"remote_port"`
	LocalPort  int    `json:"local_port"`
	Token      string `json:"token"`
}

type Runtime struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (r *Runtime) Close() {
	if r == nil {
		return
	}
	r.cancel()
	<-r.done
}

// Start forwards each FRPC proxy to a loopback-only sing-box TCP listener.
// The caller closes the returned runtime before closing box.
func Start(ctx context.Context, configurations map[string]string) (*Runtime, error) {
	services := make([]*client.Service, 0, len(configurations))
	for tag, raw := range configurations {
		var connection ConnectionConfig
		if err := json.Unmarshal([]byte(raw), &connection); err != nil {
			return nil, fmt.Errorf("入站 %q 的 FRPS 连接信息无效：%w", tag, err)
		}
		if strings.TrimSpace(connection.ServerAddr) == "" || connection.ServerPort < 1 || connection.ServerPort > 65535 ||
			strings.TrimSpace(connection.Token) == "" || connection.RemotePort < 1 || connection.RemotePort > 65535 ||
			connection.LocalPort < 49152 || connection.LocalPort > 65535 {
			return nil, fmt.Errorf("入站 %q 的 FRPS 连接信息必须包含地址、控制端口、Token、对外端口和本地高位端口", tag)
		}
		loginFailExit := false
		common := &v1.ClientCommonConfig{
			ServerAddr:    connection.ServerAddr,
			ServerPort:    connection.ServerPort,
			ClientID:      "ladder-" + tag,
			LoginFailExit: &loginFailExit,
			Auth:          v1.AuthClientConfig{Token: connection.Token},
		}
		if err := common.Complete(); err != nil {
			return nil, fmt.Errorf("入站 %q 的 FRPC 公共配置无效：%w", tag, err)
		}
		proxy := &v1.TCPProxyConfig{ProxyBaseConfig: v1.ProxyBaseConfig{
			Name: tag, Type: "tcp", ProxyBackend: v1.ProxyBackend{LocalIP: "127.0.0.1", LocalPort: connection.LocalPort},
		}, RemotePort: connection.RemotePort}
		configurationSource := source.NewConfigSource()
		if err := configurationSource.ReplaceAll([]v1.ProxyConfigurer{proxy}, nil); err != nil {
			return nil, err
		}
		service, err := client.NewService(client.ServiceOptions{
			Common:                 common,
			ConfigSourceAggregator: source.NewAggregator(configurationSource),
		})
		if err != nil {
			return nil, fmt.Errorf("创建入站 %q 的 FRPC 服务失败：%w", tag, err)
		}
		services = append(services, service)
	}
	runCtx, cancel := context.WithCancel(ctx)
	runtime := &Runtime{cancel: cancel, done: make(chan struct{})}
	var workers sync.WaitGroup
	for _, service := range services {
		workers.Add(1)
		go func(service *client.Service) {
			defer workers.Done()
			if err := service.Run(runCtx); err != nil && runCtx.Err() == nil {
				log.Printf("FRPC 连接退出：%v", err)
			}
		}(service)
	}
	go func() { workers.Wait(); close(runtime.done) }()
	return runtime, nil
}
