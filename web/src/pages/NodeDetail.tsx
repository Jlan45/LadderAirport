import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import {
  applyNode,
  getNodeMetrics,
  listInbounds,
  listNodeInbounds,
  listNodes,
  previewNodeConfig,
  setNodeInbounds,
  startNode,
  stopNode,
  streamNodeLogs,
  type InboundConfig,
  type Metrics,
  type Node,
  type Task,
} from '../api/client'

export default function NodeDetail() {
  const { id = '' } = useParams()
  const [node, setNode] = useState<Node | null>(null)
  const [allInbounds, setAllInbounds] = useState<InboundConfig[]>([])
  const [attachedIds, setAttachedIds] = useState<Set<string>>(new Set())
  const [preview, setPreview] = useState('')
  const [metrics, setMetrics] = useState<Metrics | null>(null)
  const [task, setTask] = useState<Task | null>(null)
  const [logs, setLogs] = useState<string[]>([])
  const [streaming, setStreaming] = useState(false)
  const [error, setError] = useState('')
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)
  const abortRef = useRef<AbortController | null>(null)
  const logEndRef = useRef<HTMLDivElement | null>(null)

  const load = useCallback(async () => {
    if (!id) return
    setError('')
    try {
      const [nodes, all, attached] = await Promise.all([
        listNodes(),
        listInbounds(),
        listNodeInbounds(id),
      ])
      const n = (nodes ?? []).find((x) => x.id === id) ?? null
      setNode(n)
      setAllInbounds(all ?? [])
      setAttachedIds(new Set((attached ?? []).map((a) => a.id)))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to load')
    }
  }, [id])

  useEffect(() => {
    void load()
  }, [load])

  useEffect(() => {
    return () => {
      abortRef.current?.abort()
    }
  }, [])

  useEffect(() => {
    logEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [logs])

  function toggleInbound(inbId: string) {
    setAttachedIds((prev) => {
      const next = new Set(prev)
      if (next.has(inbId)) next.delete(inbId)
      else next.add(inbId)
      return next
    })
  }

  async function onSaveInbounds() {
    if (!id) return
    setBusy(true)
    setError('')
    setMsg('')
    try {
      await setNodeInbounds(id, Array.from(attachedIds))
      setMsg('Inbounds attached')
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'attach failed')
    } finally {
      setBusy(false)
    }
  }

  async function onPreview() {
    if (!id) return
    setBusy(true)
    setError('')
    try {
      const cfg = await previewNodeConfig(id)
      setPreview(JSON.stringify(cfg, null, 2))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'preview failed')
    } finally {
      setBusy(false)
    }
  }

  async function runAction(kind: 'apply' | 'start' | 'stop') {
    if (!id) return
    setBusy(true)
    setError('')
    setMsg('')
    try {
      const fn = kind === 'apply' ? applyNode : kind === 'start' ? startNode : stopNode
      const t = await fn(id)
      setTask(t)
      setMsg(`${kind}: ${t.status}`)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : `${kind} failed`)
    } finally {
      setBusy(false)
    }
  }

  async function onMetrics() {
    if (!id) return
    setError('')
    try {
      const m = await getNodeMetrics(id)
      setMetrics(m)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'metrics failed')
    }
  }

  async function startLogs() {
    if (!id || streaming) return
    setLogs([])
    setError('')
    const ac = new AbortController()
    abortRef.current = ac
    setStreaming(true)
    try {
      await streamNodeLogs(id, {
        tail: 100,
        signal: ac.signal,
        onLine: (line) => {
          const ts = line.ts
            ? new Date(line.ts).toISOString()
            : new Date().toISOString()
          setLogs((prev) => [
            ...prev.slice(-499),
            `[${ts}] ${line.level || 'info'} ${line.message}`,
          ])
        },
      })
    } catch (err) {
      if ((err as Error).name !== 'AbortError') {
        setError(err instanceof Error ? err.message : 'log stream failed')
      }
    } finally {
      setStreaming(false)
      abortRef.current = null
    }
  }

  function stopLogs() {
    abortRef.current?.abort()
  }

  if (!id) {
    return <div className="error">Missing node id</div>
  }

  return (
    <div>
      <p>
        <Link to="/nodes">← Nodes</Link>
      </p>
      <h1>{node ? node.name : 'Node'}</h1>
      {node ? (
        <p className="muted">
          <code>
            {node.address}:{node.grpc_port}
          </code>{' '}
          · status:{' '}
          <span className={`status status-${node.status || 'unknown'}`}>
            {node.status || 'unknown'}
          </span>{' '}
          · labels: {(node.labels || []).join(', ') || '—'} · hash:{' '}
          <code>{node.config_hash || '—'}</code>
        </p>
      ) : (
        <p className="muted">Loading…</p>
      )}

      {error ? <div className="error">{error}</div> : null}
      {msg ? <div className="ok">{msg}</div> : null}

      <section className="card">
        <h2>Attach inbounds</h2>
        {allInbounds.length === 0 ? (
          <p className="muted">
            No inbounds in library. Create some on the{' '}
            <Link to="/inbounds">Inbounds</Link> page.
          </p>
        ) : (
          <div className="check-list">
            {allInbounds.map((inb) => (
              <label key={inb.id} className="check-item">
                <input
                  type="checkbox"
                  checked={attachedIds.has(inb.id)}
                  onChange={() => toggleInbound(inb.id)}
                />
                <span>
                  {inb.name} <code>{inb.protocol}</code>
                  {!inb.enabled ? ' (disabled)' : ''}
                </span>
              </label>
            ))}
          </div>
        )}
        <button type="button" disabled={busy} onClick={() => void onSaveInbounds()}>
          Save attachments
        </button>
      </section>

      <section className="card">
        <h2>Actions</h2>
        <div className="actions">
          <button type="button" disabled={busy} onClick={() => void onPreview()}>
            Preview config
          </button>
          <button type="button" disabled={busy} onClick={() => void runAction('apply')}>
            Apply
          </button>
          <button type="button" disabled={busy} onClick={() => void runAction('start')}>
            Start
          </button>
          <button type="button" disabled={busy} onClick={() => void runAction('stop')}>
            Stop
          </button>
          <button type="button" disabled={busy} onClick={() => void onMetrics()}>
            Refresh metrics
          </button>
        </div>
        {task ? (
          <div className="task-box">
            <strong>
              Task {task.id.slice(0, 8)}… — {task.type} — {task.status}
            </strong>
            <ul>
              {(task.results || []).map((r) => (
                <li key={r.node_id}>
                  {r.ok ? '✓' : '✗'} {r.node_id.slice(0, 8)}…: {r.message}
                </li>
              ))}
            </ul>
          </div>
        ) : null}
      </section>

      {metrics ? (
        <section className="card">
          <h2>Metrics</h2>
          <table className="kv">
            <tbody>
              <tr>
                <th>Connections</th>
                <td>{metrics.connections}</td>
              </tr>
              <tr>
                <th>Uplink</th>
                <td>{formatBytes(metrics.uplink_bytes)}</td>
              </tr>
              <tr>
                <th>Downlink</th>
                <td>{formatBytes(metrics.downlink_bytes)}</td>
              </tr>
              <tr>
                <th>CPU</th>
                <td>{metrics.cpu_percent?.toFixed?.(1) ?? metrics.cpu_percent}%</td>
              </tr>
              <tr>
                <th>Memory RSS</th>
                <td>{formatBytes(metrics.memory_rss_bytes)}</td>
              </tr>
            </tbody>
          </table>
        </section>
      ) : null}

      {preview ? (
        <section className="card">
          <h2>Config preview</h2>
          <pre className="code-block">{preview}</pre>
        </section>
      ) : null}

      <section className="card">
        <div className="row-between">
          <h2>Logs</h2>
          <div className="actions">
            {!streaming ? (
              <button type="button" onClick={() => void startLogs()}>
                Start stream
              </button>
            ) : (
              <button type="button" className="btn-secondary" onClick={stopLogs}>
                Stop stream
              </button>
            )}
          </div>
        </div>
        <div className="log-viewer">
          {logs.length === 0 ? (
            <div className="muted">
              {streaming ? 'Waiting for log lines…' : 'Not streaming'}
            </div>
          ) : (
            logs.map((line, i) => (
              <div key={i} className="log-line">
                {line}
              </div>
            ))
          )}
          <div ref={logEndRef} />
        </div>
      </section>
    </div>
  )
}

function formatBytes(n: number): string {
  if (n == null || !Number.isFinite(n)) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}
