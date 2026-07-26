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

	"github.com/ladderairport/panel/internal/api"
	"github.com/ladderairport/panel/internal/batch"
	"github.com/ladderairport/panel/internal/pki"
	"github.com/ladderairport/panel/internal/proxychain"
	"github.com/ladderairport/panel/internal/store"
	"github.com/ladderairport/panel/internal/subscription"
	"github.com/ladderairport/panel/internal/version"
)

func main() {
	dbPath := flag.String("db", "./data/panel.db", "SQLite database path")
	listen := flag.String("listen", "", "HTTP listen address (default: settings.listen_addr or :8080)")
	sessionSecret := flag.String("session-secret", "", "JWT session HMAC secret (random if empty)")
	bootstrap := flag.Bool("bootstrap", true, "on start, apply configs and start sing-box on all registered nodes")
	bootstrapTimeout := flag.Duration("bootstrap-timeout", 3*time.Minute, "timeout for initial startup bootstrap")
	bootstrapRetry := flag.Bool("bootstrap-retry", true, "periodically retry apply+start for nodes not yet online/running")
	bootstrapRetryInterval := flag.Duration("bootstrap-retry-interval", 30*time.Second, "interval between bootstrap retries")
	showVersion := flag.Bool("version", false, "print version and exit")
	pkiDir := flag.String("pki-dir", "", "management PKI directory (default: <db-dir>/pki)")
	migrateManagementPKI := flag.Bool("migrate-management-pki", false, "initialize management PKI and migrate the database, then exit")
	pkiRotateIntermediate := flag.Bool("pki-rotate-intermediate", false, "rotate the online intermediate CA and Panel client certificate, then exit")
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
			log.Fatalf("create db dir: %v", err)
		}
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	dir := *pkiDir
	if dir == "" {
		dir = filepath.Join(filepath.Dir(*dbPath), "pki")
	}
	ca, err := pki.Open(dir)
	if err != nil {
		log.Fatalf("open management PKI: %v", err)
	}
	if *migrateManagementPKI {
		log.Printf("management PKI migration complete (db=%s pki=%s)", *dbPath, dir)
		return
	}
	if *pkiRotateIntermediate {
		if err := ca.RotateIntermediate(time.Now()); err != nil {
			log.Fatalf("rotate management intermediate CA: %v", err)
		}
		log.Printf("management intermediate CA rotated; move %s back to offline storage", filepath.Join(dir, "offline", "root-ca.key"))
		return
	}
	log.Printf("management PKI enabled (dir=%s intermediate_expires=%s)", dir, time.Unix(ca.Status().IntermediateNotAfterUnix, 0).Format(time.RFC3339))
	go ca.Run(context.Background())

	if err := api.EnsureAdminPassword(st); err != nil {
		log.Fatalf("ensure admin password: %v", err)
	}

	settings, err := st.GetSettings()
	if err != nil {
		log.Fatalf("get settings: %v", err)
	}

	secret := []byte(*sessionSecret)
	if len(secret) == 0 {
		secret, err = randomSecret(32)
		if err != nil {
			log.Fatalf("generate session secret: %v", err)
		}
		log.Printf("session secret not set; generated ephemeral secret (sessions will not survive restart)")
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

	agg := subscription.NewAggregator(st)
	chainService := proxychain.NewService(st, runner.ConfigBuilder, runner.Coordinator)
	chainService.PKI = ca
	srv := &api.Server{
		Store:      st,
		Runner:     runner,
		Secret:     secret,
		Aggregator: agg,
		Chains:     chainService,
		PKI:        ca,
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
			log.Printf("bootstrap: starting (timeout=%s)", *bootstrapTimeout)
			if err := runner.BootstrapAll(ctx); err != nil {
				log.Printf("bootstrap: finished with error: %v", err)
			} else {
				log.Printf("bootstrap: finished")
			}
		}()
	} else {
		log.Printf("bootstrap: disabled (-bootstrap=false)")
	}

	// Keep retrying nodes that come online later (agent started after panel, etc.).
	if *bootstrap && *bootstrapRetry {
		go func() {
			log.Printf("bootstrap-retry: enabled (interval=%s)", *bootstrapRetryInterval)
			runner.RunBootstrapRetryLoop(context.Background(), *bootstrapRetryInterval)
		}()
	} else if *bootstrap {
		log.Printf("bootstrap-retry: disabled (-bootstrap-retry=false)")
	}

	// Background refresh of external subscription sources.
	go func() {
		log.Printf("external-sources: background refresh enabled (interval=%s)", subscription.BackgroundTick)
		agg.RunBackground(context.Background(), subscription.BackgroundTick)
	}()
	go chainService.RunProbeLoop(context.Background())

	log.Printf("panel listening on %s (db=%s version=%s)", addr, *dbPath, version.Version)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

func randomSecret(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}
