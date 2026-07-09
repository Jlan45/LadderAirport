import { FormEvent, useCallback, useEffect, useState } from 'react'
import {
  createSubscription,
  deleteSubscription,
  listInbounds,
  listSubscriptions,
  previewSubscription,
  updateSubscription,
  type InboundConfig,
  type Subscription,
} from '../api/client'

export default function Subscriptions() {
  const [list, setList] = useState<Subscription[]>([])
  const [inbounds, setInbounds] = useState<InboundConfig[]>([])
  const [error, setError] = useState('')
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)
  const [preview, setPreview] = useState('')

  const [name, setName] = useState('')
  const [format, setFormat] = useState<'clash' | 'singbox'>('clash')
  const [selectedInbounds, setSelectedInbounds] = useState<Set<string>>(new Set())
  const [allInbounds, setAllInbounds] = useState(true)

  const load = useCallback(async () => {
    setError('')
    try {
      const [subs, ins] = await Promise.all([listSubscriptions(), listInbounds()])
      setList(subs ?? [])
      setInbounds(ins ?? [])
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  function toggleInbound(id: string) {
    setSelectedInbounds((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  async function onCreate(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    setMsg('')
    try {
      const body = {
        name,
        format,
        inbound_ids: allInbounds ? [] : Array.from(selectedInbounds),
        enabled: true,
      }
      const sub = await createSubscription(body)
      setName('')
      setMsg(`已创建订阅：${sub.url || sub.token}`)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建失败')
    } finally {
      setBusy(false)
    }
  }

  async function onCopy(url?: string) {
    if (!url) return
    try {
      await navigator.clipboard.writeText(url)
      setMsg('订阅链接已复制')
    } catch {
      setMsg(url)
    }
  }

  async function onPreview(id: string) {
    setError('')
    try {
      const text = await previewSubscription(id)
      setPreview(text)
    } catch (err) {
      setError(err instanceof Error ? err.message : '预览失败')
    }
  }

  async function onRotate(id: string) {
    if (!confirm('轮换 Token 后旧链接将失效，继续？')) return
    try {
      const sub = await updateSubscription(id, { rotate_token: true })
      setMsg(`新链接：${sub.url}`)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '轮换失败')
    }
  }

  async function onToggle(sub: Subscription) {
    try {
      await updateSubscription(sub.id, { enabled: !sub.enabled })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '更新失败')
    }
  }

  async function onDelete(sub: Subscription) {
    if (!confirm(`删除订阅「${sub.name}」？`)) return
    try {
      await deleteSubscription(sub.id)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除失败')
    }
  }

  return (
    <div>
      <h1>订阅</h1>
      <p className="muted">
        生成 Clash / sing-box 客户端订阅。基础 CN 分流：局域网与中国大陆直连，其余走代理。
        节点 <code>Address</code> 会作为客户端连接的服务器地址。
      </p>
      {error ? <div className="error">{error}</div> : null}
      {msg ? <div className="ok">{msg}</div> : null}

      <section className="card">
        <h2>创建订阅</h2>
        <form onSubmit={onCreate}>
          <div className="form-grid">
            <div className="form-row">
              <label htmlFor="sub-name">名称</label>
              <input
                id="sub-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                required
                placeholder="例如: 主力 Clash"
              />
            </div>
            <div className="form-row">
              <label htmlFor="sub-fmt">格式</label>
              <select
                id="sub-fmt"
                value={format}
                onChange={(e) => setFormat(e.target.value as 'clash' | 'singbox')}
              >
                <option value="clash">Clash / Mihomo (YAML)</option>
                <option value="singbox">sing-box (JSON)</option>
              </select>
            </div>
          </div>

          <div className="form-row">
            <label>
              <input
                type="checkbox"
                checked={allInbounds}
                onChange={(e) => setAllInbounds(e.target.checked)}
              />{' '}
              包含所有已启用入站（按节点关联展开）
            </label>
          </div>

          {!allInbounds ? (
            <div className="check-list">
              {inbounds.length === 0 ? (
                <p className="muted">暂无入站配置</p>
              ) : (
                inbounds.map((inb) => (
                  <label key={inb.id} className="check-item">
                    <input
                      type="checkbox"
                      checked={selectedInbounds.has(inb.id)}
                      onChange={() => toggleInbound(inb.id)}
                    />
                    <span>
                      {inb.name} <code>{inb.protocol}</code>
                      {!inb.enabled ? ' (已禁用)' : ''}
                    </span>
                  </label>
                ))
              )}
            </div>
          ) : null}

          <button type="submit" disabled={busy}>
            创建订阅
          </button>
        </form>
      </section>

      <section className="card">
        <div className="row-between">
          <h2>订阅列表</h2>
          <button type="button" className="btn-secondary" onClick={() => void load()}>
            刷新
          </button>
        </div>
        {list.length === 0 ? (
          <p className="muted">暂无订阅</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>名称</th>
                <th>格式</th>
                <th>状态</th>
                <th>链接</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {list.map((sub) => (
                <tr key={sub.id}>
                  <td style={{ fontWeight: 600 }}>{sub.name}</td>
                  <td>
                    <code>{sub.format}</code>
                  </td>
                  <td>
                    <span className={`status ${sub.enabled ? 'status-success' : 'status-failed'}`}>
                      {sub.enabled ? '启用' : '禁用'}
                    </span>
                  </td>
                  <td>
                    <code className="params-preview" style={{ fontSize: '0.75rem' }}>
                      {sub.url}
                    </code>
                  </td>
                  <td className="actions">
                    <button type="button" className="btn-secondary" onClick={() => void onCopy(sub.url)}>
                      复制
                    </button>
                    <button type="button" className="btn-secondary" onClick={() => void onPreview(sub.id)}>
                      预览
                    </button>
                    <button type="button" className="btn-secondary" onClick={() => void onToggle(sub)}>
                      {sub.enabled ? '禁用' : '启用'}
                    </button>
                    <button type="button" className="btn-secondary" onClick={() => void onRotate(sub.id)}>
                      轮换 Token
                    </button>
                    <button type="button" className="btn-danger" onClick={() => void onDelete(sub)}>
                      删除
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      {preview ? (
        <section className="card">
          <div className="row-between">
            <h2>配置预览</h2>
            <button type="button" className="btn-secondary" onClick={() => setPreview('')}>
              关闭
            </button>
          </div>
          <pre className="code-block">{preview}</pre>
        </section>
      ) : null}
    </div>
  )
}
