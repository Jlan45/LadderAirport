import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  applyNode,
  getNodeInstallCommand,
  getNodeMetrics,
  listInbounds,
  listNodeInbounds,
  listNodes,
  previewNodeConfig,
  setNodeInbounds,
  startNode,
  stopNode,
  streamNodeLogs,
  updateNode,
  type InboundConfig,
  type Metrics,
  type Node,
  type NodeInstallInfo,
  type Task,
} from '../api/client'
import { formatBytes, taskKindLabel, taskStatusLabel } from '../lib/nodeDisplay'

type Props = {
  nodeId: string | null
  onClose: () => void
  onChanged: () => void
}

export default function NodeDetailDrawer({ nodeId, onClose, onChanged }: Props) {
  const open = !!nodeId
  const id = nodeId ?? ''

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

  const [editAddress, setEditAddress] = useState('')
  const [editPort, setEditPort] = useState(50051)
  const [editCA, setEditCA] = useState('')
  const [editTLSSkip, setEditTLSSkip] = useState(false)
  const [installInfo, setInstallInfo] = useState<NodeInstallInfo | null>(null)
  const [copied, setCopied] = useState(false)

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
      if (n) {
        setEditAddress(n.address || '')
        setEditPort(n.grpc_port || 50051)
        setEditCA(n.ca_cert_pem || '')
        setEditTLSSkip(!!n.tls_skip_verify)
      }
      setAllInbounds(all ?? [])
      setAttachedIds(new Set((attached ?? []).map((a) => a.id)))
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    }
  }, [id])

  useEffect(() => {
    if (!open) {
      abortRef.current?.abort()
      setStreaming(false)
      setLogs([])
      setPreview('')
      setMetrics(null)
      setTask(null)
      setInstallInfo(null)
      setNode(null)
      setMsg('')
      setError('')
      return
    }
    void load()
  }, [open, load])

  useEffect(() => {
    return () => {
      abortRef.current?.abort()
    }
  }, [])

  useEffect(() => {
    logEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [logs])

  useEffect(() => {
    if (!open) return
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

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
    setTask(null)
    try {
      const res = await setNodeInbounds(id, Array.from(attachedIds))
      const base = res.deploy_message || (res.deployed ? '已关联并下发配置' : '关联已保存')
      setMsg(base)
      if (res.apply_task) setTask(res.apply_task)
      else if (res.start_task) setTask(res.start_task)
      if (!res.deployed && res.apply_task?.status === 'failed') {
        setError(base)
        setMsg('')
      }
      await load()
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
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
      setError(err instanceof Error ? err.message : '配置预览生成失败')
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
      setMsg(`${taskKindLabel(kind)}指令下发: ${taskStatusLabel(t.status)}`)
      await load()
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : `${taskKindLabel(kind)}失败`)
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
      setError(err instanceof Error ? err.message : '获取监控指标失败')
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
          const ts = line.ts ? new Date(line.ts).toISOString() : new Date().toISOString()
          setLogs((prev) => [...prev.slice(-499), `[${ts}] ${line.level || 'info'} ${line.message}`])
        },
      })
    } catch (err) {
      if ((err as Error).name !== 'AbortError') {
        setError(err instanceof Error ? err.message : '日志流连接失败')
      }
    } finally {
      setStreaming(false)
      abortRef.current = null
    }
  }

  function stopLogs() {
    abortRef.current?.abort()
  }

  async function onSaveConnection() {
    if (!id || !node) return
    setBusy(true)
    setError('')
    setMsg('')
    try {
      const updated = await updateNode(id, {
        ...node,
        address: editAddress.trim(),
        grpc_port: editPort,
        ca_cert_pem: editCA,
        tls_skip_verify: editTLSSkip,
        status: editAddress.trim() ? (node.status === 'pending' ? 'unknown' : node.status) : 'pending',
      })
      setNode(updated)
      setMsg('连接设置已保存')
      await load()
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  async function onShowInstall() {
    if (!id) return
    setError('')
    setCopied(false)
    try {
      const info = await getNodeInstallCommand(id, { tls: !editTLSSkip })
      setInstallInfo(info)
    } catch (err) {
      setError(err instanceof Error ? err.message : '获取安装命令失败')
    }
  }

  async function copyInstall() {
    if (!installInfo?.install_command) return
    try {
      await navigator.clipboard.writeText(installInfo.install_command)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      setError('复制失败，请手动选择命令')
    }
  }

  if (!open) return null

  return (
    <div className="drawer-overlay" role="presentation" onClick={onClose}>
      <aside
        className="drawer-panel"
        role="dialog"
        aria-modal="true"
        aria-labelledby="node-drawer-title"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="drawer-header">
          <div style={{ minWidth: 0 }}>
            <h2 id="node-drawer-title" style={{ margin: 0 }}>
              {node ? node.name : '节点详情'}
            </h2>
            {node ? (
              <p className="muted" style={{ margin: '0.35rem 0 0', fontSize: '0.85rem' }}>
                <code>
                  {node.address || '（待填）'}:{node.grpc_port}
                </code>{' '}
                · {taskStatusLabel(node.status || 'unknown')}
              </p>
            ) : (
              <p className="muted" style={{ margin: '0.35rem 0 0' }}>
                加载中…
              </p>
            )}
          </div>
          <button type="button" className="btn-secondary" onClick={onClose}>
            关闭
          </button>
        </div>

        <div className="drawer-body">
          {error ? <div className="error">{error}</div> : null}
          {msg ? <div className="ok">{msg}</div> : null}

          <section className="card drawer-section">
            <h3>连接与 TLS</h3>
            <p className="muted" style={{ marginTop: 0 }}>
              装机后填写地址，并将节点上的 <code>ca.crt</code> 粘贴到下方（TLS 模式）。
            </p>
            <div className="form-grid">
              <div className="form-row">
                <label htmlFor="nd-addr">节点地址</label>
                <input
                  id="nd-addr"
                  value={editAddress}
                  onChange={(e) => setEditAddress(e.target.value)}
                  placeholder="对 Panel 可达的 IP 或域名"
                />
              </div>
              <div className="form-row">
                <label htmlFor="nd-port">gRPC 端口</label>
                <input
                  id="nd-port"
                  type="number"
                  value={editPort}
                  onChange={(e) => setEditPort(Number(e.target.value))}
                />
              </div>
              <div className="form-row">
                <label htmlFor="nd-ca">CA 证书 (ca_cert_pem)</label>
                <textarea
                  id="nd-ca"
                  rows={5}
                  value={editCA}
                  onChange={(e) => setEditCA(e.target.value)}
                  placeholder="-----BEGIN CERTIFICATE----- ..."
                  style={{ fontFamily: 'var(--mono)', fontSize: '0.85rem' }}
                />
              </div>
              <div className="form-row">
                <label htmlFor="nd-skip" style={{ display: 'flex', alignItems: 'center', cursor: 'pointer' }}>
                  <input
                    id="nd-skip"
                    type="checkbox"
                    checked={editTLSSkip}
                    onChange={(e) => setEditTLSSkip(e.target.checked)}
                  />{' '}
                  跳过 TLS 证书验证（仅 lab）
                </label>
              </div>
              <div className="actions">
                <button type="button" disabled={busy} onClick={() => void onSaveConnection()}>
                  保存连接设置
                </button>
                <button type="button" className="btn-secondary" onClick={() => void onShowInstall()}>
                  显示安装命令
                </button>
              </div>
            </div>
            {installInfo ? (
              <div style={{ marginTop: '1rem' }}>
                <div className="row-between">
                  <strong>一键安装</strong>
                  <button type="button" className="btn-secondary" onClick={() => void copyInstall()}>
                    {copied ? '已复制' : '复制'}
                  </button>
                </div>
                <pre className="install-cmd-block">{installInfo.install_command}</pre>
              </div>
            ) : null}
          </section>

          <section className="card drawer-section">
            <h3>入站配置</h3>
            <p className="muted" style={{ marginTop: 0 }}>
              勾选后保存会<strong>自动下发到节点并启动核心</strong>。
            </p>
            {allInbounds.length === 0 ? (
              <p className="muted">
                无入站配置。请先在 <Link to="/inbounds">入站</Link> 创建。
              </p>
            ) : (
              <div className="check-list">
                {allInbounds.map((inb) => (
                  <label key={inb.id} className="check-item" style={{ cursor: 'pointer' }}>
                    <input
                      type="checkbox"
                      checked={attachedIds.has(inb.id)}
                      onChange={() => toggleInbound(inb.id)}
                    />
                    <span>
                      {inb.name} <code>{inb.protocol}</code>
                      {!inb.enabled ? ' (已禁用)' : ''}
                    </span>
                  </label>
                ))}
              </div>
            )}
            <button type="button" disabled={busy} onClick={() => void onSaveInbounds()}>
              {busy ? '保存并下发中…' : '保存并下发'}
            </button>
          </section>

          <section className="card drawer-section">
            <h3>管理操作</h3>
            <div className="actions">
              <button type="button" className="btn-secondary" disabled={busy} onClick={() => void onPreview()}>
                预览配置
              </button>
              <button type="button" disabled={busy} onClick={() => void runAction('start')}>
                启动服务
              </button>
              <button type="button" className="btn-danger" disabled={busy} onClick={() => void runAction('stop')}>
                停止服务
              </button>
              <button type="button" className="btn-secondary" disabled={busy} onClick={() => void onMetrics()}>
                刷新监控指标
              </button>
            </div>
            {task ? (
              <div className="task-box">
                <strong>
                  任务 {task.id.slice(0, 8)}… — {taskKindLabel(task.type)} — {taskStatusLabel(task.status)}
                </strong>
                <ul>
                  {(task.results || []).map((r) => (
                    <li key={r.node_id}>
                      {r.ok ? '✓' : '✗'} {r.message}
                    </li>
                  ))}
                </ul>
              </div>
            ) : null}
          </section>

          {metrics ? (
            <section className="card drawer-section">
              <h3>监控指标</h3>
              <table className="kv">
                <tbody>
                  <tr>
                    <th>当前连接数</th>
                    <td>{metrics.connections}</td>
                  </tr>
                  <tr>
                    <th>上行流量</th>
                    <td>{formatBytes(metrics.uplink_bytes)}</td>
                  </tr>
                  <tr>
                    <th>下行流量</th>
                    <td>{formatBytes(metrics.downlink_bytes)}</td>
                  </tr>
                  <tr>
                    <th>CPU</th>
                    <td>{metrics.cpu_percent?.toFixed?.(1) ?? metrics.cpu_percent}%</td>
                  </tr>
                  <tr>
                    <th>内存 (RSS)</th>
                    <td>{formatBytes(metrics.memory_rss_bytes)}</td>
                  </tr>
                </tbody>
              </table>
            </section>
          ) : null}

          {preview ? (
            <section className="card drawer-section">
              <h3>配置文件预览</h3>
              <pre className="code-block">{preview}</pre>
            </section>
          ) : null}

          <section className="card drawer-section">
            <div className="row-between">
              <h3 style={{ margin: 0 }}>节点日志</h3>
              <div className="actions">
                {!streaming ? (
                  <button type="button" onClick={() => void startLogs()}>
                    开启实时日志
                  </button>
                ) : (
                  <button type="button" className="btn-secondary" onClick={stopLogs}>
                    停止实时日志
                  </button>
                )}
              </div>
            </div>
            <div className="log-viewer" style={{ marginTop: '0.75rem' }}>
              {logs.length === 0 ? (
                <div className="muted" style={{ textAlign: 'center', padding: '1.5rem 0' }}>
                  {streaming ? '正在等待日志输出…' : '未开启实时日志'}
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
      </aside>
    </div>
  )
}
