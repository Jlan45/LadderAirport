package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	acmeservice "github.com/ladderairport/panel/internal/acme"
	"github.com/ladderairport/panel/internal/api"
	"github.com/ladderairport/panel/internal/automation"
	"github.com/ladderairport/panel/internal/batch"
	"github.com/ladderairport/panel/internal/certmanager"
	"github.com/ladderairport/panel/internal/dnsprovider"
	"github.com/ladderairport/panel/internal/dnsproviders"
	"github.com/ladderairport/panel/internal/dnsreconcile"
	"github.com/ladderairport/panel/internal/pki"
	"github.com/ladderairport/panel/internal/proxychain"
	"github.com/ladderairport/panel/internal/secretstore"
	"github.com/ladderairport/panel/internal/store"
	"github.com/ladderairport/panel/internal/subscription"
	"github.com/ladderairport/panel/internal/version"
)

func main() {
	dbPath := flag.String("db", "./data/panel.db", "SQLite 数据库路径")
	listen := flag.String("listen", "", "HTTP 监听地址（默认读取 settings.listen_addr 或使用 :8080）")
	sessionSecret := flag.String("session-secret", "", "JWT 会话 HMAC 密钥（为空时随机生成）")
	bootstrap := flag.Bool("bootstrap", true, "启动时向所有已注册节点下发配置并启动 sing-box")
	bootstrapTimeout := flag.Duration("bootstrap-timeout", 3*time.Minute, "首次启动下发的超时时间")
	bootstrapRetry := flag.Bool("bootstrap-retry", true, "定期重试尚未在线或运行节点的下发与启动")
	bootstrapRetryInterval := flag.Duration("bootstrap-retry-interval", 30*time.Second, "启动重试间隔")
	showVersion := flag.Bool("version", false, "显示版本后退出")
	pkiDir := flag.String("pki-dir", "", "管理 PKI 目录（默认：<数据库目录>/pki）")
	pkiRotateIntermediate := flag.Bool("pki-rotate-intermediate", false, "轮换在线中间 CA 和 Panel 客户端证书后退出")
	credentialsKeyFile := flag.String(
		"credentials-key-file",
		"",
		"DNS/ACME 凭据主密钥文件（默认：<数据库目录>/secrets/credentials.key）",
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("ladder-panel %s", version.Version)
		if version.Commit != "" && version.Commit != "unknown" {
			fmt.Printf(" (%s)", version.Commit)
		}
		fmt.Println()
		return
	}

	if dir := filepath.Dir(*dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("创建数据库目录失败：%v", err)
		}
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("打开数据存储失败：%v", err)
	}
	defer func() { _ = st.Close() }()

	keyFile := *credentialsKeyFile
	if keyFile == "" {
		keyFile = filepath.Join(filepath.Dir(*dbPath), "secrets", "credentials.key")
	}
	credentialsKey, err := secretstore.LoadOrCreateKey(secretstore.KeyOptions{
		EnvironmentValue: os.Getenv("LADDER_CREDENTIALS_KEY"),
		FilePath:         keyFile,
	})
	if err != nil {
		log.Fatalf("打开 DNS/ACME 凭据主密钥失败：%v", err)
	}
	secrets, err := secretstore.New(credentialsKey)
	if err != nil {
		log.Fatalf("初始化 DNS/ACME 凭据保险箱失败：%v", err)
	}
	log.Printf("DNS/ACME 凭据保险箱已启用（密钥来源=%s）", credentialKeySource(keyFile))
	dnsRegistry := dnsprovider.NewRegistry()
	if err := dnsproviders.RegisterBuiltins(dnsRegistry); err != nil {
		log.Fatalf("注册 DNS 供应商失败：%v", err)
	}

	dir := *pkiDir
	if dir == "" {
		dir = filepath.Join(filepath.Dir(*dbPath), "pki")
	}
	ca, err := pki.Open(dir)
	if err != nil {
		log.Fatalf("打开管理 PKI 失败：%v", err)
	}
	if *pkiRotateIntermediate {
		if err := ca.RotateIntermediate(time.Now()); err != nil {
			log.Fatalf("轮换管理中间 CA 失败：%v", err)
		}
		log.Printf("管理中间 CA 已轮换，请将 %s 移回离线存储", filepath.Join(dir, "offline", "root-ca.key"))
		return
	}
	log.Printf("管理 PKI 已启用（目录=%s，中间 CA 到期时间=%s）", dir, time.Unix(ca.Status().IntermediateNotAfterUnix, 0).Format(time.RFC3339))
	go ca.Run(context.Background())

	if err := api.EnsureAdminPassword(st); err != nil {
		log.Fatalf("初始化管理员密码失败：%v", err)
	}

	settings, err := st.GetSettings()
	if err != nil {
		log.Fatalf("读取系统设置失败：%v", err)
	}

	secret := []byte(*sessionSecret)
	if len(secret) == 0 {
		secret, err = randomSecret(32)
		if err != nil {
			log.Fatalf("生成会话密钥失败：%v", err)
		}
		log.Printf("未设置会话密钥，已生成临时密钥（重启后现有会话将失效）")
	}

	runner := batch.NewRunner(st, func() string {
		cur, err := st.GetSettings()
		if err != nil {
			return settings.DefaultAgentToken
		}
		return cur.DefaultAgentToken
	})
	runner.PKI = ca
	if settings.GRPCTimeoutSec > 0 {
		runner.Timeout = time.Duration(settings.GRPCTimeoutSec) * time.Second
	}
	if settings.MaxConcurrency > 0 {
		runner.MaxConcurrency = settings.MaxConcurrency
	}
	dnsService := &dnsreconcile.Service{
		Store:     st,
		Secrets:   secrets,
		Providers: dnsRegistry,
		Timeout:   runner.Timeout,
		DefaultToken: func() string {
			cur, err := st.GetSettings()
			if err != nil {
				return settings.DefaultAgentToken
			}
			return cur.DefaultAgentToken
		},
		DialAgent: func(ctx context.Context, node store.Node, token string) (dnsreconcile.Agent, error) {
			return runner.DialClient(ctx, node, token)
		},
	}
	acmeService := &acmeservice.Service{Secrets: secrets}
	certificateService := &certmanager.Service{
		Store: st, Secrets: secrets, Providers: dnsRegistry, ACME: acmeService,
		Timeout: 10 * time.Minute, ConfigBuilder: runner.ConfigBuilder,
		Coordinator: runner.Coordinator,
		DefaultToken: func() string {
			cur, err := st.GetSettings()
			if err != nil {
				return settings.DefaultAgentToken
			}
			return cur.DefaultAgentToken
		},
		DialAgent: func(ctx context.Context, node store.Node, token string) (certmanager.Agent, error) {
			return runner.DialClient(ctx, node, token)
		},
	}
	automationRunner := &automation.Runner{
		Store: st, DNS: dnsService, Certificates: certificateService,
		Owner: automationOwner(),
	}

	agg := subscription.NewAggregator(st)
	chainService := proxychain.NewService(st, runner.ConfigBuilder, runner.Coordinator)
	chainService.PKI = ca
	srv := &api.Server{
		Store:        st,
		Runner:       runner,
		Secret:       secret,
		Aggregator:   agg,
		Chains:       chainService,
		PKI:          ca,
		Secrets:      secrets,
		DNSProviders: dnsRegistry,
		ACME:         acmeService,
	}

	addr := *listen
	if addr == "" {
		addr = settings.ListenAddr
	}
	if addr == "" {
		addr = ":8080"
	}

	// Auto push configs + start agents (background; does not block HTTP).
	if *bootstrap {
		go func() {
			// Small delay so ListenAndServe is up and agents have a moment if co-started.
			time.Sleep(500 * time.Millisecond)
			ctx, cancel := context.WithTimeout(context.Background(), *bootstrapTimeout)
			defer cancel()
			log.Printf("启动下发：开始（超时=%s）", *bootstrapTimeout)
			if err := runner.BootstrapAll(ctx); err != nil {
				log.Printf("启动下发：完成但发生错误：%v", err)
			} else {
				log.Printf("启动下发：完成")
			}
		}()
	} else {
		log.Printf("启动下发：已禁用（-bootstrap=false）")
	}

	// Keep retrying nodes that come online later (agent started after panel, etc.).
	if *bootstrap && *bootstrapRetry {
		go func() {
			log.Printf("启动重试：已启用（间隔=%s）", *bootstrapRetryInterval)
			runner.RunBootstrapRetryLoop(context.Background(), *bootstrapRetryInterval)
		}()
	} else if *bootstrap {
		log.Printf("启动重试：已禁用（-bootstrap-retry=false）")
	}

	// Background refresh of external subscription sources.
	go func() {
		log.Printf("外部源后台刷新已启用（间隔=%s）", subscription.BackgroundTick)
		agg.RunBackground(context.Background(), subscription.BackgroundTick)
	}()
	go chainService.RunProbeLoop(context.Background())
	go automationRunner.Run(context.Background())

	log.Printf("Panel 正在监听 %s（数据库=%s，版本=%s）", addr, *dbPath, version.Version)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatalf("启动监听失败：%v", err)
	}
}

func automationOwner() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

func credentialKeySource(file string) string {
	if os.Getenv("LADDER_CREDENTIALS_KEY") != "" {
		return "LADDER_CREDENTIALS_KEY"
	}
	return file
}

func randomSecret(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}
