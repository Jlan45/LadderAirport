import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Alert, Button, Card, Form, Input, MessagePlugin } from 'tdesign-react'
import { ApiError, login } from '../api/client'

export default function Login() {
  const navigate = useNavigate()
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function doLogin() {
    if (!password) {
      setError('请输入密码')
      return
    }
    setError('')
    setBusy(true)
    try {
      await login(password)
      try {
        MessagePlugin.success('登录成功')
      } catch {
        // adapter missing / message host unavailable — navigation still proceeds
      }
      navigate('/', { replace: true })
    } catch (err) {
      const msg =
        err instanceof ApiError
          ? err.message || '登录失败'
          : err instanceof Error
            ? err.message
            : '登录失败'
      setError(msg)
      try {
        MessagePlugin.error(msg)
      } catch {
        // keep inline Alert as fallback
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="la-login">
      <Card bordered className="la-login-card" hoverShadow>
        <h1 className="la-login-title">Ladder Airport</h1>
        <p className="la-login-sub">登录运维控制台</p>
        {error ? (
          <Alert
            theme="error"
            message={error}
            style={{ marginBottom: 16 }}
            close
            onClose={() => setError('')}
          />
        ) : null}
        <Form
          labelAlign="top"
          preventSubmitDefault
          onSubmit={() => {
            void doLogin()
          }}
        >
          <Form.FormItem label="密码" name="password">
            <Input
              type="password"
              size="large"
              autocomplete="current-password"
              value={password}
              onChange={(v) => setPassword(String(v))}
              placeholder="请输入管理员密码"
              clearable
              autofocus
              onEnter={() => void doLogin()}
            />
          </Form.FormItem>
          <Form.FormItem style={{ marginBottom: 0, marginTop: 8 }}>
            <Button
              theme="primary"
              type="submit"
              block
              size="large"
              loading={busy}
              disabled={!password}
            >
              登录
            </Button>
          </Form.FormItem>
        </Form>
      </Card>
    </div>
  )
}
