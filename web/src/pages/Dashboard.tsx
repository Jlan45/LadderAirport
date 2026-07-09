import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  batchApply,
  batchStart,
  batchStop,
  fleetOverview,
  fleetRefresh,
  type FleetOverview,
  type Node,
  type Task,
} from '../api/client'

function formatBytes(n?: number): string {
  if (n == null || n <= 0) return '0 B'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let i = 0
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024
    i++
  }
  return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${u[i]}`
}

function formatTime(unix?: number): string {
  if (!unix) return '—'
  const d = new Date(unix * 1000)
  return d.toLocaleString()
}

function statusLabel(s?: string): string {
  switch (s) {
    case 'online':
      return '在线'
    case 'unreachable':
      return '离线'
    case 'unauthorized':
      return '鉴权失败'
    case 'unknown':
      return '未知'
    default:
      return s || '未知'
  }
}

function statusClass(s?: string): string {
  if (s === 'online') return 'status status-success'
  if (s === 'unreachable' || s === 'unauthorized') return 'status status-failed'
  return 'status'
}

export default function Dashboard() {
  const [ov, setOv] = useState<FleetOverview | null>(null)
  const [error, setError] = useState('')
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)
  const [auto, setAuto] = useState(true)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [lastTask, setLastTask] = useState<Task | null>(null)

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

  const nodes = ov?.nodes ?? []

  function toggle(id: string) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  function toggleAll() {
    if (selected.size === nodes.length) {
      setSelected(new Set())
    } else {
      setSelected(new Set(nodes.map((n) => n.id)))
    }
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
        `批量${kind === 'apply' ? '下发配置' : kind === 'start' ? '启动' : '停止'}完成：${ok}/${total} 成功（任务 ${task.status}）`,
      )
      await refreshLive()
    } catch (err) {
      setError(err instanceof Error ? err.message : '批量操作失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div>
      <div className="row-between" style={{ marginBottom: '1rem' }}>
        <div>
          <h1 style={{ margin: 0 }}>舰队总览</h1>
          <p className="muted" style={{ margin: '0.25rem 0 0' }}>
            多节点状态监控 · 配置下发 · 参考 PPanel 运维台布局
          </p>
        </div>
        <div className="actions" style={{ gap: '0.5rem' }}>
          <label className="muted" style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <input type="checkbox" checked={auto} onChange={(e) => setAuto(e.target.checked)} />
            15s 自动刷新
          </label>
          <button type="button" className="btn-secondary" disabled={busy} onClick={() => void loadCached()}>
            缓存视图
          </button>
          <button type="button" disabled={busy} onClick={() => void refreshLive()}>
            {busy ? '刷新中…' : '立即探测全部'}
          </button>
        </div>
      </div>

      {error ? <div className="error">{error}</div> : null}
      {msg ? <div className="ok">{msg}</div> : null}

      <div className="stat-grid">
        <div className="stat-card">
          <div className="stat-label">节点总数</div>
          <div className="stat-value">{ov?.total_nodes ?? 0}</div>
        </div>
        <div className="stat-card stat-ok">
          <div className="stat-label">在线</div>
          <div className="stat-value">{ov?.online_nodes ?? 0}</div>
        </div>
        <div className="stat-card stat-warn">
          <div className="stat-label">离线/未知</div>
          <div className="stat-value">{ov?.offline_nodes ?? 0}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">核心运行中</div>
          <div className="stat-value">{ov?.running_nodes ?? 0}</div>
        </div>
      </div>

      <section className="card">
        <div className="row-between">
          <h2 style={{ margin: 0 }}>节点矩阵</h2>
          <div className="actions">
            <button type="button" className="btn-secondary" disabled={busy || selected.size === 0} onClick={() => void runBatch('apply')}>
              下发配置 ({selected.size})
            </button>
            <button type="button" className="btn-secondary" disabled={busy || selected.size === 0} onClick={() => void runBatch('start')}>
              启动
            </button>
            <button type="button" className="btn-secondary" disabled={busy || selected.size === 0} onClick={() => void runBatch('stop')}>
              停止
            </button>
            <Link to="/nodes" className="btn-secondary" style={{ display: 'inline-flex', alignItems: 'center' }}>
              管理节点
            </Link>
          </div>
        </div>

        {nodes.length === 0 ? (
          <p className="muted" style={{ textAlign: 'center', padding: '2rem' }}>
            暂无节点。请先到 <Link to="/nodes">节点</Link> 添加 Agent，再到入站关联配置后批量下发。
          </p>
        ) : (
          <div className="table-wrap">
            <table className="fleet-table">
              <thead>
                <tr>
                  <th>
                    <input
                      type="checkbox"
                      checked={nodes.length > 0 && selected.size === nodes.length}
                      onChange={toggleAll}
                    />
                  </th>
                  <th>名称</th>
                  <th>地址</th>
                  <th>连通</th>
                  <th>核心</th>
                  <th>入站数</th>
                  <th>连接</th>
                  <th>↑ 流量</th>
                  <th>↓ 流量</th>
                  <th>内存</th>
                  <th>标签</th>
                  <th>上次探测</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {nodes.map((n) => (
                  <NodeRow key={n.id} node={n} checked={selected.has(n.id)} onToggle={() => toggle(n.id)} />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {lastTask ? (
        <section className="card">
          <h2>最近批量任务</h2>
          <p className="muted">
            {lastTask.type} · {lastTask.status} · id={lastTask.id}
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
                    <code>{r.node_id.slice(0, 8)}</code>
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
    </div>
  )
}

function NodeRow({
  node: n,
  checked,
  onToggle,
}: {
  node: Node
  checked: boolean
  onToggle: () => void
}) {
  return (
    <tr>
      <td>
        <input type="checkbox" checked={checked} onChange={onToggle} />
      </td>
      <td style={{ fontWeight: 600 }}>
        <Link to={`/nodes/${n.id}`}>{n.name}</Link>
      </td>
      <td>
        <code>
          {n.address}:{n.grpc_port}
        </code>
      </td>
      <td>
        <span className={statusClass(n.status)}>{statusLabel(n.status)}</span>
      </td>
      <td>
        <span className={n.runtime_state === 'running' ? 'status status-success' : 'status'}>
          {n.runtime_state || '—'}
        </span>
      </td>
      <td>{n.inbound_count ?? 0}</td>
      <td>{n.connections ?? 0}</td>
      <td>{formatBytes(n.uplink_bytes)}</td>
      <td>{formatBytes(n.downlink_bytes)}</td>
      <td>{formatBytes(n.memory_rss_bytes)}</td>
      <td className="muted">{(n.labels ?? []).join(', ') || '—'}</td>
      <td className="muted" style={{ whiteSpace: 'nowrap' }}>
        {formatTime(n.last_seen_unix || n.metrics_at_unix)}
      </td>
      <td className="actions">
        <Link to={`/nodes/${n.id}`}>详情</Link>
      </td>
    </tr>
  )
}
