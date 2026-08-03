import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { ApiError, listNodes, login } from '../api/client'
import { toast } from '../lib/toast'
import { LOGO_DARK_URL, LOGO_LIGHT_URL } from '../lib/logos'

export default function Login() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const nextPath = safeNextPath(searchParams.get('next'))

  const [logo, setLogo] = useState(LOGO_LIGHT_URL)

  useEffect(() => {
    document.title = '登录 · Ladder Airport'
    const isDark = document.documentElement.classList.contains('dark') || 
                   localStorage.getItem('la-theme') === 'dark'
    setLogo(isDark ? LOGO_DARK_URL : LOGO_LIGHT_URL)
  }, [])

  useEffect(() => {
    let cancelled = false
    void listNodes()
      .then(() => {
        if (!cancelled) navigate(nextPath, { replace: true })
      })
      .catch(() => {
        // An expired session or network failure leaves form usable
      })
    return () => {
      cancelled = true
    }
  }, [navigate, nextPath])

  async function doLogin() {
    if (busy) return
    if (!password) {
      setError('请输入密码')
      return
    }
    setError('')
    setBusy(true)
    try {
      await login(password)
      toast.success('登录成功')
      navigate(nextPath, { replace: true })
    } catch (err) {
      const msg =
        err instanceof ApiError
          ? err.status === 401
            ? '密码错误，请重试'
            : err.message || '登录失败'
          : err instanceof Error
            ? err.message
            : '登录失败'
      setError(msg)
      toast.error(msg)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="min-h-screen bg-background flex flex-col items-center justify-center p-6 selection:bg-accent selection:text-foreground">
      <Card className="w-full max-w-md bg-card border-border shadow-xl overflow-hidden relative">
        <div className="absolute top-0 left-0 right-0 h-[2px] bg-gradient-to-r from-primary/20 via-primary to-primary/20" />
        <CardHeader className="space-y-4 pt-8 pb-4 text-center">
          <div className="flex justify-center" aria-hidden="true">
            <img src={logo} alt="Logo" className="w-12 h-12 object-contain rounded-lg" />
          </div>
          <div className="space-y-1.5">
            <CardTitle className="text-2xl font-bold tracking-tight text-foreground">Ladder Airport</CardTitle>
            <CardDescription className="text-sm text-muted-foreground">登录运维控制台</CardDescription>
          </div>
        </CardHeader>

        <CardContent className="space-y-4 pb-8">
          {error ? (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          ) : null}

          <form
            onSubmit={(e) => {
              e.preventDefault()
              void doLogin()
            }}
            className="space-y-4"
          >
            <div className="space-y-1.5">
              <Label htmlFor="login-password" className="font-medium">
                密码
              </Label>
              <Input
                id="login-password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="请输入管理员密码"
                autoFocus
                disabled={busy}
              />
            </div>

            <Button
              type="submit"
              className="w-full mt-2"
              loading={busy}
              disabled={!password || busy}
            >
              登录
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}

function safeNextPath(requested: string | null): string {
  if (
    requested &&
    requested.startsWith('/') &&
    !requested.startsWith('//') &&
    !requested.includes('\\') &&
    !requested.startsWith('/login')
  ) {
    return requested
  }
  return '/'
}
