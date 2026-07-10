import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
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

  // connection settings
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
      setMsg('入站配置关联成功')
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '关联失败')
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

  async function runAction(kind: 'apply' | 'start' | 'stop') {
    if (!id) return
    setBusy(true)
    setError('')
    setMsg('')
    try {
      const fn = kind === 'apply' ? applyNode : kind === 'start' ? startNode : stopNode
      const t = await fn(id)
      setTask(t)
      setMsg(`${translateKind(kind)}指令下发: ${translateStatus(t.status)}`)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : `${translateKind(kind)}失败`)
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

  if (!id) {
    return <div className="error">缺少节点 ID</div>
  }

  return (
    <div>
      <p style={{ marginBottom: '1.5rem' }}>
        <Link to="/nodes">← 返回节点列表</Link>
      </p>
      <h1>{node ? node.name : '节点详情'}</h1>
      {node ? (
        <p className="muted" style={{ display: 'flex', gap: '0.75rem', flexWrap: 'wrap', alignItems: 'center' }}>
          <code>
            {node.address || '（待填）'}:{node.grpc_port}
          </code>{' '}
          · <span>状态:</span>
          <span className={`status status-${node.status || 'unknown'}`}>
            {translateStatus(node.status || 'unknown')}
          </span>{' '}
          · <span>标签:</span>
          {(node.labels || []).length > 0 ? (
            (node.labels || []).map((lbl) => (
              <span key={lbl} className="status" style={{ fontSize: '0.75rem', padding: '0.1rem 0.4rem' }}>{lbl}</span>
            ))
          ) : (
            <span className="muted">—</span>
          )}{' '}
          · <span>配置哈希:</span>
          <code>{node.config_hash || '—'}</code>
        </p>
      ) : (
        <p className="muted">加载中…</p>
      )}

      {error ? <div className="error">{error}</div> : null}
      {msg ? <div className="ok">{msg}</div> : null}

      <section className="card">
        <h2>连接与 TLS</h2>
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
              rows={6}
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
          <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap' }}>
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
            <pre
              style={{
                marginTop: '0.5rem',
                padding: '1rem',
                background: '#0f172a',
                color: '#e2e8f0',
                borderRadius: 8,
                overflow: 'auto',
                fontSize: '0.85rem',
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-all',
              }}
            >
              {installInfo.install_command}
            </pre>
          </div>
        ) : null}
      </section>

      <section className="card">
        <h2>关联入站配置</h2>
        {allInbounds.length === 0 ? (
          <p className="muted">
            当前模板库中无入站配置。请先在 <Link to="/inbounds">入站配置管理</Link> 页面创建。
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
          保存关联关系
        </button>
      </section>

      <section className="card">
        <h2>管理操作</h2>
        <div className="actions">
          <button type="button" className="btn-secondary" disabled={busy} onClick={() => void onPreview()}>
            预览配置
          </button>
          <button type="button" disabled={busy} onClick={() => void runAction('apply')}>
            应用配置
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
              任务 {task.id.slice(0, 8)}… — 类型: {translateKind(task.type)} — 状态: {translateStatus(task.status)}
            </strong>
            <ul>
              {(task.results || []).map((r) => (
                <li key={r.node_id}>
                  {r.ok ? '✓' : '✗'} 节点 {r.node_id.slice(0, 8)}…: {r.message}
                </li>
              ))}
            </ul>
          </div>
        ) : null}
      </section>

      {metrics ? (
        <section className="card">
          <h2>监控指标</h2>
          <table className="kv">
            <tbody>
              <tr>
                <th>当前连接数</th>
                <td>{metrics.connections}</td>
              </tr>
              <tr>
                <th>上行流量 (累计)</th>
                <td>{formatBytes(metrics.uplink_bytes)}</td>
              </tr>
              <tr>
                <th>下行流量 (累计)</th>
                <td>{formatBytes(metrics.downlink_bytes)}</td>
              </tr>
              <tr>
                <th>CPU 使用率</th>
                <td>{metrics.cpu_percent?.toFixed?.(1) ?? metrics.cpu_percent}%</td>
              </tr>
              <tr>
                <th>内存占用 (RSS)</th>
                <td>{formatBytes(metrics.memory_rss_bytes)}</td>
              </tr>
            </tbody>
          </table>
        </section>
      ) : null}

      {preview ? (
        <section className="card">
          <h2>配置文件预览</h2>
          <pre className="code-block">{preview}</pre>
        </section>
      ) : null}

      <section className="card">
        <div className="row-between">
          <h2>节点日志</h2>
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
        <div className="log-viewer">
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
