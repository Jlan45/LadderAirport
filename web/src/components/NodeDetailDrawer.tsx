import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  Alert,
  Button,
  Checkbox,
  Divider,
  Drawer,
  Form,
  Input,
  InputNumber,
  MessagePlugin,
  Select,
  Space,
  Textarea,
} from 'tdesign-react'
import {
  applyNode,
  getNodeInstallCommand,
  getNodeMetrics,
  listInbounds,
  listNodeInbounds,
  listNodeInterfaces,
  listNodes,
  previewNodeConfig,
  setNodeInbounds,
  startNode,
  stopNode,
  streamNodeLogs,
  updateNode,
  type InboundConfig,
  type Metrics,
  type NetworkInterface,
  type Node,
  type NodeInstallInfo,
  type Task,
} from '../api/client'
import { formatBytes, taskKindLabel, taskStatusLabel } from '../lib/nodeDisplay'

type Props = {
  nodeId: string | null
  onClose: () => void
  onChanged: () => void
}

export default function NodeDetailDrawer({ nodeId, onClose, onChanged }: Props) {
  const open = !!nodeId
  const id = nodeId ?? ''

  const [node, setNode] = useState<Node | null>(null)
  const [allInbounds, setAllInbounds] = useState<InboundConfig[]>([])
  const [attachedIds, setAttachedIds] = useState<Set<string>>(new Set())
  const [preview, setPreview] = useState('')
  const [metrics, setMetrics] = useState<Metrics | null>(null)
  const [task, setTask] = useState<Task | null>(null)
  const [logs, setLogs] = useState<string[]>([])
  const [streaming, setStreaming] = useState(false)
  const [busy, setBusy] = useState(false)
  const abortRef = useRef<AbortController | null>(null)
  const logEndRef = useRef<HTMLDivElement | null>(null)

  const [editAddress, setEditAddress] = useState('')
  const [editPort, setEditPort] = useState(50051)
  const [editCA, setEditCA] = useState('')
  const [editTLSSkip, setEditTLSSkip] = useState(false)
  const [editEgress, setEditEgress] = useState('')
  const [ifaces, setIfaces] = useState<NetworkInterface[]>([])
  const [ifacesError, setIfacesError] = useState('')
  const [ifacesLoading, setIfacesLoading] = useState(false)
  const [installInfo, setInstallInfo] = useState<NodeInstallInfo | null>(null)
  const [copied, setCopied] = useState(false)

  const loadInterfaces = useCallback(async () => {
    if (!id) return
    setIfacesLoading(true)
    setIfacesError('')
    try {
      const res = await listNodeInterfaces(id)
      setIfaces(res.interfaces ?? [])
    } catch (err) {
      setIfaces([])
      setIfacesError(err instanceof Error ? err.message : '无法拉取网卡列表')
    } finally {
      setIfacesLoading(false)
    }
  }, [id])

  const load = useCallback(async () => {
    if (!id) return
    try {
      const [nodes, all, attached] = await Promise.all([
        listNodes(),
        listInbounds(),
        listNodeInbounds(id),
      ])
      const n = (nodes ?? []).find((x) => x.id === id) ?? null
      setNode(n)
      if (n) {
        setEditAddress(n.address || '')
        setEditPort(n.grpc_port || 50051)
        setEditCA(n.ca_cert_pem || '')
        setEditTLSSkip(!!n.tls_skip_verify)
        setEditEgress(n.egress_interface || '')
      }
      setAllInbounds(all ?? [])
      setAttachedIds(new Set((attached ?? []).map((a) => a.id)))
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '加载失败')
    }
  }, [id])

  useEffect(() => {
    if (!open) {
      abortRef.current?.abort()
      setStreaming(false)
      setLogs([])
      setPreview('')
      setMetrics(null)
      setTask(null)
      setInstallInfo(null)
      setNode(null)
      setIfaces([])
      setIfacesError('')
      setEditEgress('')
      return
    }
    void load()
    void loadInterfaces()
  }, [open, load, loadInterfaces])

  useEffect(() => {
    return () => {
      abortRef.current?.abort()
    }
  }, [])

  useEffect(() => {
    logEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [logs])

  function toggleInbound(inbId: string) {
    setAttachedIds((prev) => {
      const next = new Set(prev)
      if (next.has(inbId)) next.delete(inbId)
      else next.add(inbId)
      return next
    })
  }

  async function onSaveInbounds() {
    if (!id) return
    setBusy(true)
    setTask(null)
    try {
      const res = await setNodeInbounds(id, Array.from(attachedIds))
      const base = res.deploy_message || (res.deployed ? '已关联并下发配置' : '关联已保存')
      if (res.apply_task) setTask(res.apply_task)
      else if (res.start_task) setTask(res.start_task)
      if (!res.deployed && res.apply_task?.status === 'failed') {
        MessagePlugin.error(base)
      } else {
        MessagePlugin.success(base)
      }
      await load()
      onChanged()
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  async function onPreview() {
    if (!id) return
    setBusy(true)
    try {
      const cfg = await previewNodeConfig(id)
      setPreview(JSON.stringify(cfg, null, 2))
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '配置预览生成失败')
    } finally {
      setBusy(false)
    }
  }

  async function runAction(kind: 'apply' | 'start' | 'stop') {
    if (!id) return
    setBusy(true)
    try {
      const fn = kind === 'apply' ? applyNode : kind === 'start' ? startNode : stopNode
      const t = await fn(id)
      setTask(t)
      MessagePlugin.info(`${taskKindLabel(kind)}指令下发: ${taskStatusLabel(t.status)}`)
      await load()
      onChanged()
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : `${taskKindLabel(kind)}失败`)
    } finally {
      setBusy(false)
    }
  }

  async function onMetrics() {
    if (!id) return
    try {
      const m = await getNodeMetrics(id)
      setMetrics(m)
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '获取监控指标失败')
    }
  }

  async function startLogs() {
    if (!id || streaming) return
    setLogs([])
    const ac = new AbortController()
    abortRef.current = ac
    setStreaming(true)
    try {
      await streamNodeLogs(id, {
        tail: 100,
        signal: ac.signal,
        onLine: (line) => {
          const ts = line.ts ? new Date(line.ts).toISOString() : new Date().toISOString()
          setLogs((prev) => [...prev.slice(-499), `[${ts}] ${line.level || 'info'} ${line.message}`])
        },
      })
    } catch (err) {
      if ((err as Error).name !== 'AbortError') {
        MessagePlugin.error(err instanceof Error ? err.message : '日志流连接失败')
      }
    } finally {
      setStreaming(false)
      abortRef.current = null
    }
  }

  function stopLogs() {
    abortRef.current?.abort()
  }

  async function onSaveConnection() {
    if (!id || !node) return
    setBusy(true)
    try {
      const updated = await updateNode(id, {
        ...node,
        address: editAddress.trim(),
        grpc_port: editPort,
        ca_cert_pem: editCA,
        tls_skip_verify: editTLSSkip,
        egress_interface: editEgress.trim(),
        status: editAddress.trim() ? (node.status === 'pending' ? 'unknown' : node.status) : 'pending',
      })
      setNode(updated)
      MessagePlugin.success('连接设置已保存（出口网卡需下发配置后生效）')
      await load()
      onChanged()
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  function ifaceLabel(iface: NetworkInterface): string {
    const flags: string[] = []
    if (iface.up) flags.push('up')
    else flags.push('down')
    if (iface.loopback) flags.push('loopback')
    const addr = (iface.addresses && iface.addresses.length > 0 ? iface.addresses[0] : '') || ''
    const flagStr = flags.length ? ` (${flags.join(', ')})` : ''
    return addr ? `${iface.name}${flagStr} · ${addr}` : `${iface.name}${flagStr}`
  }

  function egressOptions() {
    const opts: { label: string; value: string }[] = [
      { label: '系统默认（不绑定网卡）', value: '' },
    ]
    const seen = new Set<string>([''])
    for (const iface of ifaces) {
      if (!iface.name || seen.has(iface.name)) continue
      seen.add(iface.name)
      opts.push({ label: ifaceLabel(iface), value: iface.name })
    }
    // Keep a saved value that is no longer in the live list.
    if (editEgress && !seen.has(editEgress)) {
      opts.push({ label: `${editEgress}（已保存，当前列表中不存在）`, value: editEgress })
    }
    return opts
  }

  async function onShowInstall() {
    if (!id) return
    setCopied(false)
    try {
      const info = await getNodeInstallCommand(id, { tls: !editTLSSkip })
      setInstallInfo(info)
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '获取安装命令失败')
    }
  }

  async function copyInstall() {
    if (!installInfo?.install_command) return
    try {
      await navigator.clipboard.writeText(installInfo.install_command)
      setCopied(true)
      MessagePlugin.success('已复制')
      setTimeout(() => setCopied(false), 2000)
    } catch {
      MessagePlugin.error('复制失败，请手动选择命令')
    }
  }

  return (
    <Drawer
      visible={open}
      size="560px"
      header={node ? node.name : '节点详情'}
      onClose={onClose}
      footer={null}
      destroyOnClose
    >
      {node ? (
        <p className="la-page-desc" style={{ marginTop: -4 }}>
          <code className="la-mono">
            {node.address || '（待填）'}:{node.grpc_port}
          </code>{' '}
          · {taskStatusLabel(node.status || 'unknown')}
        </p>
      ) : (
        <p className="la-page-desc">加载中…</p>
      )}

      <div className="la-drawer-section">
        <h3>连接与 TLS</h3>
        <p className="la-page-desc" style={{ marginTop: 0 }}>
          装机后填写地址，并将节点上的 <code>ca.crt</code> 粘贴到下方（TLS 模式）。
        </p>
        <Form labelAlign="top">
          <Form.FormItem label="节点地址">
            <Input
              value={editAddress}
              onChange={(v) => setEditAddress(String(v))}
              placeholder="对 Panel 可达的 IP 或域名"
              clearable
            />
          </Form.FormItem>
          <Form.FormItem label="gRPC 端口">
            <InputNumber
              theme="normal"
              style={{ width: '100%' }}
              value={editPort}
              onChange={(v) => setEditPort(Number(v) || 50051)}
            />
          </Form.FormItem>
          <Form.FormItem label="CA 证书 (ca_cert_pem)">
            <Textarea
              value={editCA}
              onChange={(v) => setEditCA(String(v))}
              placeholder="-----BEGIN CERTIFICATE----- ..."
              autosize={{ minRows: 4, maxRows: 10 }}
              style={{ fontFamily: 'var(--la-mono)', fontSize: 12 }}
            />
          </Form.FormItem>
          <Form.FormItem>
            <Checkbox checked={editTLSSkip} onChange={(c) => setEditTLSSkip(Boolean(c))}>
              跳过 TLS 证书验证（仅 lab）
            </Checkbox>
          </Form.FormItem>
          <Form.FormItem label="出口网卡">
            <Space direction="vertical" style={{ width: '100%' }}>
              <Select
                value={editEgress}
                onChange={(v) => setEditEgress(String(v ?? ''))}
                options={egressOptions()}
                placeholder="系统默认"
                clearable
                filterable
                style={{ width: '100%' }}
                empty={ifacesLoading ? '加载网卡中…' : '暂无网卡数据'}
              />
              <Space>
                <Button size="small" variant="outline" loading={ifacesLoading} onClick={() => void loadInterfaces()}>
                  刷新网卡
                </Button>
              </Space>
              <p className="la-page-desc" style={{ margin: 0 }}>
                留空使用系统默认路由。修改后需「下发配置」才写入节点。Linux 绑定网卡通常需要 root / CAP_NET_RAW。
              </p>
              {ifacesError ? (
                <Alert theme="warning" message={`无法从 Agent 拉取网卡：${ifacesError}`} />
              ) : null}
            </Space>
          </Form.FormItem>
          <Space>
            <Button theme="primary" loading={busy} onClick={() => void onSaveConnection()}>
              保存连接设置
            </Button>
            <Button variant="outline" onClick={() => void onShowInstall()}>
              显示安装命令
            </Button>
          </Space>
        </Form>
        {installInfo ? (
          <div style={{ marginTop: 16 }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 8 }}>
              <strong>一键安装</strong>
              <Button size="small" variant="outline" onClick={() => void copyInstall()}>
                {copied ? '已复制' : '复制'}
              </Button>
            </div>
            <pre className="la-pre">{installInfo.install_command}</pre>
          </div>
        ) : null}
      </div>

      <Divider />

      <div className="la-drawer-section">
        <h3>入站配置</h3>
        <p className="la-page-desc" style={{ marginTop: 0 }}>
          勾选后保存会<strong>自动下发到节点并启动核心</strong>。
        </p>
        {allInbounds.length === 0 ? (
          <Alert
            theme="info"
            message={
              <>
                无入站配置。请先在 <Link to="/inbounds">入站</Link> 创建。
              </>
            }
          />
        ) : (
          <div className="la-check-list">
            {allInbounds.map((inb) => (
              <Checkbox
                key={inb.id}
                checked={attachedIds.has(inb.id)}
                onChange={() => toggleInbound(inb.id)}
              >
                {inb.name} <code className="la-mono">{inb.protocol}</code>
                {!inb.enabled ? ' (已禁用)' : ''}
              </Checkbox>
            ))}
          </div>
        )}
        <div style={{ marginTop: 12 }}>
          <Button theme="primary" loading={busy} onClick={() => void onSaveInbounds()}>
            保存并下发
          </Button>
        </div>
      </div>

      <Divider />

      <div className="la-drawer-section">
        <h3>管理操作</h3>
        <Space breakLine>
          <Button variant="outline" loading={busy} onClick={() => void onPreview()}>
            预览配置
          </Button>
          <Button theme="primary" loading={busy} onClick={() => void runAction('start')}>
            启动服务
          </Button>
          <Button theme="danger" variant="outline" loading={busy} onClick={() => void runAction('stop')}>
            停止服务
          </Button>
          <Button variant="outline" loading={busy} onClick={() => void onMetrics()}>
            刷新监控指标
          </Button>
        </Space>
        {task ? (
          <div className="la-task-box">
            <strong>
              任务 {task.id.slice(0, 8)}… — {taskKindLabel(task.type)} — {taskStatusLabel(task.status)}
            </strong>
            <ul>
              {(task.results || []).map((r) => (
                <li key={r.node_id}>
                  {r.ok ? '✓' : '✗'} {r.message}
                </li>
              ))}
            </ul>
          </div>
        ) : null}
      </div>

      {metrics ? (
        <>
          <Divider />
          <div className="la-drawer-section">
            <h3>监控指标</h3>
            <table className="la-kv">
              <tbody>
                <tr>
                  <th>当前连接数</th>
                  <td>{metrics.connections}</td>
                </tr>
                <tr>
                  <th>上行流量</th>
                  <td>{formatBytes(metrics.uplink_bytes)}</td>
                </tr>
                <tr>
                  <th>下行流量</th>
                  <td>{formatBytes(metrics.downlink_bytes)}</td>
                </tr>
                <tr>
                  <th>CPU</th>
                  <td>{metrics.cpu_percent?.toFixed?.(1) ?? metrics.cpu_percent}%</td>
                </tr>
                <tr>
                  <th>内存 (RSS)</th>
                  <td>{formatBytes(metrics.memory_rss_bytes)}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </>
      ) : null}

      {preview ? (
        <>
          <Divider />
          <div className="la-drawer-section">
            <h3>配置文件预览</h3>
            <pre className="la-pre">{preview}</pre>
          </div>
        </>
      ) : null}

      <Divider />

      <div className="la-drawer-section">
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <h3 style={{ margin: 0 }}>节点日志</h3>
          {!streaming ? (
            <Button size="small" theme="primary" onClick={() => void startLogs()}>
              开启实时日志
            </Button>
          ) : (
            <Button size="small" variant="outline" onClick={stopLogs}>
              停止实时日志
            </Button>
          )}
        </div>
        <div className="la-log-viewer">
          {logs.length === 0 ? (
            <div style={{ textAlign: 'center', padding: '24px 0', opacity: 0.7 }}>
              {streaming ? '正在等待日志输出…' : '未开启实时日志'}
            </div>
          ) : (
            logs.map((line, i) => (
              <div key={i} className="la-log-line">
                {line}
              </div>
            ))
          )}
          <div ref={logEndRef} />
        </div>
      </div>
    </Drawer>
  )
}
