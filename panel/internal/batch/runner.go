// Package batch runs multi-node control tasks with bounded concurrency.
package batch

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/ladderairport/panel/internal/converter"
	"github.com/ladderairport/panel/internal/nodeclient"
	"github.com/ladderairport/panel/internal/store"
	"github.com/ladderairport/pkg/hashutil"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
)

// NodeRPC is the subset of nodeclient used by the runner.
// *nodeclient.Client implements this interface.
type NodeRPC interface {
	Close() error
	ApplyConfig(ctx context.Context, configJSON, hash string, replace bool) (*agentv1.ApplyConfigResponse, error)
	Start(ctx context.Context) (*agentv1.StartResponse, error)
	Stop(ctx context.Context) (*agentv1.StopResponse, error)
}

// DialFunc dials a node control plane. Tests may inject a fake implementation.
type DialFunc func(ctx context.Context, n store.Node, token string) (NodeRPC, error)

// Runner executes batch tasks against agent nodes.
type Runner struct {
	Store          *store.Store
	DefaultToken   func() string
	Timeout        time.Duration
	MaxConcurrency int
	Dial           DialFunc
}

// NewRunner constructs a Runner with sensible defaults.
// Timeout defaults to 10s; MaxConcurrency defaults to 10.
// Dial defaults to nodeclient.Dial using the node's address/port/TLS settings.
func NewRunner(s *store.Store, defaultToken func() string) *Runner {
	r := &Runner{
		Store:          s,
		DefaultToken:   defaultToken,
		Timeout:        10 * time.Second,
		MaxConcurrency: 10,
	}
	r.Dial = r.defaultDial
	return r
}

func (r *Runner) defaultDial(ctx context.Context, n store.Node, token string) (NodeRPC, error) {
	cfg := nodeclient.DialConfig{
		Address:       net.JoinHostPort(n.Address, fmt.Sprintf("%d", n.GRPCPort)),
		Token:         token,
		Timeout:       r.Timeout,
		TLSSkipVerify: n.TLSSkipVerify,
	}
	if n.CACertPEM != "" {
		cfg.CACertPEM = []byte(n.CACertPEM)
	}
	return nodeclient.Dial(ctx, cfg)
}

// RunTask loads a task, fans out work to each target node, and updates status.
// Final status: all ok → success; all fail → failed; mixed → partial.
func (r *Runner) RunTask(ctx context.Context, taskID string) error {
	if r == nil || r.Store == nil {
		return fmt.Errorf("runner not configured")
	}
	if r.Dial == nil {
		return fmt.Errorf("dial not configured")
	}
	if r.DefaultToken == nil {
		r.DefaultToken = func() string { return "" }
	}

	task, err := r.Store.GetTask(taskID)
	if err != nil {
		return err
	}

	task.Status = "running"
	task.Results = []store.TaskNodeResult{}
	if err := r.Store.UpdateTask(task); err != nil {
		return fmt.Errorf("mark running: %w", err)
	}

	maxConc := r.MaxConcurrency
	if maxConc <= 0 {
		maxConc = 10
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	sem := make(chan struct{}, maxConc)
	var (
		mu      sync.Mutex
		results = make([]store.TaskNodeResult, 0, len(task.NodeIDs))
		wg      sync.WaitGroup
	)

	for _, nodeID := range task.NodeIDs {
		nodeID := nodeID
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				mu.Lock()
				results = append(results, store.TaskNodeResult{
					NodeID:  nodeID,
					OK:      false,
					Message: ctx.Err().Error(),
				})
				mu.Unlock()
				return
			}

			res := r.runOne(ctx, timeout, task.Type, task.ID, nodeID)

			mu.Lock()
			results = append(results, res)
			// Persist progress as results arrive.
			taskCopy := *task
			taskCopy.Results = append([]store.TaskNodeResult(nil), results...)
			taskCopy.Status = "running"
			_ = r.Store.UpdateTask(&taskCopy)
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Re-load is not needed; compute final status from collected results.
	okCount, failCount := 0, 0
	for _, res := range results {
		if res.OK {
			okCount++
		} else {
			failCount++
		}
	}
	final := "success"
	switch {
	case len(results) == 0:
		final = "success"
	case failCount == 0:
		final = "success"
	case okCount == 0:
		final = "failed"
	default:
		final = "partial"
	}

	task.Results = results
	task.Status = final
	if err := r.Store.UpdateTask(task); err != nil {
		return fmt.Errorf("finalize task: %w", err)
	}
	return nil
}

func (r *Runner) runOne(ctx context.Context, timeout time.Duration, taskType, taskID, nodeID string) store.TaskNodeResult {
	res := store.TaskNodeResult{NodeID: nodeID}

	node, err := r.Store.GetNode(nodeID)
	if err != nil {
		res.Message = err.Error()
		return res
	}

	token := node.Token
	if token == "" {
		token = r.DefaultToken()
	}

	opCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client, err := r.Dial(opCtx, *node, token)
	if err != nil {
		res.Message = fmt.Sprintf("dial: %v", err)
		node.Status = "unreachable"
		node.LastError = res.Message
		_ = r.Store.UpdateNode(node)
		return res
	}
	defer func() { _ = client.Close() }()

	switch taskType {
	case "apply":
		msg, err := r.applyNode(opCtx, client, taskID, node)
		if err != nil {
			res.Message = err.Error()
			node.LastError = res.Message
			_ = r.Store.UpdateNode(node)
			return res
		}
		res.OK = true
		res.Message = msg
	case "start":
		resp, err := client.Start(opCtx)
		if err != nil {
			res.Message = err.Error()
			node.LastError = res.Message
			_ = r.Store.UpdateNode(node)
			return res
		}
		res.OK = resp.GetOk()
		res.Message = resp.GetMessage()
		if !res.OK && res.Message == "" {
			res.Message = "start failed"
		}
		if res.OK {
			node.Status = "online"
			node.RuntimeState = "running"
			node.LastError = ""
			node.LastSeenUnix = time.Now().Unix()
			_ = r.Store.UpdateNode(node)
		} else {
			node.LastError = res.Message
			_ = r.Store.UpdateNode(node)
		}
	case "stop":
		resp, err := client.Stop(opCtx)
		if err != nil {
			res.Message = err.Error()
			return res
		}
		res.OK = resp.GetOk()
		res.Message = resp.GetMessage()
		if !res.OK && res.Message == "" {
			res.Message = "stop failed"
		}
	default:
		res.Message = fmt.Sprintf("unknown task type %q", taskType)
	}
	return res
}

func (r *Runner) applyNode(ctx context.Context, client NodeRPC, taskID string, node *store.Node) (string, error) {
	inbounds, err := r.Store.ListInboundsForNode(node.ID)
	if err != nil {
		return "", fmt.Errorf("list inbounds: %w", err)
	}
	cfgBytes, err := converter.Convert(inbounds, converter.ConvertOptions{
		BindInterface: node.EgressInterface,
	})
	if err != nil {
		return "", fmt.Errorf("convert: %w", err)
	}
	cfgJSON := string(cfgBytes)
	hash := hashutil.SHA256Hex(cfgBytes)

	snap := &store.ConfigSnapshot{
		NodeID:     node.ID,
		ConfigJSON: cfgJSON,
		ConfigHash: hash,
		TaskID:     taskID,
	}
	if err := r.Store.SaveSnapshot(snap); err != nil {
		return "", fmt.Errorf("save snapshot: %w", err)
	}

	resp, err := client.ApplyConfig(ctx, cfgJSON, hash, true)
	if err != nil {
		return "", err
	}
	if !resp.GetOk() {
		msg := resp.GetMessage()
		if msg == "" {
			msg = "apply failed"
		}
		return "", fmt.Errorf("%s", msg)
	}

	// Cache last applied hash + mark control plane online / core running.
	// Status is connectivity (online|unreachable|...); RuntimeState is box lifecycle.
	node.ConfigHash = hash
	node.Status = "online"
	node.RuntimeState = "running"
	node.LastError = ""
	node.LastSeenUnix = time.Now().Unix()
	if err := r.Store.UpdateNode(node); err != nil {
		// Apply succeeded; surface update error lightly in message.
		return fmt.Sprintf("applied (node update: %v)", err), nil
	}
	return resp.GetMessage(), nil
}
