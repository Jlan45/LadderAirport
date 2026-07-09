import { FormEvent, useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  ApiError,
  createNode,
  deleteNode,
  listNodes,
  probeNode,
  type Node,
} from '../api/client'

export default function Nodes() {
  const [nodes, setNodes] = useState<Node[]>([])
  const [error, setError] = useState('')
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)

  // create form
  const [name, setName] = useState('')
  const [address, setAddress] = useState('')
  const [grpcPort, setGrpcPort] = useState(50051)
  const [token, setToken] = useState('')
  const [labels, setLabels] = useState('')
  const [tlsSkip, setTlsSkip] = useState(true)

  const load = useCallback(async () => {
    setError('')
    try {
      const list = await listNodes()
      setNodes(list ?? [])
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to load nodes')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  async function onCreate(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    setMsg('')
    try {
      await createNode({
        name,
        address,
        grpc_port: grpcPort,
        token: token || undefined,
        labels: labels
          .split(',')
          .map((s) => s.trim())
          .filter(Boolean),
        tls_skip_verify: tlsSkip,
      })
      setName('')
      setAddress('')
      setToken('')
      setLabels('')
      setMsg('Node created')
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'create failed')
    } finally {
      setBusy(false)
    }
  }

  async function onProbe(id: string) {
    setError('')
    setMsg('')
    try {
      const r = await probeNode(id)
      setMsg(
        `Probe OK: agent=${r.agent_version} singbox=${r.singbox_version} status=${r.node.status}`,
      )
      await load()
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message)
      } else {
        setError(err instanceof Error ? err.message : 'probe failed')
      }
      await load()
    }
  }

  async function onDelete(id: string, n: string) {
    if (!confirm(`Delete node "${n}"?`)) return
    setError('')
    try {
      await deleteNode(id)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'delete failed')
    }
  }

  return (
    <div>
      <h1>Nodes</h1>
      {error ? <div className="error">{error}</div> : null}
      {msg ? <div className="ok">{msg}</div> : null}

      <section className="card">
        <h2>Create node</h2>
        <form className="form-grid" onSubmit={onCreate}>
          <div className="form-row">
            <label htmlFor="n-name">Name</label>
            <input
              id="n-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              required
            />
          </div>
          <div className="form-row">
            <label htmlFor="n-addr">Address</label>
            <input
              id="n-addr"
              value={address}
              onChange={(e) => setAddress(e.target.value)}
              required
              placeholder="10.0.0.1"
            />
          </div>
          <div className="form-row">
            <label htmlFor="n-port">gRPC port</label>
            <input
              id="n-port"
              type="number"
              value={grpcPort}
              onChange={(e) => setGrpcPort(Number(e.target.value))}
            />
          </div>
          <div className="form-row">
            <label htmlFor="n-token">Token (optional override)</label>
            <input
              id="n-token"
              type="password"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              autoComplete="off"
            />
          </div>
          <div className="form-row">
            <label htmlFor="n-labels">Labels (comma-separated)</label>
            <input
              id="n-labels"
              value={labels}
              onChange={(e) => setLabels(e.target.value)}
              placeholder="edge,prod"
            />
          </div>
          <div className="form-row">
            <label htmlFor="n-tls">
              <input
                id="n-tls"
                type="checkbox"
                checked={tlsSkip}
                onChange={(e) => setTlsSkip(e.target.checked)}
              />{' '}
              TLS skip verify
            </label>
          </div>
          <button type="submit" disabled={busy}>
            Create
          </button>
        </form>
      </section>

      <section className="card">
        <div className="row-between">
          <h2>Fleet</h2>
          <button type="button" className="btn-secondary" onClick={() => void load()}>
            Refresh
          </button>
        </div>
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Address</th>
              <th>Status</th>
              <th>Labels</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {nodes.length === 0 ? (
              <tr>
                <td colSpan={5} className="muted">
                  No nodes yet
                </td>
              </tr>
            ) : (
              nodes.map((n) => (
                <tr key={n.id}>
                  <td>
                    <Link to={`/nodes/${n.id}`}>{n.name}</Link>
                  </td>
                  <td>
                    <code>
                      {n.address}:{n.grpc_port}
                    </code>
                  </td>
                  <td>
                    <span className={`status status-${n.status || 'unknown'}`}>
                      {n.status || 'unknown'}
                    </span>
                  </td>
                  <td>{(n.labels || []).join(', ') || '—'}</td>
                  <td className="actions">
                    <button type="button" onClick={() => void onProbe(n.id)}>
                      Probe
                    </button>
                    <button
                      type="button"
                      className="btn-danger"
                      onClick={() => void onDelete(n.id, n.name)}
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
