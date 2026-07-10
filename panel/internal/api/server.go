// Package api implements the Panel HTTP JSON API and admin session auth.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/ladderairport/panel/internal/batch"
	"github.com/ladderairport/panel/internal/nodeclient"
	"github.com/ladderairport/panel/internal/store"
	"github.com/ladderairport/panel/web"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
)

// NodeLive is the subset of nodeclient used for probe/metrics/logs.
// *nodeclient.Client implements this interface.
type NodeLive interface {
	Close() error
	Ping(ctx context.Context) (*agentv1.PingResponse, error)
	GetStatus(ctx context.Context) (*agentv1.GetStatusResponse, error)
	GetMetrics(ctx context.Context) (*agentv1.GetMetricsResponse, error)
	StreamLogs(ctx context.Context, level string, tail int32) (agentv1.AgentControl_StreamLogsClient, error)
}

// LiveDialFunc dials a node for live RPCs (probe/metrics/logs).
// Tests may inject a fake implementation.
type LiveDialFunc func(ctx context.Context, n store.Node, token string) (NodeLive, error)

// Server is the Panel HTTP API.
type Server struct {
	Store  *store.Store
	Runner *batch.Runner
	Secret []byte // JWT HMAC secret

	// Dial is used for probe/metrics/logs. When nil, defaults to nodeclient.Dial.
	Dial LiveDialFunc

	// Timeout for probe/metrics/logs dials. Defaults to Runner.Timeout or 10s.
	Timeout time.Duration
}

// Handler returns an http.Handler with all routes, auth middleware, and embedded SPA.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	s.mountSPA(mux)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Admin APIs require session; /sub/{token} and login are public.
		if isAPIPath(r.URL.Path) && !isPublicAPI(r) {
			if !s.authenticated(r) {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

// mountSPA serves the embedded React SPA for non-API routes with index.html fallback.
func (s *Server) mountSPA(mux *http.ServeMux) {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		// Dist layout missing; skip SPA (API still works).
		return
	}
	mux.Handle("/", spaHandler(dist))
}

// spaHandler serves static files from root; unknown paths fall back to index.html.
func spaHandler(root fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		upath := path.Clean(r.URL.Path)
		if upath == "/" || upath == "." {
			http.ServeFileFS(w, r, root, "index.html")
			return
		}
		// Strip leading slash for fs.FS paths.
		rel := strings.TrimPrefix(upath, "/")

		f, err := root.Open(rel)
		if err != nil {
			// SPA client-side route fallback.
			http.ServeFileFS(w, r, root, "index.html")
			return
		}
		defer f.Close()

		st, err := f.Stat()
		if err != nil || st.IsDir() {
			http.ServeFileFS(w, r, root, "index.html")
			return
		}

		http.ServeFileFS(w, r, root, rel)
	})
}

func isAPIPath(path string) bool {
	return strings.HasPrefix(path, "/api/v1/")
}

func isPublicAPI(r *http.Request) bool {
	if r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/login" {
		return true
	}
	// Agent install enrollment (auth via node token, not admin session).
	if r.Method == http.MethodPost && r.URL.Path == "/api/v1/agent/enroll" {
		return true
	}
	// Public subscription pull (token in path).
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/sub/") {
		return true
	}
	return false
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	// Public subscription endpoint (no admin session).
	mux.HandleFunc("GET /sub/{token}", s.handlePublicSubscription)

	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/agent/enroll", s.handleAgentEnroll)

	mux.HandleFunc("GET /api/v1/templates", s.handleListTemplates)

	mux.HandleFunc("GET /api/v1/inbounds", s.handleListInbounds)
	mux.HandleFunc("POST /api/v1/inbounds", s.handleCreateInbound)
	mux.HandleFunc("PUT /api/v1/inbounds/{id}", s.handleUpdateInbound)
	mux.HandleFunc("DELETE /api/v1/inbounds/{id}", s.handleDeleteInbound)

	mux.HandleFunc("GET /api/v1/fleet/overview", s.handleFleetOverview)
	mux.HandleFunc("POST /api/v1/fleet/refresh", s.handleFleetRefresh)

	mux.HandleFunc("GET /api/v1/nodes", s.handleListNodes)
	mux.HandleFunc("POST /api/v1/nodes", s.handleCreateNode)
	mux.HandleFunc("POST /api/v1/nodes/bootstrap", s.handleBootstrapNode)
	mux.HandleFunc("PUT /api/v1/nodes/{id}", s.handleUpdateNode)
	mux.HandleFunc("DELETE /api/v1/nodes/{id}", s.handleDeleteNode)
	mux.HandleFunc("GET /api/v1/nodes/{id}/install-command", s.handleNodeInstallCommand)
	mux.HandleFunc("POST /api/v1/nodes/{id}/probe", s.handleProbeNode)
	mux.HandleFunc("GET /api/v1/nodes/{id}/inbounds", s.handleListNodeInbounds)
	mux.HandleFunc("PUT /api/v1/nodes/{id}/inbounds", s.handleSetNodeInbounds)
	mux.HandleFunc("POST /api/v1/nodes/{id}/apply", s.handleNodeApply)
	mux.HandleFunc("POST /api/v1/nodes/{id}/config/preview", s.handleNodePreview)
	mux.HandleFunc("POST /api/v1/nodes/{id}/start", s.handleNodeStart)
	mux.HandleFunc("POST /api/v1/nodes/{id}/stop", s.handleNodeStop)
	mux.HandleFunc("GET /api/v1/nodes/{id}/metrics", s.handleNodeMetrics)
	mux.HandleFunc("GET /api/v1/nodes/{id}/logs", s.handleNodeLogs)

	mux.HandleFunc("POST /api/v1/batch/apply", s.handleBatchApply)
	mux.HandleFunc("POST /api/v1/batch/start", s.handleBatchStart)
	mux.HandleFunc("POST /api/v1/batch/stop", s.handleBatchStop)

	mux.HandleFunc("GET /api/v1/tasks", s.handleListTasks)
	mux.HandleFunc("GET /api/v1/tasks/{id}", s.handleGetTask)

	mux.HandleFunc("GET /api/v1/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/v1/settings", s.handlePutSettings)

	mux.HandleFunc("GET /api/v1/subscriptions", s.handleListSubscriptions)
	mux.HandleFunc("POST /api/v1/subscriptions", s.handleCreateSubscription)
	mux.HandleFunc("PUT /api/v1/subscriptions/{id}", s.handleUpdateSubscription)
	mux.HandleFunc("DELETE /api/v1/subscriptions/{id}", s.handleDeleteSubscription)
	mux.HandleFunc("GET /api/v1/subscriptions/{id}/preview", s.handlePreviewSubscription)
}

func (s *Server) liveDial(ctx context.Context, n store.Node, token string) (NodeLive, error) {
	if s.Dial != nil {
		return s.Dial(ctx, n, token)
	}
	return s.defaultLiveDial(ctx, n, token)
}

func (s *Server) defaultLiveDial(ctx context.Context, n store.Node, token string) (NodeLive, error) {
	timeout := s.Timeout
	if timeout <= 0 && s.Runner != nil && s.Runner.Timeout > 0 {
		timeout = s.Runner.Timeout
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	cfg := nodeclient.DialConfig{
		Address:       net.JoinHostPort(n.Address, fmt.Sprintf("%d", n.GRPCPort)),
		Token:         token,
		Timeout:       timeout,
		TLSSkipVerify: n.TLSSkipVerify,
	}
	if n.CACertPEM != "" {
		cfg.CACertPEM = []byte(n.CACertPEM)
	}
	return nodeclient.Dial(ctx, cfg)
}

func (s *Server) nodeToken(n *store.Node) string {
	if n.Token != "" {
		return n.Token
	}
	if s.Runner != nil && s.Runner.DefaultToken != nil {
		return s.Runner.DefaultToken()
	}
	return ""
}

func (s *Server) opTimeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	if s.Runner != nil && s.Runner.Timeout > 0 {
		return s.Runner.Timeout
	}
	return 10 * time.Second
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, dest any) error {
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	return dec.Decode(dest)
}

func pathID(r *http.Request) string {
	return r.PathValue("id")
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "not found")
}
