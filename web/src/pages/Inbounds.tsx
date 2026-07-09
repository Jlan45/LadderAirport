import { FormEvent, useCallback, useEffect, useMemo, useState } from 'react'
import {
  createInbound,
  deleteInbound,
  listInbounds,
  listTemplates,
  type InboundConfig,
  type Template,
} from '../api/client'
import DynamicForm, { defaultsFromFields } from '../components/DynamicForm'

export default function Inbounds() {
  const [inbounds, setInbounds] = useState<InboundConfig[]>([])
  const [templates, setTemplates] = useState<Template[]>([])
  const [error, setError] = useState('')
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)

  const [name, setName] = useState('')
  const [protocol, setProtocol] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [params, setParams] = useState<Record<string, unknown>>({})
  const [lastCreated, setLastCreated] = useState<InboundConfig | null>(null)
  const [expandedId, setExpandedId] = useState<string | null>(null)

  const selectedTemplate = useMemo(
    () => templates.find((t) => t.protocol === protocol),
    [templates, protocol],
  )

  const load = useCallback(async () => {
    setError('')
    try {
      const [ins, tmpls] = await Promise.all([listInbounds(), listTemplates()])
      setInbounds(ins ?? [])
      const tlist = tmpls ?? []
      setTemplates(tlist)
      if (!protocol && tlist.length > 0) {
        setProtocol(tlist[0].protocol)
        setParams(defaultsFromFields(tlist[0].fields))
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    }
  }, [protocol])

  useEffect(() => {
    void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- initial load only
  }, [])

  function onProtocolChange(p: string) {
    setProtocol(p)
    const t = templates.find((x) => x.protocol === p)
    if (t) setParams(defaultsFromFields(t.fields))
  }

  async function onCreate(e: FormEvent) {
    e.preventDefault()
    if (!protocol) {
      setError('请选择传输协议')
      return
    }
    setBusy(true)
    setError('')
    setMsg('')
    try {
      // Coerce empty int fields out; keep numbers as numbers.
      const clean: Record<string, unknown> = {}
      for (const [k, v] of Object.entries(params)) {
        if (v === '') continue
        clean[k] = v
      }
      const created = await createInbound({
        name,
        protocol,
        params: clean,
        enabled,
      })
      setName('')
      setLastCreated(created)
      setMsg('入站配置创建成功；密码 / UUID / 证书密钥已自动生成，请在下方查看')
      if (selectedTemplate) {
        setParams(defaultsFromFields(selectedTemplate.fields))
      }
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建失败')
    } finally {
      setBusy(false)
    }
  }

  async function onDelete(id: string, n: string) {
    if (!confirm(`确定要删除入站配置 "${n}" 吗？`)) return
    try {
      await deleteInbound(id)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除失败')
    }
  }

  return (
    <div>
      <h1>入站配置管理</h1>
      {error ? <div className="error">{error}</div> : null}
      {msg ? <div className="ok">{msg}</div> : null}

      <section className="card">
        <h2>创建入站配置</h2>
        <p className="muted" style={{ marginTop: 0 }}>
          只需填写名称、协议和端口等基础项。密码、UUID、TLS 证书、Reality 密钥会在服务端自动生成。
        </p>
        <form onSubmit={onCreate}>
          <div className="form-grid">
            <div className="form-row">
              <label htmlFor="in-name">配置名称</label>
              <input
                id="in-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                required
                placeholder="例如: ss-edge-1"
              />
            </div>
            <div className="form-row">
              <label htmlFor="in-proto">传输协议</label>
              <select
                id="in-proto"
                value={protocol}
                onChange={(e) => onProtocolChange(e.target.value)}
                required
              >
                {templates.map((t) => (
                  <option key={t.id} value={t.protocol}>
                    {t.name} ({t.protocol})
                  </option>
                ))}
              </select>
            </div>
            <div className="form-row" style={{ display: 'flex', alignItems: 'flex-end', paddingBottom: '0.2rem' }}>
              <label htmlFor="in-en" style={{ display: 'flex', alignItems: 'center', cursor: 'pointer', margin: 0 }}>
                <input
                  id="in-en"
                  type="checkbox"
                  checked={enabled}
                  onChange={(e) => setEnabled(e.target.checked)}
                />{' '}
                立即启用此配置
              </label>
            </div>
          </div>

          {selectedTemplate ? (
            <>
              <h3 className="subhead">{selectedTemplate.name} 参数配置</h3>
              <DynamicForm
                fields={selectedTemplate.fields}
                value={params}
                onChange={setParams}
              />
            </>
          ) : null}

          <button type="submit" disabled={busy || !protocol} style={{ marginTop: '0.5rem' }}>
            创建入站配置
          </button>
        </form>
        {lastCreated ? (
          <div className="card" style={{ marginTop: '1rem', background: 'var(--surface-2, #1a1f2e)' }}>
            <h3 className="subhead">已生成凭据 — {lastCreated.name}</h3>
            <pre className="params-preview" style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
              {formatSecrets(lastCreated.params)}
            </pre>
          </div>
        ) : null}
      </section>

      <section className="card">
        <div className="row-between">
          <h2>已有入站</h2>
          <button type="button" className="btn-secondary" onClick={() => void load()}>
            刷新列表
          </button>
        </div>
        <table>
          <thead>
            <tr>
              <th>配置名称</th>
              <th>传输协议</th>
              <th>是否启用</th>
              <th>核心参数预览</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {inbounds.length === 0 ? (
              <tr>
                <td colSpan={5} className="muted" style={{ textAlign: 'center', padding: '2rem' }}>
                  暂无入站配置数据
                </td>
              </tr>
            ) : (
              inbounds.map((inb) => (
                <tr key={inb.id}>
                  <td style={{ fontWeight: 600 }}>{inb.name}</td>
                  <td>
                    <code>{inb.protocol}</code>
                  </td>
                  <td>
                    <span className={`status ${inb.enabled ? 'status-success' : 'status-failed'}`}>
                      {inb.enabled ? '已启用' : '已禁用'}
                    </span>
                  </td>
                  <td>
                    <code className="params-preview">
                      {summarizeParams(inb.params)}
                    </code>
                    {expandedId === inb.id ? (
                      <pre
                        className="params-preview"
                        style={{
                          display: 'block',
                          marginTop: '0.5rem',
                          whiteSpace: 'pre-wrap',
                          wordBreak: 'break-all',
                        }}
                      >
                        {formatSecrets(inb.params)}
                      </pre>
                    ) : null}
                  </td>
                  <td className="actions">
                    <button
                      type="button"
                      className="btn-secondary"
                      style={{ padding: '0.35rem 0.75rem', fontSize: '0.85rem' }}
                      onClick={() =>
                        setExpandedId((id) => (id === inb.id ? null : inb.id))
                      }
                    >
                      {expandedId === inb.id ? '隐藏凭据' : '查看凭据'}
                    </button>
                    <button
                      type="button"
                      className="btn-danger"
                      style={{ padding: '0.35rem 0.75rem', fontSize: '0.85rem' }}
                      onClick={() => void onDelete(inb.id, inb.name)}
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

const SECRET_KEYS = new Set([
  'password',
  'private_key',
  'public_key',
  'uuid',
  'short_id',
  'tls_cert_pem',
  'tls_key_pem',
  'tls_cert_path',
  'tls_key_path',
])

function summarizeParams(params: Record<string, unknown> | null | undefined): string {
  if (!params) return '—'
  const parts: string[] = []
  for (const [k, v] of Object.entries(params)) {
    if (SECRET_KEYS.has(k) || k.endsWith('_pem')) {
      parts.push(`${k}=***`)
    } else {
      parts.push(`${k}=${String(v)}`)
    }
  }
  const s = parts.join(' ')
  return s.length > 80 ? s.slice(0, 77) + '…' : s || '—'
}

function formatSecrets(params: Record<string, unknown> | null | undefined): string {
  if (!params) return '—'
  const lines: string[] = []
  const order = [
    'listen',
    'port',
    'method',
    'password',
    'uuid',
    'tls_mode',
    'private_key',
    'short_id',
    'server_name',
    'handshake_server',
    'handshake_server_port',
    'flow',
    'network',
    'up_mbps',
    'down_mbps',
  ]
  const seen = new Set<string>()
  for (const k of order) {
    if (params[k] === undefined || params[k] === null || params[k] === '') continue
    seen.add(k)
    if (k === 'tls_cert_pem' || k === 'tls_key_pem') continue
    lines.push(`${k}: ${String(params[k])}`)
  }
  if (params.tls_cert_pem) {
    lines.push('tls_cert_pem: <auto self-signed PEM, applied inline>')
  }
  if (params.tls_key_pem) {
    lines.push('tls_key_pem: <auto private key PEM, applied inline>')
  }
  for (const [k, v] of Object.entries(params)) {
    if (seen.has(k) || k.endsWith('_pem')) continue
    lines.push(`${k}: ${String(v)}`)
  }
  return lines.join('\n') || '—'
}
