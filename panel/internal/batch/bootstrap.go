package batch

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ladderairport/panel/internal/store"
)

// BootstrapAll applies config to every registered node.
// Apply already starts the agent core (single-instance lifecycle);
// a separate Start is not issued (avoids double reload on panel restart).
//
// Unreachable nodes are logged and skipped without failing the whole run.
func (r *Runner) BootstrapAll(ctx context.Context) error {
	if r == nil || r.Store == nil {
		return fmt.Errorf("批处理执行器尚未配置")
	}
	nodes, err := r.Store.ListNodes()
	if err != nil {
		return fmt.Errorf("查询节点列表失败：%w", err)
	}
	if len(nodes) == 0 {
		log.Printf("启动下发：尚未注册任何节点")
		return nil
	}
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	return r.bootstrapIDs(ctx, ids, "bootstrap")
}

// BootstrapPending applies only nodes that look unsynced:
// not online, or core not running, and have at least one enabled inbound.
// Apply alone starts the core when the agent accepts the config.
func (r *Runner) BootstrapPending(ctx context.Context) error {
	if r == nil || r.Store == nil {
		return fmt.Errorf("批处理执行器尚未配置")
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
	log.Printf("启动重试：%d 个节点需要同步", len(ids))
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
			log.Printf("启动重试：已停止")
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
		log.Printf("启动重试失败：%v", err)
	}
}

func (r *Runner) nodesNeedingBootstrap() ([]store.Node, error) {
	nodes, err := r.Store.ListNodes()
	if err != nil {
		return nil, fmt.Errorf("查询节点列表失败：%w", err)
	}
	var need []store.Node
	for _, n := range nodes {
		builder := r.ConfigBuilder
		if builder == nil {
			return nil, fmt.Errorf("配置构建器尚未配置")
		}
		cfg, err := builder.Build(n.ID)
		if err != nil {
			return nil, err
		}
		if nodeLooksSynced(n) && (n.ConfigHash == "" || n.ConfigHash == cfg.Hash) {
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
	// Single apply task only. Agent Apply is idempotent for same hash and
	// always leaves at most one sing-box instance running.
	applyTask := &store.Task{
		Type:    "apply",
		Status:  "pending",
		NodeIDs: ids,
	}
	if err := r.Store.CreateTask(applyTask); err != nil {
		return fmt.Errorf("创建配置下发任务失败：%w", err)
	}

	log.Printf("%s：正在向 %d 个节点下发配置（任务=%s）", label, len(ids), applyTask.ID)
	if err := r.RunTask(ctx, applyTask.ID); err != nil {
		log.Printf("%s：下发任务失败：%v", label, err)
	}
	if t, err := r.Store.GetTask(applyTask.ID); err == nil {
		logBootstrapResults(label+" 下发", t)
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
			log.Printf("%s 节点=%s：%s", kind, shortID(res.NodeID), res.Message)
		}
	}
	log.Printf("%s 完成：状态=%s，成功=%d，失败=%d", kind, t.Status, ok, fail)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// BootstrapNode applies config to a single node (starts core via Apply).
func (r *Runner) BootstrapNode(ctx context.Context, nodeID string) error {
	return r.bootstrapIDs(ctx, []string{nodeID}, "bootstrap-node")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
