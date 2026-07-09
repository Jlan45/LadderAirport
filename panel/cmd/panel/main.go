package main

import (
	"crypto/rand"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/labberairport/panel/internal/api"
	"github.com/labberairport/panel/internal/batch"
	"github.com/labberairport/panel/internal/store"
)

func main() {
	dbPath := flag.String("db", "./data/panel.db", "SQLite database path")
	listen := flag.String("listen", "", "HTTP listen address (default: settings.listen_addr or :8080)")
	sessionSecret := flag.String("session-secret", "", "JWT session HMAC secret (random if empty)")
	flag.Parse()

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
	if settings.GRPCTimeoutSec > 0 {
		runner.Timeout = time.Duration(settings.GRPCTimeoutSec) * time.Second
	}
	if settings.MaxConcurrency > 0 {
		runner.MaxConcurrency = settings.MaxConcurrency
	}

	srv := &api.Server{
		Store:  st,
		Runner: runner,
		Secret: secret,
	}

	addr := *listen
	if addr == "" {
		addr = settings.ListenAddr
	}
	if addr == "" {
		addr = ":8080"
	}

	log.Printf("panel listening on %s (db=%s)", addr, *dbPath)
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
