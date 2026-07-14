import { Navigate, Outlet, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { useEffect, useMemo, useState } from 'react'
import { Layout, Loading, Menu } from 'tdesign-react'
import {
  RootListIcon,
  SettingIcon,
  ShareIcon,
  SystemSettingIcon,
} from 'tdesign-icons-react'
import { ApiError, listNodes } from './api/client'
import Login from './pages/Login'
import Fleet from './pages/Fleet'
import Inbounds from './pages/Inbounds'
import Subscriptions from './pages/Subscriptions'
import Settings from './pages/Settings'

const { Header, Content } = Layout
const { HeadMenu, MenuItem } = Menu

const NAV = [
  { path: '/', label: '节点', icon: <RootListIcon /> },
  { path: '/inbounds', label: '入站', icon: <SystemSettingIcon /> },
  { path: '/subscriptions', label: '订阅', icon: <ShareIcon /> },
  { path: '/settings', label: '设置', icon: <SettingIcon /> },
] as const

/** Layout with nav; redirects to /login if session is invalid. */
function AppLayout() {
  const navigate = useNavigate()
  const location = useLocation()
  const [ready, setReady] = useState(false)
  const [authed, setAuthed] = useState(false)

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        await listNodes()
        if (!cancelled) {
          setAuthed(true)
          setReady(true)
        }
      } catch (err) {
        if (!cancelled) {
          if (err instanceof ApiError && err.status === 401) {
            setAuthed(false)
          } else {
            // Network or other error — still allow shell; pages will surface errors.
            setAuthed(true)
          }
          setReady(true)
        }
      }
    })()
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    if (ready && !authed) {
      navigate('/login', { replace: true })
    }
  }, [ready, authed, navigate])

  const active = useMemo(() => {
    const p = location.pathname
    if (p.startsWith('/inbounds')) return '/inbounds'
    if (p.startsWith('/subscriptions')) return '/subscriptions'
    if (p.startsWith('/settings')) return '/settings'
    return '/'
  }, [location.pathname])

  if (!ready) {
    return (
      <div className="la-loading-page">
        <Loading text="加载中…" size="medium" />
      </div>
    )
  }

  if (!authed) {
    return null
  }

  return (
    <Layout className="la-layout">
      <Header className="la-header">
        <HeadMenu
          theme="light"
          value={active}
          onChange={(v: string | number) => navigate(String(v))}
          className="la-header-menu"
          logo={<div className="la-brand">Ladder Airport</div>}
        >
          {NAV.map((item) => (
            <MenuItem key={item.path} value={item.path} icon={item.icon}>
              {item.label}
            </MenuItem>
          ))}
        </HeadMenu>
      </Header>
      <Content className="la-content">
        <Outlet />
      </Content>
    </Layout>
  )
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route element={<AppLayout />}>
        <Route path="/" element={<Fleet />} />
        <Route path="/inbounds" element={<Inbounds />} />
        <Route path="/subscriptions" element={<Subscriptions />} />
        <Route path="/settings" element={<Settings />} />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
