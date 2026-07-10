import { FormEvent, useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  ApiError,
  bootstrapNode,
  deleteNode,
  getNodeInstallCommand,
  listNodes,
  probeNode,
  type Node,
  type NodeInstallInfo,
} from '../api/client'

export default function Nodes() {
  const [nodes, setNodes] = useState<Node[]>([])
  const [error, setError] = useState('')
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)

  // create / bootstrap form
  const [name, setName] = useState('')
  const [address, setAddress] = useState('')
  const [grpcPort, setGrpcPort] = useState(50051)
  const [labels, setLabels] = useState('')
  const [enableTLS, setEnableTLS] = useState(true)
  const [agentVersion, setAgentVersion] = useState('latest')

  const [installInfo, setInstallInfo] = useState<NodeInstallInfo | null>(null)
  const [copied, setCopied] = useState(false)

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

  async function onBootstrap(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    setMsg('')
    setCopied(false)
    try {
      const info = await bootstrapNode({
        name,
        address: address.trim() || undefined,
        grpc_port: grpcPort,
        labels: labels
          .split(',')
          .map((s) => s.trim())
          .filter(Boolean),
        enable_tls: enableTLS,
        agent_version: agentVersion.trim() || 'latest',
      })
      setInstallInfo(info)
      setName('')
      setAddress('')
      setLabels('')
      setMsg(`节点「${info.node.name}」已创建，请复制下方安装命令到服务器执行`)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建失败')
    } finally {
      setBusy(false)
    }
  }

  async function showInstall(id: string) {
    setError('')
    setMsg('')
    setCopied(false)
    try {
      const info = await getNodeInstallCommand(id)
      setInstallInfo(info)
      setMsg(`已生成节点「${info.node.name}」的安装命令`)
    } catch (err) {
      setError(err instanceof Error ? err.message : '获取安装命令失败')
    }
  }

  async function copyCommand() {
    if (!installInfo?.install_command) return
    try {
      await navigator.clipboard.writeText(installInfo.install_command)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      setError('复制失败，请手动选择命令文本')
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
      if (installInfo?.node.id === id) setInstallInfo(null)
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
      case 'pending':
        return '待安装'
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
        <h2>添加节点 · 一键安装</h2>
        <p className="muted" style={{ marginTop: 0 }}>
          创建节点并生成安装命令。目标机执行后会<strong>自动向 Panel 上报地址与 CA</strong>
          （需在「设置」填写 Public Base URL，如 <code>https://panel.example.com</code>）。
          地址可留空，由 Agent 探测上报。
        </p>
        <form className="form-grid" onSubmit={onBootstrap}>
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
            <label htmlFor="n-addr">节点地址（可选）</label>
            <input
              id="n-addr"
              value={address}
              onChange={(e) => setAddress(e.target.value)}
              placeholder="可先留空，装完再填 IP/域名"
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
            <label htmlFor="n-labels">节点标签（英文逗号分隔）</label>
            <input
              id="n-labels"
              value={labels}
              onChange={(e) => setLabels(e.target.value)}
              placeholder="例如: edge,prod,hk"
            />
          </div>
          <div className="form-row">
            <label htmlFor="n-ver">Agent 版本</label>
            <input
              id="n-ver"
              value={agentVersion}
              onChange={(e) => setAgentVersion(e.target.value)}
              placeholder="latest 或 v0.2.0"
            />
          </div>
          <div className="form-row">
            <label htmlFor="n-tls" style={{ display: 'flex', alignItems: 'center', cursor: 'pointer' }}>
              <input
                id="n-tls"
                type="checkbox"
                checked={enableTLS}
                onChange={(e) => setEnableTLS(e.target.checked)}
              />{' '}
              安装时启用 TLS（推荐，LADDER_TLS=1）
            </label>
          </div>
          <button type="submit" disabled={busy}>
            {busy ? '生成中…' : '添加节点并生成安装命令'}
          </button>
        </form>
      </section>

      {installInfo ? (
        <section className="card" style={{ borderColor: 'var(--accent)' }}>
          <div className="row-between">
            <h2 style={{ margin: 0 }}>安装命令 · {installInfo.node.name}</h2>
            <button type="button" className="btn-secondary" onClick={() => void copyCommand()}>
              {copied ? '已复制' : '复制命令'}
            </button>
          </div>
          <p className="muted" style={{ marginBottom: '0.5rem' }}>
            Token 已写入节点；命令含 <code>LADDER_TOKEN</code>
            {installInfo.enable_tls ? ' + TLS' : '（明文）'}
            {installInfo.enroll_enabled
              ? `；装机后自动 enroll 到 ${installInfo.panel_base_url || 'Panel'}。`
              : '。未配置 Public Base URL 时无法自动上报，请先到「设置」填写。'}
          </p>
          <pre
            className="install-cmd"
            style={{
              margin: 0,
              padding: '1rem',
              background: '#0f172a',
              color: '#e2e8f0',
              borderRadius: 8,
              overflow: 'auto',
              fontSize: '0.85rem',
              lineHeight: 1.5,
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
            }}
          >
            {installInfo.install_command}
          </pre>
          <ol style={{ marginTop: '1rem', paddingLeft: '1.25rem' }}>
            {installInfo.steps.map((s) => (
              <li key={s} style={{ marginBottom: '0.35rem' }}>
                {s}
              </li>
            ))}
          </ol>
          <p className="muted" style={{ marginBottom: 0 }}>
            节点详情：
            <Link to={`/nodes/${installInfo.node.id}`}> 编辑地址 / 粘贴 CA</Link>
          </p>
        </section>
      ) : null}

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
                    <Link to={`/nodes/${n.id}`} style={{ fontWeight: 600 }}>
                      {n.name}
                    </Link>
                  </td>
                  <td>
                    <code>
                      {n.address || '（待填）'}:{n.grpc_port}
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
                          <span
                            key={lbl}
                            className="status"
                            style={{ fontSize: '0.75rem', padding: '0.1rem 0.4rem' }}
                          >
                            {lbl}
                          </span>
                        ))}
                      </div>
                    ) : (
                      <span className="muted">—</span>
                    )}
                  </td>
                  <td className="actions">
                    <button
                      type="button"
                      className="btn-secondary"
                      style={{ padding: '0.35rem 0.75rem', fontSize: '0.85rem' }}
                      onClick={() => void showInstall(n.id)}
                    >
                      安装命令
                    </button>
                    <button
                      type="button"
                      className="btn-secondary"
                      style={{ padding: '0.35rem 0.75rem', fontSize: '0.85rem' }}
                      onClick={() => void onProbe(n.id)}
                    >
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
