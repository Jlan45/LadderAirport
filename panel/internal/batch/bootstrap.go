package batch

import (
	"context"
	"fmt"
	"log"

	"github.com/labberairport/panel/internal/store"
)

// BootstrapAll applies config then starts every registered node.
// Intended for panel process start: agents that are up get hot-loaded;
// unreachable nodes are logged and skipped without failing the whole run.
//
// Per node:
//  1. Apply current attached inbounds (skipped if none / convert error)
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

	// Persist audit tasks so the UI can show startup sync results.
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

	log.Printf("bootstrap: applying config to %d node(s) (task=%s)", len(ids), applyTask.ID)
	if err := r.RunTask(ctx, applyTask.ID); err != nil {
		log.Printf("bootstrap: apply task error: %v", err)
	}
	if t, err := r.Store.GetTask(applyTask.ID); err == nil {
		logBootstrapResults("apply", t)
	}

	log.Printf("bootstrap: starting runtimes on %d node(s) (task=%s)", len(ids), startTask.ID)
	if err := r.RunTask(ctx, startTask.ID); err != nil {
		log.Printf("bootstrap: start task error: %v", err)
	}
	if t, err := r.Store.GetTask(startTask.ID); err == nil {
		logBootstrapResults("start", t)
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
			log.Printf("bootstrap %s node=%s: %s", kind, shortID(res.NodeID), res.Message)
		}
	}
	log.Printf("bootstrap %s done: status=%s ok=%d fail=%d", kind, t.Status, ok, fail)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// BootstrapNode applies + starts a single node (used by tests or manual hooks).
func (r *Runner) BootstrapNode(ctx context.Context, nodeID string) error {
	applyTask := &store.Task{Type: "apply", Status: "pending", NodeIDs: []string{nodeID}}
	if err := r.Store.CreateTask(applyTask); err != nil {
		return err
	}
	if err := r.RunTask(ctx, applyTask.ID); err != nil {
		return err
	}
	startTask := &store.Task{Type: "start", Status: "pending", NodeIDs: []string{nodeID}}
	if err := r.Store.CreateTask(startTask); err != nil {
		return err
	}
	return r.RunTask(ctx, startTask.ID)
}
