import { FormEvent, useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  ApiError,
  createNode,
  deleteNode,
  listNodes,
  probeNode,
  type Node,
} from '../api/client'

export default function Nodes() {
  const [nodes, setNodes] = useState<Node[]>([])
  const [error, setError] = useState('')
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)

  // create form
  const [name, setName] = useState('')
  const [address, setAddress] = useState('')
  const [grpcPort, setGrpcPort] = useState(50051)
  const [token, setToken] = useState('')
  const [labels, setLabels] = useState('')
  const [tlsSkip, setTlsSkip] = useState(true)

  const load = useCallback(async () => {
    setError('')
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

  async function onCreate(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    setMsg('')
    try {
      await createNode({
        name,
        address,
        grpc_port: grpcPort,
        token: token || undefined,
        labels: labels
          .split(',')
          .map((s) => s.trim())
          .filter(Boolean),
        tls_skip_verify: tlsSkip,
      })
      setName('')
      setAddress('')
      setToken('')
      setLabels('')
      setMsg('节点创建成功')
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建失败')
    } finally {
      setBusy(false)
    }
  }

  async function onProbe(id: string) {
    setError('')
    setMsg('')
    try {
      const r = await probeNode(id)
      setMsg(
        `探测成功: Agent版本 = ${r.agent_version}, Sing-box版本 = ${r.singbox_version}, 状态 = ${r.node.status === 'online' ? '在线' : r.node.status === 'unreachable' ? '无法连接' : r.node.status || '未知'}`,
      )
      await load()
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message)
      } else {
        setError(err instanceof Error ? err.message : '探测失败')
      }
      await load()
    }
  }

  async function onDelete(id: string, n: string) {
    if (!confirm(`确定要删除节点 "${n}" 吗？`)) return
    setError('')
    try {
      await deleteNode(id)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除失败')
    }
  }

  const renderStatus = (status: string) => {
    switch (status) {
      case 'online':
        return '在线'
      case 'unreachable':
        return '无法连接'
      default:
        return status || '未知'
    }
  }

  return (
    <div>
      <h1>节点管理</h1>
      {error ? <div className="error">{error}</div> : null}
      {msg ? <div className="ok">{msg}</div> : null}

      <section className="card">
        <h2>创建新节点</h2>
        <form className="form-grid" onSubmit={onCreate}>
          <div className="form-row">
            <label htmlFor="n-name">节点名称</label>
            <input
              id="n-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              required
              placeholder="例如: 香港 01"
            />
          </div>
          <div className="form-row">
            <label htmlFor="n-addr">节点地址</label>
            <input
              id="n-addr"
              value={address}
              onChange={(e) => setAddress(e.target.value)}
              required
              placeholder="10.0.0.1 或 hk.example.com"
            />
          </div>
          <div className="form-row">
            <label htmlFor="n-port">gRPC 端口</label>
            <input
              id="n-port"
              type="number"
              value={grpcPort}
              onChange={(e) => setGrpcPort(Number(e.target.value))}
            />
          </div>
          <div className="form-row">
            <label htmlFor="n-token">访问令牌 (可选，覆盖默认值)</label>
            <input
              id="n-token"
              type="password"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              autoComplete="off"
              placeholder="留空则使用全局默认令牌"
            />
          </div>
          <div className="form-row">
            <label htmlFor="n-labels">节点标签 (英文逗号分隔)</label>
            <input
              id="n-labels"
              value={labels}
              onChange={(e) => setLabels(e.target.value)}
              placeholder="例如: edge,prod,hk"
            />
          </div>
          <div className="form-row">
            <label htmlFor="n-tls" style={{ display: 'flex', alignItems: 'center', cursor: 'pointer' }}>
              <input
                id="n-tls"
                type="checkbox"
                checked={tlsSkip}
                onChange={(e) => setTlsSkip(e.target.checked)}
              />{' '}
              跳过 TLS 证书验证
            </label>
          </div>
          <button type="submit" disabled={busy}>
            创建节点
          </button>
        </form>
      </section>

      <section className="card">
        <div className="row-between">
          <h2>节点集群</h2>
          <button type="button" className="btn-secondary" onClick={() => void load()}>
            刷新列表
          </button>
        </div>
        <table>
          <thead>
            <tr>
              <th>节点名称</th>
              <th>地址与端口</th>
              <th>运行状态</th>
              <th>节点标签</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {nodes.length === 0 ? (
              <tr>
                <td colSpan={5} className="muted" style={{ textAlign: 'center', padding: '2rem' }}>
                  暂无节点数据
                </td>
              </tr>
            ) : (
              nodes.map((n) => (
                <tr key={n.id}>
                  <td>
                    <Link to={`/nodes/${n.id}`} style={{ fontWeight: 600 }}>{n.name}</Link>
                  </td>
                  <td>
                    <code>
                      {n.address}:{n.grpc_port}
                    </code>
                  </td>
                  <td>
                    <span className={`status status-${n.status || 'unknown'}`}>
                      {renderStatus(n.status || 'unknown')}
                    </span>
                  </td>
                  <td>
                    {(n.labels || []).length > 0 ? (
                      <div style={{ display: 'flex', gap: '0.25rem', flexWrap: 'wrap' }}>
                        {(n.labels || []).map((lbl) => (
                          <span key={lbl} className="status" style={{ fontSize: '0.75rem', padding: '0.1rem 0.4rem' }}>{lbl}</span>
                        ))}
                      </div>
                    ) : (
                      <span className="muted">—</span>
                    )}
                  </td>
                  <td className="actions">
                    <button type="button" className="btn-secondary" style={{ padding: '0.35rem 0.75rem', fontSize: '0.85rem' }} onClick={() => void onProbe(n.id)}>
                      探测
                    </button>
                    <button
                      type="button"
                      className="btn-danger"
                      style={{ padding: '0.35rem 0.75rem', fontSize: '0.85rem' }}
                      onClick={() => void onDelete(n.id, n.name)}
                    >
                      删除
                    </button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </section>
    </div>
  )
}
