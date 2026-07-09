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
      setError(err instanceof Error ? err.message : '加载系统设置失败')
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
      setError('两次输入的密码不一致')
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
      setMsg('系统设置保存成功')
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div>
      <h1>系统设置</h1>
      {error ? <div className="error">{error}</div> : null}
      {msg ? <div className="ok">{msg}</div> : null}

      <section className="card">
        <form onSubmit={onSave}>
          <div className="form-row">
            <label htmlFor="tok">默认 Agent 访问令牌</label>
            <input
              id="tok"
              type="password"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              autoComplete="off"
              placeholder="全局默认的连接校验令牌"
            />
          </div>
          <div className="form-row">
            <label htmlFor="to">gRPC 超时时间 (秒)</label>
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
            <label htmlFor="cc">最大并发任务数</label>
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
            <label htmlFor="la">面板监听地址</label>
            <input
              id="la"
              value={listenAddr}
              onChange={(e) => setListenAddr(e.target.value)}
              placeholder="例如: :8080"
            />
            <div className="field-hint">
              仅保存做参考之用；更改此项可能需要手动重启面板服务。
            </div>
          </div>

          <h3 className="subhead">修改管理员密码</h3>
          <div className="form-row">
            <label htmlFor="np">新密码</label>
            <input
              id="np"
              type="password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              autoComplete="new-password"
              placeholder="留空则不修改密码"
            />
          </div>
          <div className="form-row">
            <label htmlFor="cp">确认新密码</label>
            <input
              id="cp"
              type="password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              autoComplete="new-password"
              placeholder="再次输入新密码以确认"
            />
          </div>

          <button type="submit" disabled={busy} style={{ marginTop: '0.5rem' }}>
            {busy ? '保存中…' : '保存设置'}
          </button>
        </form>
      </section>
    </div>
  )
}
