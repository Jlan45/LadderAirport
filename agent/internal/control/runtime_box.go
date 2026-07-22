package control

import (
	"context"
	stdjson "encoding/json"
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
	return "1.12.22"
}

// BoxRuntime drives a single in-process sing-box instance (二开 adapter).
//
// Lifecycle (strict single-instance):
//   - At most one *box.Box is ever started at a time in this process.
//   - Apply serializes via applyMu; concurrent Apply/Start wait in line.
//   - Config update: stop old completely → start new (brief downtime is OK).
//   - Same config_hash while already running → no-op (idempotent).
//   - If the new box fails to start, attempt to restore the previous config.
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

	// applyMu serializes all lifecycle transitions (Apply/Start/Stop).
	applyMu sync.Mutex
}

// NewBoxRuntime creates a BoxRuntime. dataDir, if non-empty, receives current.json
// snapshots of the last successfully applied config.
func NewBoxRuntime(dataDir string) *BoxRuntime {
	r := &BoxRuntime{
		dataDir: dataDir,
		state:   StateStopped,
	}
	r.loadTraffic()
	if dataDir != "" {
		go r.startTrafficPersistLoop()
	}
	return r
}

func (r *BoxRuntime) Apply(ctx context.Context, configJSON string, hash string) error {
	r.applyMu.Lock()
	defer r.applyMu.Unlock()
	return r.applyLocked(ctx, configJSON, hash)
}

// applyLocked requires applyMu held.
func (r *BoxRuntime) applyLocked(ctx context.Context, configJSON string, hash string) error {
	// Idempotent: already running this exact config — do not restart.
	r.mu.Lock()
	same := r.state == StateRunning && r.instance != nil && hash != "" && hash == r.configHash && configJSON == r.configJSON
	r.mu.Unlock()
	if same {
		return nil
	}

	// Parse/validate BEFORE tearing down the current instance.
	opts, err := r.parseOptions(configJSON)
	if err != nil {
		r.setLastError(err.Error())
		return err
	}

	// Snapshot previous config for restore-on-failure.
	r.mu.Lock()
	prevJSON, prevHash := r.configJSON, r.configHash
	r.mu.Unlock()

	// Strict single-instance: stop old completely before starting new.
	// Brief disconnect is acceptable; avoids dual listen on the same ports.
	r.stopInstanceLocked()

	if err := r.startInstanceLocked(opts, configJSON, hash); err != nil {
		// Best-effort restore of previous config when reload fails mid-way.
		if prevJSON != "" && prevJSON != configJSON {
			if restErr := r.startInstanceFromJSONLocked(prevJSON, prevHash); restErr != nil {
				r.setLastError(fmt.Sprintf("start failed: %v; restore also failed: %v", err, restErr))
				return fmt.Errorf("start box: %w (restore failed: %v)", err, restErr)
			}
			r.setLastError("start failed, restored previous config: " + err.Error())
			return fmt.Errorf("start box: %w (previous config restored)", err)
		}
		r.setLastError(err.Error())
		return fmt.Errorf("start box: %w", err)
	}

	if r.dataDir != "" {
		if err := r.writeCurrent(r.dataDir, configJSON); err != nil {
			r.setLastError("box running; failed to write current.json: " + err.Error())
		}
	}

	select {
	case <-ctx.Done():
		// Config already applied; do not roll back.
	default:
	}
	return nil
}

// stopInstanceLocked closes the current box if any. Caller must hold applyMu.
func (r *BoxRuntime) stopInstanceLocked() {
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
	// Keep configJSON/hash so Start can re-apply after Stop.
	r.mu.Unlock()

	r.saveTraffic()

	if old != nil {
		_ = old.Close()
	}
	if oldCancel != nil {
		oldCancel()
	}
}

// startInstanceFromJSONLocked parses and starts. Caller holds applyMu; no current instance.
func (r *BoxRuntime) startInstanceFromJSONLocked(configJSON, hash string) error {
	opts, err := r.parseOptions(configJSON)
	if err != nil {
		return err
	}
	return r.startInstanceLocked(opts, configJSON, hash)
}

// startInstanceLocked creates and starts a box. Caller holds applyMu; instance must be nil.
func (r *BoxRuntime) startInstanceLocked(opts option.Options, configJSON, hash string) error {
	boxCtx := include.Context(context.Background())
	boxCtx, cancel := context.WithCancel(boxCtx)

	instance, err := box.New(box.Options{
		Context: boxCtx,
		Options: opts,
	})
	if err != nil {
		cancel()
		return fmt.Errorf("create box: %w", err)
	}

	tracker := newTrafficTracker()
	instance.Router().AppendTracker(tracker)

	if err := instance.Start(); err != nil {
		_ = instance.Close()
		cancel()
		return err
	}

	r.mu.Lock()
	r.instance = instance
	r.cancel = cancel
	r.tracker = tracker
	r.configJSON = configJSON
	r.configHash = hash
	r.startedAtUnix = time.Now().Unix()
	r.lastError = ""
	r.state = StateRunning
	r.mu.Unlock()
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
	r.applyMu.Lock()
	defer r.applyMu.Unlock()

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
	// Re-apply under same lock (idempotent if already mid-start elsewhere).
	return r.applyLocked(ctx, cfg, hash)
}

func (r *BoxRuntime) Stop(_ context.Context) error {
	r.applyMu.Lock()
	defer r.applyMu.Unlock()

	r.mu.Lock()
	had := r.instance != nil
	r.mu.Unlock()
	r.stopInstanceLocked()
	if !had {
		return nil
	}
	return nil
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
	ctx := include.Context(context.Background())
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

type trafficState struct {
	Uplink   int64 `json:"uplink"`
	Downlink int64 `json:"downlink"`
}

func (r *BoxRuntime) loadTraffic() {
	if r.dataDir == "" {
		return
	}
	path := filepath.Join(r.dataDir, "traffic.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var state trafficState
	if err := stdjson.Unmarshal(data, &state); err == nil {
		r.mu.Lock()
		r.prevUplink = state.Uplink
		r.prevDownlink = state.Downlink
		r.mu.Unlock()
	}
}

func (r *BoxRuntime) saveTraffic() {
	if r.dataDir == "" {
		return
	}
	r.mu.Lock()
	var up, down int64
	if r.tracker != nil {
		_, u, d := r.tracker.Snapshot()
		up, down = u, d
	}
	totalUp := up + r.prevUplink
	totalDown := down + r.prevDownlink
	r.mu.Unlock()

	state := trafficState{
		Uplink:   totalUp,
		Downlink: totalDown,
	}
	data, err := stdjson.Marshal(state)
	if err != nil {
		return
	}
	path := filepath.Join(r.dataDir, "traffic.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

func (r *BoxRuntime) startTrafficPersistLoop() {
	ticker := time.NewTicker(10 * time.Second)
	for range ticker.C {
		r.saveTraffic()
	}
}
