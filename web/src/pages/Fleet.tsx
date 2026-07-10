import { useCallback, useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  ApiError,
  applyNode,
  batchApply,
  batchStart,
  batchStop,
  deleteNode,
  fleetOverview,
  fleetRefresh,
  getNodeInstallCommand,
  probeNode,
  startNode,
  stopNode,
  type FleetOverview,
  type Node,
  type NodeInstallInfo,
  type Task,
} from '../api/client'
import AddNodeModal from '../components/AddNodeModal'
import NodeDetailDrawer from '../components/NodeDetailDrawer'
import StatsBar from '../components/StatsBar'
import {
  formatBytes,
  formatTime,
  isOnlineStatus,
  runtimeLabel,
  statusClass,
  statusLabel,
} from '../lib/nodeDisplay'

type ViewMode = 'cards' | 'table'

export default function Fleet() {
  const [searchParams, setSearchParams] = useSearchParams()
  const detailId = searchParams.get('node')

  const [ov, setOv] = useState<FleetOverview | null>(null)
  const [error, setError] = useState('')
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)
  const [auto, setAuto] = useState(true)
  const [view, setView] = useState<ViewMode>(() => {
    const v = localStorage.getItem('fleet-view')
    return v === 'table' ? 'table' : 'cards'
  })
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [labelFilter, setLabelFilter] = useState('')
  const [lastTask, setLastTask] = useState<Task | null>(null)
  const [actionId, setActionId] = useState<string | null>(null)
  const [addOpen, setAddOpen] = useState(false)
  const [installBanner, setInstallBanner] = useState<NodeInstallInfo | null>(null)
  const [copied, setCopied] = useState(false)

  const loadCached = useCallback(async () => {
    try {
      const data = await fleetOverview()
      setOv(data)
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    }
  }, [])

  const refreshLive = useCallback(async () => {
    setBusy(true)
    setError('')
    try {
      const data = await fleetRefresh()
      setOv(data)
      setMsg(`已刷新 ${data.total_nodes} 个节点 · ${formatTime(data.refreshed_at)}`)
    } catch (err) {
      setError(err instanceof Error ? err.message : '刷新失败')
    } finally {
      setBusy(false)
    }
  }, [])

  useEffect(() => {
    void loadCached()
  }, [loadCached])

  useEffect(() => {
    if (!auto) return
    const t = window.setInterval(() => {
      void refreshLive()
    }, 15000)
    return () => window.clearInterval(t)
  }, [auto, refreshLive])

  function setViewMode(mode: ViewMode) {
    setView(mode)
    localStorage.setItem('fleet-view', mode)
  }

  const allNodes = ov?.nodes ?? []

  const nodes = useMemo(() => {
    const q = labelFilter.trim().toLowerCase()
    if (!q) return allNodes
    return allNodes.filter((n) => (n.labels ?? []).some((l) => l.toLowerCase().includes(q)))
  }, [allNodes, labelFilter])

  function openDetail(id: string) {
    setSearchParams({ node: id }, { replace: false })
  }

  function closeDetail() {
    setSearchParams({}, { replace: true })
  }

  function toggle(id: string) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  function toggleAllVisible() {
    if (nodes.length === 0) return
    const allSelected = nodes.every((n) => selected.has(n.id))
    setSelected((prev) => {
      const next = new Set(prev)
      if (allSelected) {
        for (const n of nodes) next.delete(n.id)
      } else {
        for (const n of nodes) next.add(n.id)
      }
      return next
    })
  }

  async function runBatch(kind: 'apply' | 'start' | 'stop') {
    const ids = Array.from(selected)
    if (ids.length === 0) {
      setError('请先勾选节点')
      return
    }
    setBusy(true)
    setError('')
    setMsg('')
    try {
      const fn = kind === 'apply' ? batchApply : kind === 'start' ? batchStart : batchStop
      const task = await fn({ node_ids: ids })
      setLastTask(task)
      const ok = task.results?.filter((r) => r.ok).length ?? 0
      const total = task.results?.length ?? 0
      setMsg(
        `批量${kind === 'apply' ? '下发配置' : kind === 'start' ? '启动' : '停止'}完成：${ok}/${total} 成功（${task.status}）`,
      )
      await refreshLive()
    } catch (err) {
      setError(err instanceof Error ? err.message : '批量操作失败')
    } finally {
      setBusy(false)
    }
  }

  async function runOne(id: string, kind: 'apply' | 'start' | 'stop') {
    setActionId(id)
    setError('')
    setMsg('')
    try {
      const task =
        kind === 'apply'
          ? await applyNode(id)
          : kind === 'start'
            ? await startNode(id)
            : await stopNode(id)
      setLastTask(task)
      const ok = task.results?.[0]?.ok
      setMsg(
        `${kind === 'apply' ? '下发' : kind === 'start' ? '启动' : '停止'} ${ok ? '成功' : '失败'}: ${task.results?.[0]?.message || task.status}`,
      )
      await refreshLive()
    } catch (err) {
      setError(err instanceof Error ? err.message : '操作失败')
    } finally {
      setActionId(null)
    }
  }

  async function onProbe(id: string) {
    setError('')
    setMsg('')
    try {
      const r = await probeNode(id)
      setMsg(
        `探测成功: Agent = ${r.agent_version}, sing-box = ${r.singbox_version}, 状态 = ${statusLabel(r.node.status)}`,
      )
      await loadCached()
    } catch (err) {
      if (err instanceof ApiError) setError(err.message)
      else setError(err instanceof Error ? err.message : '探测失败')
      await loadCached()
    }
  }

  async function onDelete(id: string, name: string) {
    if (!confirm(`确定要删除节点「${name}」吗？`)) return
    setError('')
    try {
      await deleteNode(id)
      if (detailId === id) closeDetail()
      if (installBanner?.node.id === id) setInstallBanner(null)
      setSelected((prev) => {
        const next = new Set(prev)
        next.delete(id)
        return next
      })
      await loadCached()
      setMsg(`已删除节点「${name}」`)
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除失败')
    }
  }

  async function showInstall(id: string) {
    setError('')
    setCopied(false)
    try {
      const info = await getNodeInstallCommand(id)
      setInstallBanner(info)
      setMsg(`已生成节点「${info.node.name}」的安装命令`)
    } catch (err) {
      setError(err instanceof Error ? err.message : '获取安装命令失败')
    }
  }

  async function copyInstallBanner() {
    if (!installBanner?.install_command) return
    try {
      await navigator.clipboard.writeText(installBanner.install_command)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      setError('复制失败，请手动选择命令')
    }
  }

  const visibleAllSelected = nodes.length > 0 && nodes.every((n) => selected.has(n.id))

  return (
    <div>
      <div className="row-between" style={{ marginBottom: '1rem' }}>
        <div>
          <h1 style={{ margin: 0 }}>节点</h1>
          <p className="muted" style={{ margin: '0.25rem 0 0' }}>
            状态监控 · 批量运维 · 安装与配置
          </p>
        </div>
        <div className="actions" style={{ gap: '0.5rem' }}>
          <div className="view-toggle" role="group" aria-label="视图切换">
            <button
              type="button"
              className={view === 'cards' ? 'view-toggle-btn active' : 'view-toggle-btn'}
              onClick={() => setViewMode('cards')}
            >
              卡片
            </button>
            <button
              type="button"
              className={view === 'table' ? 'view-toggle-btn active' : 'view-toggle-btn'}
              onClick={() => setViewMode('table')}
            >
              表格
            </button>
          </div>
          <label className="muted" style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <input type="checkbox" checked={auto} onChange={(e) => setAuto(e.target.checked)} />
            15s 自动刷新
          </label>
          <button type="button" className="btn-secondary" disabled={busy} onClick={() => void loadCached()}>
            缓存
          </button>
          <button type="button" disabled={busy} onClick={() => void refreshLive()}>
            {busy ? '刷新中…' : '探测全部'}
          </button>
          <button type="button" onClick={() => setAddOpen(true)}>
            添加节点
          </button>
        </div>
      </div>

      {error ? <div className="error">{error}</div> : null}
      {msg ? <div className="ok">{msg}</div> : null}

      <StatsBar ov={ov} />

      {installBanner ? (
        <section className="card" style={{ borderColor: 'var(--accent)', marginBottom: '1rem' }}>
          <div className="row-between">
            <h2 style={{ margin: 0 }}>安装命令 · {installBanner.node.name}</h2>
            <div className="actions">
              <button type="button" className="btn-secondary" onClick={() => void copyInstallBanner()}>
                {copied ? '已复制' : '复制命令'}
              </button>
              <button type="button" className="btn-secondary" onClick={() => setInstallBanner(null)}>
                关闭
              </button>
            </div>
          </div>
          <pre className="install-cmd-block" style={{ marginTop: '0.75rem' }}>
            {installBanner.install_command}
          </pre>
        </section>
      ) : null}

      <section className="card">
        <div className="row-between" style={{ flexWrap: 'wrap', gap: '0.75rem' }}>
          <h2 style={{ margin: 0 }}>{view === 'cards' ? '节点卡片' : '节点列表'}</h2>
          <div className="actions">
            <input
              type="search"
              value={labelFilter}
              onChange={(e) => setLabelFilter(e.target.value)}
              placeholder="按标签筛选…"
              style={{ minWidth: 140, maxWidth: 200 }}
              aria-label="按标签筛选"
            />
            <button type="button" className="btn-secondary" onClick={toggleAllVisible} disabled={nodes.length === 0}>
              {visibleAllSelected ? '取消全选' : '全选可见'}
            </button>
            <button
              type="button"
              className="btn-secondary"
              disabled={busy || selected.size === 0}
              onClick={() => void runBatch('apply')}
            >
              下发配置 ({selected.size})
            </button>
            <button
              type="button"
              className="btn-secondary"
              disabled={busy || selected.size === 0}
              onClick={() => void runBatch('start')}
            >
              启动
            </button>
            <button
              type="button"
              className="btn-secondary"
              disabled={busy || selected.size === 0}
              onClick={() => void runBatch('stop')}
            >
              停止
            </button>
          </div>
        </div>

        {nodes.length === 0 ? (
          <p className="muted" style={{ textAlign: 'center', padding: '2rem' }}>
            {allNodes.length === 0 ? (
              <>
                暂无节点。
                <button
                  type="button"
                  className="btn-secondary"
                  style={{ marginLeft: '0.5rem' }}
                  onClick={() => setAddOpen(true)}
                >
                  添加节点
                </button>
              </>
            ) : (
              '没有匹配标签筛选的节点'
            )}
          </p>
        ) : view === 'cards' ? (
          <div className="node-card-grid">
            {nodes.map((n) => (
              <NodeCard
                key={n.id}
                node={n}
                checked={selected.has(n.id)}
                busy={actionId === n.id || busy}
                onToggle={() => toggle(n.id)}
                onDetail={() => openDetail(n.id)}
                onApply={() => void runOne(n.id, 'apply')}
                onStart={() => void runOne(n.id, 'start')}
                onStop={() => void runOne(n.id, 'stop')}
                onProbe={() => void onProbe(n.id)}
                onInstall={() => void showInstall(n.id)}
                onDelete={() => void onDelete(n.id, n.name)}
              />
            ))}
          </div>
        ) : (
          <div className="table-wrap">
            <table className="fleet-table">
              <thead>
                <tr>
                  <th>
                    <input
                      type="checkbox"
                      checked={visibleAllSelected}
                      onChange={toggleAllVisible}
                      aria-label="全选可见节点"
                    />
                  </th>
                  <th>名称</th>
                  <th>地址</th>
                  <th>连通</th>
                  <th>核心</th>
                  <th>入站</th>
                  <th>连接</th>
                  <th>↑</th>
                  <th>↓</th>
                  <th>内存</th>
                  <th>标签</th>
                  <th>探测</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {nodes.map((n) => (
                  <tr key={n.id}>
                    <td>
                      <input
                        type="checkbox"
                        checked={selected.has(n.id)}
                        onChange={() => toggle(n.id)}
                        aria-label={`选择 ${n.name}`}
                      />
                    </td>
                    <td style={{ fontWeight: 600 }}>
                      <button type="button" className="link-btn" onClick={() => openDetail(n.id)}>
                        {n.name}
                      </button>
                    </td>
                    <td>
                      <code>
                        {n.address || '（待填）'}:{n.grpc_port}
                      </code>
                    </td>
                    <td>
                      <span className={statusClass(n.status)}>{statusLabel(n.status)}</span>
                    </td>
                    <td>
                      <span className={n.runtime_state === 'running' ? 'status status-success' : 'status'}>
                        {runtimeLabel(n.runtime_state)}
                      </span>
                    </td>
                    <td>{n.inbound_count ?? 0}</td>
                    <td>{n.connections ?? 0}</td>
                    <td>{formatBytes(n.uplink_bytes)}</td>
                    <td>{formatBytes(n.downlink_bytes)}</td>
                    <td>{formatBytes(n.memory_rss_bytes)}</td>
                    <td className="muted">{(n.labels ?? []).join(', ') || '—'}</td>
                    <td className="muted">{formatTime(n.last_seen_unix || n.metrics_at_unix)}</td>
                    <td className="actions">
                      <button type="button" className="link-btn" onClick={() => openDetail(n.id)}>
                        详情
                      </button>
                      <button type="button" className="link-btn" onClick={() => void onProbe(n.id)}>
                        探测
                      </button>
                      <button type="button" className="link-btn" onClick={() => void showInstall(n.id)}>
                        安装
                      </button>
                      <button
                        type="button"
                        className="link-btn link-danger"
                        onClick={() => void onDelete(n.id, n.name)}
                      >
                        删除
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {lastTask ? (
        <section className="card">
          <h2>最近任务</h2>
          <p className="muted">
            {lastTask.type} · {lastTask.status} · {lastTask.id}
          </p>
          <table>
            <thead>
              <tr>
                <th>节点</th>
                <th>结果</th>
                <th>信息</th>
              </tr>
            </thead>
            <tbody>
              {(lastTask.results ?? []).map((r) => (
                <tr key={r.node_id}>
                  <td>
                    <button type="button" className="link-btn" onClick={() => openDetail(r.node_id)}>
                      <code>{r.node_id.slice(0, 8)}</code>
                    </button>
                  </td>
                  <td>
                    <span className={`status ${r.ok ? 'status-success' : 'status-failed'}`}>
                      {r.ok ? '成功' : '失败'}
                    </span>
                  </td>
                  <td className="muted">{r.message}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      ) : null}

      <AddNodeModal
        open={addOpen}
        onClose={() => setAddOpen(false)}
        onCreated={() => void loadCached()}
        onOpenDetail={openDetail}
      />

      <NodeDetailDrawer
        nodeId={detailId}
        onClose={closeDetail}
        onChanged={() => void loadCached()}
      />
    </div>
  )
}

function NodeCard({
  node: n,
  checked,
  busy,
  onToggle,
  onDetail,
  onApply,
  onStart,
  onStop,
  onProbe,
  onInstall,
  onDelete,
}: {
  node: Node
  checked: boolean
  busy: boolean
  onToggle: () => void
  onDetail: () => void
  onApply: () => void
  onStart: () => void
  onStop: () => void
  onProbe: () => void
  onInstall: () => void
  onDelete: () => void
}) {
  const online = isOnlineStatus(n.status)
  const running = n.runtime_state === 'running' || n.status === 'running'
  return (
    <article
      className={`node-card ${online ? 'node-card-online' : 'node-card-offline'} ${checked ? 'node-card-selected' : ''}`}
    >
      <header className="node-card-header">
        <label className="node-card-check">
          <input type="checkbox" checked={checked} onChange={onToggle} />
          <button type="button" className="node-card-title link-btn" onClick={onDetail}>
            {n.name}
          </button>
        </label>
        <div className="node-card-badges">
          <span className={statusClass(n.status)}>{statusLabel(n.status)}</span>
          <span className={running ? 'status status-success' : 'status'}>{runtimeLabel(n.runtime_state)}</span>
        </div>
      </header>

      <div className="node-card-addr">
        <code>
          {n.address || '（待填）'}:{n.grpc_port}
        </code>
      </div>

      {(n.labels?.length ?? 0) > 0 ? (
        <div className="node-card-tags">
          {n.labels.map((l) => (
            <span key={l} className="tag-chip">
              {l}
            </span>
          ))}
        </div>
      ) : null}

      <div className="node-card-metrics">
        <div>
          <span className="m-label">入站</span>
          <span className="m-val">{n.inbound_count ?? 0}</span>
        </div>
        <div>
          <span className="m-label">连接</span>
          <span className="m-val">{n.connections ?? 0}</span>
        </div>
        <div>
          <span className="m-label">↑ 上传</span>
          <span className="m-val">{formatBytes(n.uplink_bytes)}</span>
        </div>
        <div>
          <span className="m-label">↓ 下载</span>
          <span className="m-val">{formatBytes(n.downlink_bytes)}</span>
        </div>
        <div>
          <span className="m-label">内存</span>
          <span className="m-val">{formatBytes(n.memory_rss_bytes)}</span>
        </div>
        <div>
          <span className="m-label">CPU</span>
          <span className="m-val">{(n.cpu_percent ?? 0).toFixed(1)}%</span>
        </div>
      </div>

      <div className="node-card-meta muted">
        <div>
          Agent {n.agent_version || '—'} · sing-box {n.singbox_version || '—'}
        </div>
        <div>探测 {formatTime(n.last_seen_unix || n.metrics_at_unix)}</div>
        {n.config_hash ? <div>配置 {n.config_hash.slice(0, 12)}…</div> : null}
        {n.last_error ? <div className="node-card-error">{n.last_error}</div> : null}
      </div>

      <footer className="node-card-actions">
        <button type="button" className="btn-secondary" onClick={onDetail}>
          详情
        </button>
        <button type="button" className="btn-secondary" disabled={busy} onClick={onApply}>
          下发
        </button>
        <button type="button" className="btn-secondary" disabled={busy} onClick={onStart}>
          启动
        </button>
        <button type="button" className="btn-secondary" disabled={busy} onClick={onStop}>
          停止
        </button>
        <button type="button" className="btn-secondary" onClick={onProbe}>
          探测
        </button>
        <button type="button" className="btn-secondary" onClick={onInstall}>
          安装
        </button>
        <button type="button" className="btn-danger" onClick={onDelete}>
          删除
        </button>
      </footer>
    </article>
  )
}
