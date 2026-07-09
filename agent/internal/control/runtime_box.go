package control

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"
)

// SingboxVersion is the upstream sing-box base version this agent is built against.
// Prefer constant.Version when set via -ldflags; fall back to the pinned tag.
func SingboxVersion() string {
	if constant.Version != "" && constant.Version != "unknown" {
		return constant.Version
	}
	return "1.11.15"
}

// BoxRuntime drives an in-process sing-box instance (二开 adapter).
// Apply builds and starts a new box before closing the old one so failures
// leave the previous instance running.
type BoxRuntime struct {
	mu            sync.Mutex
	dataDir       string
	instance      *box.Box
	cancel        context.CancelFunc
	configJSON    string
	configHash    string
	startedAtUnix int64
	lastError     string
	state         State
}

// NewBoxRuntime creates a BoxRuntime. dataDir, if non-empty, receives current.json
// snapshots of the last successfully applied config.
func NewBoxRuntime(dataDir string) *BoxRuntime {
	return &BoxRuntime{
		dataDir: dataDir,
		state:   StateStopped,
	}
}

func (r *BoxRuntime) Apply(ctx context.Context, configJSON string, hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	opts, err := r.parseOptions(configJSON)
	if err != nil {
		r.lastError = err.Error()
		// Keep previous instance/state; only surface the error.
		if r.instance == nil {
			r.state = StateStopped
		} else {
			r.state = StateRunning
		}
		return err
	}

	boxCtx := box.Context(
		context.Background(),
		include.InboundRegistry(),
		include.OutboundRegistry(),
		include.EndpointRegistry(),
	)
	boxCtx, cancel := context.WithCancel(boxCtx)

	instance, err := box.New(box.Options{
		Context: boxCtx,
		Options: opts,
	})
	if err != nil {
		cancel()
		r.lastError = err.Error()
		if r.instance == nil {
			r.state = StateStopped
		}
		return fmt.Errorf("create box: %w", err)
	}

	if err := instance.Start(); err != nil {
		_ = instance.Close()
		cancel()
		r.lastError = err.Error()
		if r.instance == nil {
			r.state = StateStopped
		}
		return fmt.Errorf("start box: %w", err)
	}

	// Success: tear down old, swap in new.
	if r.instance != nil {
		_ = r.instance.Close()
	}
	if r.cancel != nil {
		r.cancel()
	}

	r.instance = instance
	r.cancel = cancel
	r.configJSON = configJSON
	r.configHash = hash
	r.startedAtUnix = time.Now().Unix()
	r.lastError = ""
	r.state = StateRunning

	if r.dataDir != "" {
		if err := r.writeCurrentLocked(configJSON); err != nil {
			// Non-fatal: box is already running.
			r.lastError = "box running; failed to write current.json: " + err.Error()
		}
	}

	return nil
}

func (r *BoxRuntime) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.state == StateRunning && r.instance != nil {
		r.mu.Unlock()
		return nil
	}
	cfg := r.configJSON
	hash := r.configHash
	r.mu.Unlock()

	if cfg == "" {
		return fmt.Errorf("no config to start; Apply a config first")
	}
	return r.Apply(ctx, cfg, hash)
}

func (r *BoxRuntime) Stop(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var err error
	if r.instance != nil {
		err = r.instance.Close()
		r.instance = nil
	}
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.state = StateStopped
	r.startedAtUnix = 0
	return err
}

func (r *BoxRuntime) Status(_ context.Context) Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Status{
		State:         r.state,
		ConfigHash:    r.configHash,
		StartedAtUnix: r.startedAtUnix,
		LastError:     r.lastError,
	}
}

// Metrics returns process memory via runtime.MemStats.
// Connection and traffic counters are 0 in v1 — sing-box does not expose a
// stable in-process connection tally without clash API / custom hooks.
func (r *BoxRuntime) Metrics(_ context.Context) Metrics {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return Metrics{
		Connections:    0,
		UplinkBytes:    0,
		DownlinkBytes:  0,
		CPUPercent:     0,
		MemoryRSSBytes: int64(ms.Sys),
	}
}

// ConfigJSON returns the last successfully applied config JSON (for tests).
func (r *BoxRuntime) ConfigJSON() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.configJSON
}

func (r *BoxRuntime) parseOptions(configJSON string) (option.Options, error) {
	// Registries must be present so typed inbound/outbound options can decode.
	ctx := box.Context(
		context.Background(),
		include.InboundRegistry(),
		include.OutboundRegistry(),
		include.EndpointRegistry(),
	)
	opts, err := json.UnmarshalExtendedContext[option.Options](ctx, []byte(configJSON))
	if err != nil {
		return option.Options{}, fmt.Errorf("parse config: %w", err)
	}
	return opts, nil
}

func (r *BoxRuntime) writeCurrentLocked(configJSON string) error {
	if err := os.MkdirAll(r.dataDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(r.dataDir, "current.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(configJSON), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
