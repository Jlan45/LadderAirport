package batch

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/labberairport/panel/internal/store"
)

// BootstrapAll applies config then starts every registered node.
// Intended for panel process start: agents that are up get hot-loaded;
// unreachable nodes are logged and skipped without failing the whole run.
//
// Per node:
//  1. Apply current attached inbounds (fails cleanly if none)
//  2. Start runtime (no-op if already running after Apply)
func (r *Runner) BootstrapAll(ctx context.Context) error {
	if r == nil || r.Store == nil {
		return fmt.Errorf("runner not configured")
	}
	nodes, err := r.Store.ListNodes()
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}
	if len(nodes) == 0 {
		log.Printf("bootstrap: no nodes registered")
		return nil
	}
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	return r.bootstrapIDs(ctx, ids, "bootstrap")
}

// BootstrapPending applies+starts only nodes that look unsynced:
// not online, or core not running, and have at least one enabled inbound.
func (r *Runner) BootstrapPending(ctx context.Context) error {
	if r == nil || r.Store == nil {
		return fmt.Errorf("runner not configured")
	}
	need, err := r.nodesNeedingBootstrap()
	if err != nil {
		return err
	}
	if len(need) == 0 {
		return nil
	}
	ids := make([]string, 0, len(need))
	for _, n := range need {
		ids = append(ids, n.ID)
	}
	log.Printf("bootstrap-retry: %d node(s) need sync", len(ids))
	return r.bootstrapIDs(ctx, ids, "bootstrap-retry")
}

// RunBootstrapRetryLoop periodically retries unsynced nodes until ctx is cancelled.
func (r *Runner) RunBootstrapRetryLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	// Immediate first pass after a short delay (agents often start after panel).
	timer := time.NewTimer(2 * time.Second)
	select {
	case <-ctx.Done():
		timer.Stop()
		return
	case <-timer.C:
		r.runRetryOnce(ctx)
	}

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Printf("bootstrap-retry: stopped")
			return
		case <-t.C:
			r.runRetryOnce(ctx)
		}
	}
}

func (r *Runner) runRetryOnce(parent context.Context) {
	// Cap each round so a hung dial cannot block the loop forever.
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	// Allow all pending nodes: concurrency * per-node timeout + slack.
	round := timeout*time.Duration(max(1, r.MaxConcurrency)*3) + 30*time.Second
	if round < 2*time.Minute {
		round = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, round)
	defer cancel()
	if err := r.BootstrapPending(ctx); err != nil {
		log.Printf("bootstrap-retry: %v", err)
	}
}

func (r *Runner) nodesNeedingBootstrap() ([]store.Node, error) {
	nodes, err := r.Store.ListNodes()
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	var need []store.Node
	for _, n := range nodes {
		if nodeLooksSynced(n) {
			continue
		}
		ins, err := r.Store.ListInboundsForNode(n.ID)
		if err != nil {
			return nil, err
		}
		hasEnabled := false
		for _, in := range ins {
			if in.Enabled {
				hasEnabled = true
				break
			}
		}
		if !hasEnabled {
			// Nothing to push; skip to avoid endless convert failures.
			continue
		}
		need = append(need, n)
	}
	return need, nil
}

func nodeLooksSynced(n store.Node) bool {
	online := n.Status == "online" || n.Status == "running"
	running := n.RuntimeState == "running" || n.Status == "running"
	return online && running
}

func (r *Runner) bootstrapIDs(ctx context.Context, ids []string, label string) error {
	if len(ids) == 0 {
		return nil
	}
	applyTask := &store.Task{
		Type:    "apply",
		Status:  "pending",
		NodeIDs: ids,
	}
	if err := r.Store.CreateTask(applyTask); err != nil {
		return fmt.Errorf("create apply task: %w", err)
	}
	startTask := &store.Task{
		Type:    "start",
		Status:  "pending",
		NodeIDs: ids,
	}
	if err := r.Store.CreateTask(startTask); err != nil {
		return fmt.Errorf("create start task: %w", err)
	}

	log.Printf("%s: applying config to %d node(s) (task=%s)", label, len(ids), applyTask.ID)
	if err := r.RunTask(ctx, applyTask.ID); err != nil {
		log.Printf("%s: apply task error: %v", label, err)
	}
	if t, err := r.Store.GetTask(applyTask.ID); err == nil {
		logBootstrapResults(label+" apply", t)
	}

	log.Printf("%s: starting runtimes on %d node(s) (task=%s)", label, len(ids), startTask.ID)
	if err := r.RunTask(ctx, startTask.ID); err != nil {
		log.Printf("%s: start task error: %v", label, err)
	}
	if t, err := r.Store.GetTask(startTask.ID); err == nil {
		logBootstrapResults(label+" start", t)
	}
	return nil
}

func logBootstrapResults(kind string, t *store.Task) {
	ok, fail := 0, 0
	for _, res := range t.Results {
		if res.OK {
			ok++
		} else {
			fail++
			log.Printf("%s node=%s: %s", kind, shortID(res.NodeID), res.Message)
		}
	}
	log.Printf("%s done: status=%s ok=%d fail=%d", kind, t.Status, ok, fail)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// BootstrapNode applies + starts a single node (used by tests or manual hooks).
func (r *Runner) BootstrapNode(ctx context.Context, nodeID string) error {
	return r.bootstrapIDs(ctx, []string{nodeID}, "bootstrap-node")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
