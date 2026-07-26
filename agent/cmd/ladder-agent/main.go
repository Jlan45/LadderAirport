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
	listen := flag.String("listen", ":50051", "gRPC listen address")
	token := flag.String("token", "changeme", "shared bearer token for AgentControl")
	tlsCert := flag.String("tls-cert", "", "Panel-issued TLS certificate file (required)")
	tlsKey := flag.String("tls-key", "", "Agent TLS private key file (required)")
	tlsClientCA := flag.String("tls-client-ca", "", "Panel management CA bundle (required)")
	panelURL := flag.String("panel-url", "", "Panel base URL for certificate renewal (required)")
	nodeID := flag.String("node-id", "", "Panel node ID for certificate identity (required)")
	reportAddress := flag.String("report-address", "", "address reported to Panel during certificate renewal")
	tlsSANs := flag.String("tls-sans", "", "comma-separated DNS/IP SANs preserved during renewal")
	dataDir := flag.String("data-dir", "", "directory for cached config/state (optional)")
	showVersion := flag.Bool("version", false, "print version and exit")
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
		log.Fatal("-token is required")
	}
	if *tlsCert == "" || *tlsKey == "" || *tlsClientCA == "" || *panelURL == "" || *nodeID == "" {
		log.Fatal("-tls-cert, -tls-key, -tls-client-ca, -panel-url and -node-id are required")
	}
	if err := managementpki.ParsePanelURL(*panelURL); err != nil {
		log.Fatal(err)
	}
	if *dataDir != "" {
		if err := os.MkdirAll(*dataDir, 0o755); err != nil {
			log.Fatalf("create data-dir: %v", err)
		}
	}

	rt := control.NewBoxRuntime(*dataDir)
	logs := control.NewLogBuf(0)
	singboxVer := control.SingboxVersion()
	agentVersion := version.Version
	log.Printf("runtime=box agent_version=%s singbox_version=%s data_dir=%q", agentVersion, singboxVer, *dataDir)

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
		log.Fatalf("load management TLS: %v", err)
	}
	tlsConfig := &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: certManager.GetCertificate,
	}
	pool, err := managementpki.ClientCAPool(*tlsClientCA)
	if err != nil {
		log.Fatalf("load Panel client CA: %v", err)
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
	log.Printf("mtls=required cert=%s client_ca=%s", *tlsCert, *tlsClientCA)
	opts = append(opts, grpc.Creds(credentials.NewTLS(tlsConfig)))
	go certManager.Run(runCtx)

	gs := grpc.NewServer(opts...)
	agentv1.RegisterAgentControlServer(gs, srv)

	lis, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("listen %s: %v", *listen, err)
	}
	log.Printf("AgentControl listening on %s", lis.Addr())

	errCh := make(chan error, 1)
	go func() {
		errCh <- gs.Serve(lis)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("signal %v, shutting down", sig)
		cancelRun()
		gs.GracefulStop()
		_ = rt.Stop(context.Background())
	case err := <-errCh:
		if err != nil {
			log.Fatalf("serve: %v", err)
		}
	}
	fmt.Fprintln(os.Stderr, "bye")
}
