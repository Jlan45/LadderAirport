package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/ladderairport/agent/internal/control"
	"github.com/ladderairport/agent/internal/managementpki"
	"github.com/ladderairport/agent/internal/version"
	"github.com/ladderairport/pkg/auth"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	listen := flag.String("listen", ":50051", "gRPC 监听地址")
	token := flag.String("token", "changeme", "AgentControl 共享 Bearer 令牌")
	tlsCert := flag.String("tls-cert", "", "Panel 签发的 TLS 证书文件（必填）")
	tlsKey := flag.String("tls-key", "", "Agent TLS 私钥文件（必填）")
	tlsClientCA := flag.String("tls-client-ca", "", "Panel 管理 CA 证书包（必填）")
	panelURL := flag.String("panel-url", "", "证书续签使用的 Panel 基础 URL（必填）")
	nodeID := flag.String("node-id", "", "证书身份对应的 Panel 节点 ID（必填）")
	reportAddress := flag.String("report-address", "", "证书续签时上报给 Panel 的地址")
	tlsSANs := flag.String("tls-sans", "", "续签时保留的逗号分隔 DNS/IP SAN")
	dataDir := flag.String("data-dir", "", "配置和状态缓存目录（可选）")
	showVersion := flag.Bool("version", false, "显示版本后退出")
	flag.Parse()

	if *showVersion {
		fmt.Printf("ladder-agent %s", version.Version)
		if version.Commit != "" && version.Commit != "unknown" {
			fmt.Printf(" (%s)", version.Commit)
		}
		fmt.Println()
		return
	}

	if *token == "" {
		log.Fatal("必须提供 -token")
	}
	if *tlsCert == "" || *tlsKey == "" || *tlsClientCA == "" || *panelURL == "" || *nodeID == "" {
		log.Fatal("必须同时提供 -tls-cert、-tls-key、-tls-client-ca、-panel-url 和 -node-id")
	}
	if err := managementpki.ParsePanelURL(*panelURL); err != nil {
		log.Fatal(err)
	}
	if *dataDir != "" {
		if err := os.MkdirAll(*dataDir, 0o755); err != nil {
			log.Fatalf("创建数据目录失败：%v", err)
		}
	}

	rt := control.NewBoxRuntime(*dataDir)
	logs := control.NewLogBuf(0)
	singboxVer := control.SingboxVersion()
	agentVersion := version.Version
	log.Printf("运行模式=内置代理实例 Agent版本=%s sing-box版本=%s 数据目录=%q", agentVersion, singboxVer, *dataDir)

	srv := control.NewServer(rt, agentVersion, singboxVer, logs)

	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(auth.UnaryServerInterceptor(*token)),
		grpc.StreamInterceptor(auth.StreamServerInterceptor(*token)),
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	host, portText, _ := net.SplitHostPort(*listen)
	port, _ := strconv.Atoi(portText)
	if *reportAddress != "" {
		host = *reportAddress
	}
	certManager, err := managementpki.New(managementpki.Config{
		PanelURL: *panelURL,
		NodeID:   *nodeID,
		Token:    *token,
		CertPath: *tlsCert,
		KeyPath:  *tlsKey,
		CAPath:   *tlsClientCA,
		Address:  host,
		GRPCPort: port,
		SANs:     strings.Split(*tlsSANs, ","),
	})
	if err != nil {
		log.Fatalf("加载管理面 TLS 失败：%v", err)
	}
	tlsConfig := &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: certManager.GetCertificate,
	}
	pool, err := managementpki.ClientCAPool(*tlsClientCA)
	if err != nil {
		log.Fatalf("加载 Panel 客户端 CA 失败：%v", err)
	}
	tlsConfig.ClientCAs = pool
	tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	tlsConfig.VerifyPeerCertificate = managementpki.VerifyPanelIdentity
	tlsConfig.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
		currentPool, err := managementpki.ClientCAPool(*tlsClientCA)
		if err != nil {
			return nil, err
		}
		current := tlsConfig.Clone()
		current.GetConfigForClient = nil
		current.ClientCAs = currentPool
		return current, nil
	}
	log.Printf("mTLS=强制 证书=%s 客户端CA=%s", *tlsCert, *tlsClientCA)
	opts = append(opts, grpc.Creds(credentials.NewTLS(tlsConfig)))
	go certManager.Run(runCtx)

	gs := grpc.NewServer(opts...)
	agentv1.RegisterAgentControlServer(gs, srv)

	lis, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("监听 %s 失败：%v", *listen, err)
	}
	log.Printf("AgentControl 正在监听 %s", lis.Addr())

	errCh := make(chan error, 1)
	go func() {
		errCh <- gs.Serve(lis)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("收到信号 %v，正在停止", sig)
		cancelRun()
		gs.GracefulStop()
		_ = rt.Stop(context.Background())
	case err := <-errCh:
		if err != nil {
			log.Fatalf("Agent 服务运行失败：%v", err)
		}
	}
	fmt.Fprintln(os.Stderr, "bye")
}
