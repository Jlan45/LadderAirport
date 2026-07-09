import { useCallback, useEffect, useState } from 'react'
import {
  batchApply,
  batchStart,
  batchStop,
  getTask,
  listNodes,
  type Node,
  type Task,
} from '../api/client'

export default function Batch() {
  const [nodes, setNodes] = useState<Node[]>([])
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [labelFilter, setLabelFilter] = useState('')
  const [task, setTask] = useState<Task | null>(null)
  const [error, setError] = useState('')
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    try {
      const list = await listNodes()
      setNodes(list ?? [])
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to load nodes')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  // Poll task while pending/running
  useEffect(() => {
    if (!task) return
    if (task.status !== 'pending' && task.status !== 'running') return
    const id = task.id
    const timer = setInterval(() => {
      void (async () => {
        try {
          const t = await getTask(id)
          setTask(t)
        } catch {
          // ignore poll errors
        }
      })()
    }, 1500)
    return () => clearInterval(timer)
  }, [task])

  function toggle(id: string) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  function selectAll() {
    setSelected(new Set(nodes.map((n) => n.id)))
  }

  function selectNone() {
    setSelected(new Set())
  }

  async function run(kind: 'apply' | 'start' | 'stop') {
    setBusy(true)
    setError('')
    setMsg('')
    try {
      const labels = labelFilter
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean)
      const body = {
        node_ids: Array.from(selected),
        labels,
      }
      if (body.node_ids.length === 0 && labels.length === 0) {
        setError('select nodes and/or provide labels')
        return
      }
      const fn =
        kind === 'apply' ? batchApply : kind === 'start' ? batchStart : batchStop
      const t = await fn(body)
      setTask(t)
      setMsg(`Batch ${kind} started — status ${t.status}`)
    } catch (err) {
      setError(err instanceof Error ? err.message : `${kind} failed`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div>
      <h1>Batch</h1>
      {error ? <div className="error">{error}</div> : null}
      {msg ? <div className="ok">{msg}</div> : null}

      <section className="card">
        <h2>Targets</h2>
        <div className="form-row">
          <label htmlFor="labels">Label filter (comma-separated, optional)</label>
          <input
            id="labels"
            value={labelFilter}
            onChange={(e) => setLabelFilter(e.target.value)}
            placeholder="prod,edge"
          />
          <div className="field-hint">
            Nodes matching any label are included in addition to the selection below.
          </div>
        </div>

        <div className="actions" style={{ marginBottom: '0.75rem' }}>
          <button type="button" className="btn-secondary" onClick={selectAll}>
            Select all
          </button>
          <button type="button" className="btn-secondary" onClick={selectNone}>
            Select none
          </button>
          <button type="button" className="btn-secondary" onClick={() => void load()}>
            Refresh
          </button>
        </div>

        <div className="check-list">
          {nodes.length === 0 ? (
            <p className="muted">No nodes</p>
          ) : (
            nodes.map((n) => (
              <label key={n.id} className="check-item">
                <input
                  type="checkbox"
                  checked={selected.has(n.id)}
                  onChange={() => toggle(n.id)}
                />
                <span>
                  {n.name}{' '}
                  <code>
                    {n.address}:{n.grpc_port}
                  </code>{' '}
                  <span className={`status status-${n.status || 'unknown'}`}>
                    {n.status || 'unknown'}
                  </span>{' '}
                  {(n.labels || []).length > 0 ? (
                    <span className="muted">[{(n.labels || []).join(', ')}]</span>
                  ) : null}
                </span>
              </label>
            ))
          )}
        </div>
      </section>

      <section className="card">
        <h2>Actions</h2>
        <div className="actions">
          <button type="button" disabled={busy} onClick={() => void run('apply')}>
            Apply
          </button>
          <button type="button" disabled={busy} onClick={() => void run('start')}>
            Start
          </button>
          <button type="button" disabled={busy} onClick={() => void run('stop')}>
            Stop
          </button>
        </div>
      </section>

      {task ? (
        <section className="card">
          <h2>Task result</h2>
          <p>
            <strong>ID:</strong> <code>{task.id}</code>
            <br />
            <strong>Type:</strong> {task.type}
            <br />
            <strong>Status:</strong>{' '}
            <span className={`status status-${task.status}`}>{task.status}</span>
            <br />
            <strong>Nodes:</strong> {(task.node_ids || []).length}
          </p>
          <table>
            <thead>
              <tr>
                <th>Node</th>
                <th>OK</th>
                <th>Message</th>
              </tr>
            </thead>
            <tbody>
              {(task.results || []).length === 0 ? (
                <tr>
                  <td colSpan={3} className="muted">
                    {task.status === 'pending' || task.status === 'running'
                      ? 'Running…'
                      : 'No per-node results'}
                  </td>
                </tr>
              ) : (
                (task.results || []).map((r) => {
                  const n = nodes.find((x) => x.id === r.node_id)
                  return (
                    <tr key={r.node_id}>
                      <td>{n ? n.name : r.node_id}</td>
                      <td>{r.ok ? '✓' : '✗'}</td>
                      <td>{r.message}</td>
                    </tr>
                  )
                })
              )}
            </tbody>
          </table>
        </section>
      ) : null}
    </div>
  )
}
