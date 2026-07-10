import { FormEvent, useEffect, useState } from 'react'
import { bootstrapNode, type NodeInstallInfo } from '../api/client'

type Props = {
  open: boolean
  onClose: () => void
  onCreated: () => void
  onOpenDetail: (nodeId: string) => void
}

export default function AddNodeModal({ open, onClose, onCreated, onOpenDetail }: Props) {
  const [name, setName] = useState('')
  const [address, setAddress] = useState('')
  const [grpcPort, setGrpcPort] = useState(50051)
  const [labels, setLabels] = useState('')
  const [enableTLS, setEnableTLS] = useState(true)
  const [agentVersion, setAgentVersion] = useState('latest')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [installInfo, setInstallInfo] = useState<NodeInstallInfo | null>(null)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (!open) return
    setName('')
    setAddress('')
    setGrpcPort(50051)
    setLabels('')
    setEnableTLS(true)
    setAgentVersion('latest')
    setBusy(false)
    setError('')
    setInstallInfo(null)
    setCopied(false)
  }, [open])

  useEffect(() => {
    if (!open) return
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
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
      onCreated()
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建失败')
    } finally {
      setBusy(false)
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

  return (
    <div className="modal-overlay" role="presentation" onClick={onClose}>
      <div
        className="modal-panel"
        role="dialog"
        aria-modal="true"
        aria-labelledby="add-node-title"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="row-between" style={{ marginBottom: '0.75rem' }}>
          <h2 id="add-node-title" style={{ margin: 0 }}>
            {installInfo ? `安装命令 · ${installInfo.node.name}` : '添加节点'}
          </h2>
          <button type="button" className="btn-secondary" onClick={onClose}>
            关闭
          </button>
        </div>

        {error ? <div className="error">{error}</div> : null}

        {!installInfo ? (
          <>
            <p className="muted" style={{ marginTop: 0 }}>
              创建节点并生成安装命令。目标机执行后会<strong>自动向 Panel 上报地址与 CA</strong>
              （需在「设置」填写 Public Base URL）。地址可留空，由 Agent 探测上报。
            </p>
            <form className="form-grid" onSubmit={(e) => void onSubmit(e)}>
              <div className="form-row">
                <label htmlFor="add-n-name">节点名称</label>
                <input
                  id="add-n-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  required
                  placeholder="例如: 香港 01"
                />
              </div>
              <div className="form-row">
                <label htmlFor="add-n-addr">节点地址（可选）</label>
                <input
                  id="add-n-addr"
                  value={address}
                  onChange={(e) => setAddress(e.target.value)}
                  placeholder="可先留空，装完再填 IP/域名"
                />
              </div>
              <div className="form-row">
                <label htmlFor="add-n-port">gRPC 端口</label>
                <input
                  id="add-n-port"
                  type="number"
                  value={grpcPort}
                  onChange={(e) => setGrpcPort(Number(e.target.value))}
                />
              </div>
              <div className="form-row">
                <label htmlFor="add-n-labels">节点标签（英文逗号分隔）</label>
                <input
                  id="add-n-labels"
                  value={labels}
                  onChange={(e) => setLabels(e.target.value)}
                  placeholder="例如: edge,prod,hk"
                />
              </div>
              <div className="form-row">
                <label htmlFor="add-n-ver">Agent 版本</label>
                <input
                  id="add-n-ver"
                  value={agentVersion}
                  onChange={(e) => setAgentVersion(e.target.value)}
                  placeholder="latest 或 v0.2.0"
                />
              </div>
              <div className="form-row">
                <label htmlFor="add-n-tls" style={{ display: 'flex', alignItems: 'center', cursor: 'pointer' }}>
                  <input
                    id="add-n-tls"
                    type="checkbox"
                    checked={enableTLS}
                    onChange={(e) => setEnableTLS(e.target.checked)}
                  />{' '}
                  安装时启用 TLS（推荐）
                </label>
              </div>
              <button type="submit" disabled={busy}>
                {busy ? '生成中…' : '添加并生成安装命令'}
              </button>
            </form>
          </>
        ) : (
          <>
            <p className="muted" style={{ marginBottom: '0.5rem' }}>
              Token 已写入节点；命令含 <code>LADDER_TOKEN</code>
              {installInfo.enable_tls ? ' + TLS' : '（明文）'}
              {installInfo.enroll_enabled
                ? `；装机后自动 enroll 到 ${installInfo.panel_base_url || 'Panel'}。`
                : '。未配置 Public Base URL 时无法自动上报，请先到「设置」填写。'}
            </p>
            <div className="row-between" style={{ marginBottom: '0.5rem' }}>
              <strong>一键安装</strong>
              <button type="button" className="btn-secondary" onClick={() => void copyCommand()}>
                {copied ? '已复制' : '复制命令'}
              </button>
            </div>
            <pre className="install-cmd-block">{installInfo.install_command}</pre>
            <ol style={{ marginTop: '1rem', paddingLeft: '1.25rem' }}>
              {installInfo.steps.map((s) => (
                <li key={s} style={{ marginBottom: '0.35rem' }}>
                  {s}
                </li>
              ))}
            </ol>
            <div className="actions" style={{ marginTop: '1rem' }}>
              <button
                type="button"
                onClick={() => {
                  onOpenDetail(installInfo.node.id)
                  onClose()
                }}
              >
                打开节点详情
              </button>
              <button type="button" className="btn-secondary" onClick={onClose}>
                完成
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
