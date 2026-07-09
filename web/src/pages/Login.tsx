import { FormEvent, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ApiError, login } from '../api/client'

export default function Login() {
  const navigate = useNavigate()
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setBusy(true)
    try {
      await login(password)
      navigate('/nodes', { replace: true })
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message || '登录失败')
      } else {
        setError(err instanceof Error ? err.message : '登录失败')
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="login-page">
      <form className="card login-card" onSubmit={onSubmit}>
        <h1>Ladder Airport</h1>
        <p className="muted">登录运维控制台</p>
        <div className="form-row">
          <label htmlFor="password">密码</label>
          <input
            id="password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            autoFocus
          />
        </div>
        {error ? <div className="error">{error}</div> : null}
        <button type="submit" disabled={busy || !password}>
          {busy ? '登录中…' : '登录'}
        </button>
      </form>
    </div>
  )
}
