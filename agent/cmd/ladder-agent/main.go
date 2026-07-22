package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/ladderairport/agent/internal/control"
	"github.com/ladderairport/agent/internal/version"
	"github.com/ladderairport/pkg/auth"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	listen := flag.String("listen", ":50051", "gRPC listen address")
	token := flag.String("token", "changeme", "shared bearer token for AgentControl")
	tlsCert := flag.String("tls-cert", "", "TLS certificate file (optional)")
	tlsKey := flag.String("tls-key", "", "TLS private key file (optional)")
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

	useTLS := *tlsCert != "" && *tlsKey != ""
	if useTLS {
		creds, err := credentials.NewServerTLSFromFile(*tlsCert, *tlsKey)
		if err != nil {
			log.Fatalf("load TLS credentials: %v", err)
		}
		opts = append(opts, grpc.Creds(creds))
		log.Printf("tls=enabled cert=%s", *tlsCert)
	} else {
		if *tlsCert != "" || *tlsKey != "" {
			log.Printf("warning: both -tls-cert and -tls-key required for TLS; falling back to plaintext")
		}
		log.Printf("warning: listening without TLS (lab mode)")
	}

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
		gs.GracefulStop()
		_ = rt.Stop(context.Background())
	case err := <-errCh:
		if err != nil {
			log.Fatalf("serve: %v", err)
		}
	}
	fmt.Fprintln(os.Stderr, "bye")
}
