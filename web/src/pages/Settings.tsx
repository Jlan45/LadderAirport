import { FormEvent, useCallback, useEffect, useState } from 'react'
import { getSettings, putSettings } from '../api/client'

export default function Settings() {
  const [token, setToken] = useState('')
  const [timeoutSec, setTimeoutSec] = useState(10)
  const [concurrency, setConcurrency] = useState(8)
  const [listenAddr, setListenAddr] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [error, setError] = useState('')
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    setError('')
    try {
      const s = await getSettings()
      setToken(s.default_agent_token || '')
      setTimeoutSec(s.grpc_timeout_sec)
      setConcurrency(s.max_concurrency)
      setListenAddr(s.listen_addr || '')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to load settings')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  async function onSave(e: FormEvent) {
    e.preventDefault()
    setError('')
    setMsg('')
    if (newPassword && newPassword !== confirmPassword) {
      setError('passwords do not match')
      return
    }
    setBusy(true)
    try {
      const body: Parameters<typeof putSettings>[0] = {
        default_agent_token: token,
        grpc_timeout_sec: timeoutSec,
        max_concurrency: concurrency,
        listen_addr: listenAddr,
      }
      if (newPassword) {
        body.new_password = newPassword
      }
      const s = await putSettings(body)
      setToken(s.default_agent_token || '')
      setTimeoutSec(s.grpc_timeout_sec)
      setConcurrency(s.max_concurrency)
      setListenAddr(s.listen_addr || '')
      setNewPassword('')
      setConfirmPassword('')
      setMsg('Settings saved')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'save failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div>
      <h1>Settings</h1>
      {error ? <div className="error">{error}</div> : null}
      {msg ? <div className="ok">{msg}</div> : null}

      <section className="card">
        <form onSubmit={onSave}>
          <div className="form-row">
            <label htmlFor="tok">Default agent token</label>
            <input
              id="tok"
              type="password"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              autoComplete="off"
            />
          </div>
          <div className="form-row">
            <label htmlFor="to">gRPC timeout (seconds)</label>
            <input
              id="to"
              type="number"
              min={1}
              value={timeoutSec}
              onChange={(e) => setTimeoutSec(Number(e.target.value))}
              required
            />
          </div>
          <div className="form-row">
            <label htmlFor="cc">Max concurrency</label>
            <input
              id="cc"
              type="number"
              min={1}
              value={concurrency}
              onChange={(e) => setConcurrency(Number(e.target.value))}
              required
            />
          </div>
          <div className="form-row">
            <label htmlFor="la">Listen address</label>
            <input
              id="la"
              value={listenAddr}
              onChange={(e) => setListenAddr(e.target.value)}
              placeholder=":8080"
            />
            <div className="field-hint">
              Stored for reference; changing may require panel restart.
            </div>
          </div>

          <h3 className="subhead">Change password</h3>
          <div className="form-row">
            <label htmlFor="np">New password</label>
            <input
              id="np"
              type="password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              autoComplete="new-password"
            />
          </div>
          <div className="form-row">
            <label htmlFor="cp">Confirm new password</label>
            <input
              id="cp"
              type="password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              autoComplete="new-password"
            />
          </div>

          <button type="submit" disabled={busy}>
            {busy ? 'Saving…' : 'Save'}
          </button>
        </form>
      </section>
    </div>
  )
}
