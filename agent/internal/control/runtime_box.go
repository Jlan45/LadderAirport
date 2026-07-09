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
//
// Hot reload (Apply):
//  1. Parse + create + start a NEW box instance without holding the state lock
//     (so gRPC Ping/GetStatus stay responsive during reload).
//  2. On success, swap under lock and close the old instance.
//  3. On failure, keep the old instance running and return the error.
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

	// Traffic stats from ConnectionTracker (current instance + rolled-up previous).
	tracker      *trafficTracker
	prevUplink   int64
	prevDownlink int64

	// applyMu serializes reloads so two Applies don't start two boxes concurrently.
	applyMu sync.Mutex
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
	// Only one reload at a time; do not hold state mu across Start (hot-reload).
	r.applyMu.Lock()
	defer r.applyMu.Unlock()

	opts, err := r.parseOptions(configJSON)
	if err != nil {
		r.setLastError(err.Error())
		return err
	}

	// Build + start NEW instance while old keeps serving traffic.
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
		r.setLastError(err.Error())
		return fmt.Errorf("create box: %w", err)
	}

	// Attach traffic tracker before Start so all routed conns are counted.
	tracker := newTrafficTracker()
	instance.Router().AppendTracker(tracker)

	if err := instance.Start(); err != nil {
		_ = instance.Close()
		cancel()
		r.setLastError(err.Error())
		// Keep previous instance if any.
		return fmt.Errorf("start box: %w", err)
	}

	// Success: atomic swap under state lock, then tear down old.
	r.mu.Lock()
	old := r.instance
	oldCancel := r.cancel
	oldTracker := r.tracker
	// Roll previous instance traffic into lifetime totals so hot-reload does not reset UI counters.
	if oldTracker != nil {
		_, up, down := oldTracker.Snapshot()
		r.prevUplink += up
		r.prevDownlink += down
	}
	r.instance = instance
	r.cancel = cancel
	r.tracker = tracker
	r.configJSON = configJSON
	r.configHash = hash
	r.startedAtUnix = time.Now().Unix()
	r.lastError = ""
	r.state = StateRunning
	dataDir := r.dataDir
	r.mu.Unlock()

	if old != nil {
		_ = old.Close()
	}
	if oldCancel != nil {
		oldCancel()
	}

	if dataDir != "" {
		if err := r.writeCurrent(dataDir, configJSON); err != nil {
			r.setLastError("box running; failed to write current.json: " + err.Error())
		}
	}

	// Respect cancel of the RPC context after success (best-effort).
	select {
	case <-ctx.Done():
		// Config already applied; do not roll back.
	default:
	}
	return nil
}

func (r *BoxRuntime) setLastError(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastError = msg
	if r.instance == nil {
		r.state = StateStopped
	} else {
		r.state = StateRunning
	}
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
	r.applyMu.Lock()
	defer r.applyMu.Unlock()

	r.mu.Lock()
	old := r.instance
	oldCancel := r.cancel
	oldTracker := r.tracker
	if oldTracker != nil {
		_, up, down := oldTracker.Snapshot()
		r.prevUplink += up
		r.prevDownlink += down
	}
	r.instance = nil
	r.cancel = nil
	r.tracker = nil
	r.state = StateStopped
	r.startedAtUnix = 0
	r.mu.Unlock()

	var err error
	if old != nil {
		err = old.Close()
	}
	if oldCancel != nil {
		oldCancel()
	}
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

// Metrics returns live connection count, cumulative traffic (survives hot-reload),
// and approximate process memory. CPU percent is sampled coarsely (0 if unavailable).
func (r *BoxRuntime) Metrics(_ context.Context) Metrics {
	r.mu.Lock()
	var conns, up, down int64
	if r.tracker != nil {
		c, u, d := r.tracker.Snapshot()
		conns, up, down = c, u, d
	}
	up += r.prevUplink
	down += r.prevDownlink
	r.mu.Unlock()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return Metrics{
		Connections:    conns,
		UplinkBytes:    up,
		DownlinkBytes:  down,
		CPUPercent:     sampleCPUPercent(),
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

func (r *BoxRuntime) writeCurrent(dataDir, configJSON string) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dataDir, "current.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(configJSON), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
