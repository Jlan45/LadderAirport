import { Navigate, NavLink, Outlet, Route, Routes, useNavigate } from 'react-router-dom'
import { useEffect, useState } from 'react'
import { ApiError, listNodes } from './api/client'
import Login from './pages/Login'
import Dashboard from './pages/Dashboard'
import Nodes from './pages/Nodes'
import Inbounds from './pages/Inbounds'
import NodeDetail from './pages/NodeDetail'
import Batch from './pages/Batch'
import Subscriptions from './pages/Subscriptions'
import Settings from './pages/Settings'

/** Layout with nav; redirects to /login if session is invalid. */
function AppLayout() {
  const navigate = useNavigate()
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

  if (!ready) {
    return (
      <div className="login-page">
        <p className="muted">加载中…</p>
      </div>
    )
  }

  if (!authed) {
    return null
  }

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">Labber Airport</div>
        <nav>
          <NavLink to="/" end>
            总览
          </NavLink>
          <NavLink to="/nodes" end={false}>
            节点
          </NavLink>
          <NavLink to="/inbounds">入站</NavLink>
          <NavLink to="/subscriptions">订阅</NavLink>
          <NavLink to="/batch">批量操作</NavLink>
          <NavLink to="/settings">设置</NavLink>
        </nav>
      </header>
      <main className="main">
        <Outlet />
      </main>
    </div>
  )
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route element={<AppLayout />}>
        <Route path="/" element={<Dashboard />} />
        <Route path="/nodes" element={<Nodes />} />
        <Route path="/nodes/:id" element={<NodeDetail />} />
        <Route path="/inbounds" element={<Inbounds />} />
        <Route path="/subscriptions" element={<Subscriptions />} />
        <Route path="/batch" element={<Batch />} />
        <Route path="/settings" element={<Settings />} />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
