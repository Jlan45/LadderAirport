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
      setError(err instanceof Error ? err.message : '加载节点列表失败')
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

  const translateKind = (kind: string) => {
    switch (kind) {
      case 'apply':
        return '应用配置'
      case 'start':
        return '启动服务'
      case 'stop':
        return '停止服务'
      default:
        return kind
    }
  }

  const translateStatus = (status: string) => {
    switch (status) {
      case 'online':
        return '在线'
      case 'unreachable':
        return '无法连接'
      case 'success':
        return '成功'
      case 'failed':
        return '失败'
      case 'pending':
        return '等待中'
      case 'running':
        return '进行中'
      default:
        return status || '未知'
    }
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
        setError('请选择操作节点或提供标签过滤')
        return
      }
      const fn =
        kind === 'apply' ? batchApply : kind === 'start' ? batchStart : batchStop
      const t = await fn(body)
      setTask(t)
      setMsg(`批量${translateKind(kind)}任务已启动 — 当前状态: ${translateStatus(t.status)}`)
    } catch (err) {
      setError(err instanceof Error ? err.message : `批量${translateKind(kind)}操作失败`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div>
      <h1>批量操作</h1>
      {error ? <div className="error">{error}</div> : null}
      {msg ? <div className="ok">{msg}</div> : null}

      <section className="card">
        <h2>操作目标节点</h2>
        <div className="form-row">
          <label htmlFor="labels">标签过滤器 (英文逗号分隔，可选)</label>
          <input
            id="labels"
            value={labelFilter}
            onChange={(e) => setLabelFilter(e.target.value)}
            placeholder="例如: prod,edge"
          />
          <div className="field-hint">
            除了下方手动勾选的节点，匹配此过滤条件中任意标签的节点也将包含在操作目标内。
          </div>
        </div>

        <div className="actions" style={{ marginBottom: '1rem' }}>
          <button type="button" className="btn-secondary" onClick={selectAll}>
            全选
          </button>
          <button type="button" className="btn-secondary" onClick={selectNone}>
            取消全选
          </button>
          <button type="button" className="btn-secondary" onClick={() => void load()}>
            刷新列表
          </button>
        </div>

        <div className="check-list">
          {nodes.length === 0 ? (
            <p className="muted" style={{ textAlign: 'center', padding: '1rem' }}>暂无节点可选</p>
          ) : (
            nodes.map((n) => (
              <label key={n.id} className="check-item" style={{ cursor: 'pointer' }}>
                <input
                  type="checkbox"
                  checked={selected.has(n.id)}
                  onChange={() => toggle(n.id)}
                />
                <span>
                  <strong>{n.name}</strong>{' '}
                  <code>
                    {n.address}:{n.grpc_port}
                  </code>{' '}
                  <span className={`status status-${n.status || 'unknown'}`} style={{ marginLeft: '0.25rem' }}>
                    {translateStatus(n.status || 'unknown')}
                  </span>{' '}
                  {(n.labels || []).length > 0 ? (
                    <span className="muted" style={{ marginLeft: '0.5rem', fontSize: '0.8rem' }}>
                      [{(n.labels || []).join(', ')}]
                    </span>
                  ) : null}
                </span>
              </label>
            ))
          )}
        </div>
      </section>

      <section className="card">
        <h2>批量指令</h2>
        <div className="actions">
          <button type="button" disabled={busy} onClick={() => void run('apply')}>
            批量应用配置
          </button>
          <button type="button" disabled={busy} onClick={() => void run('start')}>
            批量启动服务
          </button>
          <button type="button" className="btn-danger" disabled={busy} onClick={() => void run('stop')}>
            批量停止服务
          </button>
        </div>
      </section>

      {task ? (
        <section className="card">
          <h2>任务执行结果</h2>
          <div style={{ marginBottom: '1.25rem', lineHeight: '1.8' }}>
            <strong>任务 ID:</strong> <code>{task.id}</code>
            <br />
            <strong>任务类型:</strong> {translateKind(task.type)}
            <br />
            <strong>任务状态:</strong>{' '}
            <span className={`status status-${task.status}`}>{translateStatus(task.status)}</span>
            <br />
            <strong>包含节点数:</strong> {(task.node_ids || []).length} 个节点
          </div>
          <table>
            <thead>
              <tr>
                <th>节点名称</th>
                <th>执行成功</th>
                <th>返回消息</th>
              </tr>
            </thead>
            <tbody>
              {(task.results || []).length === 0 ? (
                <tr>
                  <td colSpan={3} className="muted" style={{ textAlign: 'center', padding: '1.5rem' }}>
                    {task.status === 'pending' || task.status === 'running'
                      ? '正在执行中…'
                      : '暂无单节点详细执行结果'}
                  </td>
                </tr>
              ) : (
                (task.results || []).map((r) => {
                  const n = nodes.find((x) => x.id === r.node_id)
                  return (
                    <tr key={r.node_id}>
                      <td style={{ fontWeight: 600 }}>{n ? n.name : r.node_id}</td>
                      <td>
                        <span className={`status ${r.ok ? 'status-success' : 'status-failed'}`}>
                          {r.ok ? '是' : '否'}
                        </span>
                      </td>
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
