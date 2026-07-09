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
      setError(err instanceof Error ? err.message : 'failed to load')
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
      setError('select a protocol')
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
      await createInbound({
        name,
        protocol,
        params: clean,
        enabled,
      })
      setName('')
      setMsg('Inbound created')
      if (selectedTemplate) {
        setParams(defaultsFromFields(selectedTemplate.fields))
      }
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'create failed')
    } finally {
      setBusy(false)
    }
  }

  async function onDelete(id: string, n: string) {
    if (!confirm(`Delete inbound "${n}"?`)) return
    try {
      await deleteInbound(id)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'delete failed')
    }
  }

  return (
    <div>
      <h1>Inbounds</h1>
      {error ? <div className="error">{error}</div> : null}
      {msg ? <div className="ok">{msg}</div> : null}

      <section className="card">
        <h2>Create inbound</h2>
        <form onSubmit={onCreate}>
          <div className="form-grid">
            <div className="form-row">
              <label htmlFor="in-name">Name</label>
              <input
                id="in-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                required
              />
            </div>
            <div className="form-row">
              <label htmlFor="in-proto">Protocol</label>
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
            <div className="form-row">
              <label htmlFor="in-en">
                <input
                  id="in-en"
                  type="checkbox"
                  checked={enabled}
                  onChange={(e) => setEnabled(e.target.checked)}
                />{' '}
                Enabled
              </label>
            </div>
          </div>

          {selectedTemplate ? (
            <>
              <h3 className="subhead">{selectedTemplate.name} parameters</h3>
              <DynamicForm
                fields={selectedTemplate.fields}
                value={params}
                onChange={setParams}
              />
            </>
          ) : null}

          <button type="submit" disabled={busy || !protocol}>
            Create
          </button>
        </form>
      </section>

      <section className="card">
        <div className="row-between">
          <h2>Library</h2>
          <button type="button" className="btn-secondary" onClick={() => void load()}>
            Refresh
          </button>
        </div>
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Protocol</th>
              <th>Enabled</th>
              <th>Params</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {inbounds.length === 0 ? (
              <tr>
                <td colSpan={5} className="muted">
                  No inbounds yet
                </td>
              </tr>
            ) : (
              inbounds.map((inb) => (
                <tr key={inb.id}>
                  <td>{inb.name}</td>
                  <td>
                    <code>{inb.protocol}</code>
                  </td>
                  <td>{inb.enabled ? 'yes' : 'no'}</td>
                  <td>
                    <code className="params-preview">
                      {summarizeParams(inb.params)}
                    </code>
                  </td>
                  <td className="actions">
                    <button
                      type="button"
                      className="btn-danger"
                      onClick={() => void onDelete(inb.id, inb.name)}
                    >
                      Delete
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

function summarizeParams(params: Record<string, unknown> | null | undefined): string {
  if (!params) return '—'
  const parts: string[] = []
  for (const [k, v] of Object.entries(params)) {
    if (k === 'password' || k === 'private_key') {
      parts.push(`${k}=***`)
    } else {
      parts.push(`${k}=${String(v)}`)
    }
  }
  const s = parts.join(' ')
  return s.length > 80 ? s.slice(0, 77) + '…' : s || '—'
}
