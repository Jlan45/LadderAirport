import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  Alert,
  Button,
  Checkbox,
  Drawer,
  Form,
  Input,
  InputNumber,
  MessagePlugin,
  Select,
  Tabs,
  Tag,
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
  upgradeNode,
  type InboundConfig,
  type Metrics,
  type NetworkInterface,
  type Node,
  type NodeInstallInfo,
  type Task,
} from '../api/client'
import {
  formatBytes,
  isAgentOutdated,
  runtimeLabel,
  runtimeTheme,
  statusLabel,
  statusTheme,
  taskKindLabel,
  taskStatusLabel,
} from '../lib/nodeDisplay'

type Props = {
  nodeId: string | null
  onClose: () => void
  onChanged: () => void
}

type MappingRow = { listen_port: number; public_port: number }

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
  const [activeTab, setActiveTab] = useState<string | number>('connection')
  const abortRef = useRef<AbortController | null>(null)
  const logEndRef = useRef<HTMLDivElement | null>(null)

  const [editAddress, setEditAddress] = useState('')
  const [editPort, setEditPort] = useState(50051)
  const [editPublic, setEditPublic] = useState('')
  const [editMappings, setEditMappings] = useState<MappingRow[]>([])
  const [editCA, setEditCA] = useState('')
  const [editTLSSkip, setEditTLSSkip] = useState(false)
  const [editEgress, setEditEgress] = useState('')
  const [ifaces, setIfaces] = useState<NetworkInterface[]>([])
  const [ifacesError, setIfacesError] = useState('')
  const [ifacesLoading, setIfacesLoading] = useState(false)
  const [installInfo, setInstallInfo] = useState<NodeInstallInfo | null>(null)
  const [copied, setCopied] = useState(false)
  const [copiedUpgrade, setCopiedUpgrade] = useState(false)

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
        setEditPublic(n.public_address || '')
        setEditMappings(
          (n.port_mappings ?? []).map((m) => ({
            listen_port: Number(m.listen_port) || 0,
            public_port: Number(m.public_port) || 0,
          })),
        )
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
      setEditMappings([])
      setActiveTab('connection')
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
      const mappings = editMappings
        .map((m) => ({
          listen_port: Number(m.listen_port) || 0,
          public_port: Number(m.public_port) || 0,
        }))
        .filter(
          (m) =>
            m.listen_port >= 1 &&
            m.listen_port <= 65535 &&
            m.public_port >= 1 &&
            m.public_port <= 65535,
        )
      const updated = await updateNode(id, {
        ...node,
        address: editAddress.trim(),
        grpc_port: editPort,
        public_address: editPublic.trim(),
        port_mappings: mappings,
        ca_cert_pem: editCA,
        tls_skip_verify: editTLSSkip,
        egress_interface: editEgress.trim(),
        status: editAddress.trim()
          ? node.status === 'pending'
            ? 'unknown'
            : node.status
          : 'pending',
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
    if (editEgress && !seen.has(editEgress)) {
      opts.push({ label: `${editEgress}（已保存，当前列表中不存在）`, value: editEgress })
    }
    return opts
  }

  async function onRemoteUpgrade() {
    if (!id) return
    setBusy(true)
    try {
      const version = installInfo?.recommended_agent_version || undefined
      const res = await upgradeNode(id, version ? { version } : {})
      if (!res.ok) {
        MessagePlugin.error(res.message || '远程升级失败')
        return
      }
      MessagePlugin.success(
        `已触发远程升级${res.version ? ` → ${res.version}` : ''}：${res.message || 'staged'}`,
      )
      window.setTimeout(() => {
        void load()
      }, 5000)
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '远程升级失败')
    } finally {
      setBusy(false)
    }
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

  async function copyUpgrade() {
    if (!installInfo?.upgrade_command) return
    try {
      await navigator.clipboard.writeText(installInfo.upgrade_command)
      setCopiedUpgrade(true)
      MessagePlugin.success('已复制升级命令')
      setTimeout(() => setCopiedUpgrade(false), 2000)
    } catch {
      MessagePlugin.error('复制失败，请手动选择命令')
    }
  }

  function updateMapping(idx: number, patch: Partial<MappingRow>) {
    setEditMappings((prev) => prev.map((row, i) => (i === idx ? { ...row, ...patch } : row)))
  }

  const attachedCount = attachedIds.size
  const mappingCount = editMappings.filter(
    (m) => m.listen_port >= 1 && m.public_port >= 1 && m.listen_port !== m.public_port,
  ).length

  return (
    <Drawer
      visible={open}
      size="720px"
      placement="right"
      header={null}
      footer={null}
      onClose={onClose}
      destroyOnClose
      closeOnOverlayClick
      className="la-node-drawer"
    >
      {!node ? (
        <div className="la-drawer-loading">加载中…</div>
      ) : (
        <div className="la-drawer-body">
          <header className="la-drawer-hero">
            <div className="la-drawer-hero-top">
              <div className="la-drawer-hero-title">
                <h2>{node.name}</h2>
                <div className="la-drawer-tags">
                  <Tag theme={statusTheme(node.status)} variant="light" size="small">
                    {statusLabel(node.status)}
                  </Tag>
                  {node.runtime_state ? (
                    <Tag theme={runtimeTheme(node.runtime_state)} variant="light" size="small">
                      {runtimeLabel(node.runtime_state)}
                    </Tag>
                  ) : null}
                  {installInfo &&
                  isAgentOutdated(node.agent_version, installInfo.recommended_agent_version) ? (
                    <Tag theme="warning" variant="light" size="small">
                      可升级
                      {installInfo.recommended_agent_version
                        ? ` → ${installInfo.recommended_agent_version}`
                        : ''}
                    </Tag>
                  ) : null}
                </div>
              </div>
              <Button variant="text" shape="square" onClick={onClose} aria-label="关闭">
                ✕
              </Button>
            </div>

            <div className="la-drawer-summary">
              <div className="la-summary-item">
                <span className="label">控制面</span>
                <code className="la-mono value">
                  {node.address || '（待填）'}:{node.grpc_port}
                </code>
              </div>
              <div className="la-summary-item">
                <span className="label">订阅入口</span>
                <code className="la-mono value">
                  {node.public_address || node.address || '—'}
                  {mappingCount > 0 ? ` · ${mappingCount} 条端口映射` : ''}
                </code>
              </div>
              <div className="la-summary-item">
                <span className="label">版本</span>
                <span className="value">
                  Agent <code className="la-mono">{node.agent_version || '—'}</code>
                  {node.singbox_version ? (
                    <>
                      {' '}
                      · sing-box <code className="la-mono">{node.singbox_version}</code>
                    </>
                  ) : null}
                </span>
              </div>
            </div>
          </header>

          <Tabs
            value={activeTab}
            onChange={setActiveTab}
            className="la-drawer-tabs"
            size="medium"
          >
            <Tabs.TabPanel value="connection" label="连接 / NAT">
              <div className="la-drawer-panel">
                <section className="la-panel-card">
                  <div className="la-panel-card-head">
                    <h3>Panel 控制面</h3>
                    <p>Panel 拨号用。NAT 时填映射后的公网/VPN 地址与<strong>外部</strong> gRPC 端口。</p>
                  </div>
                  <Form labelAlign="top" className="la-drawer-form">
                    <div className="la-form-row-2">
                      <Form.FormItem label="控制面地址">
                        <Input
                          value={editAddress}
                          onChange={(v) => setEditAddress(String(v))}
                          placeholder="公网 IP / DDNS / VPN 地址"
                          clearable
                        />
                      </Form.FormItem>
                      <Form.FormItem label="gRPC 端口（外部映射口）">
                        <InputNumber
                          theme="normal"
                          style={{ width: '100%' }}
                          value={editPort}
                          onChange={(v) => setEditPort(Number(v) || 50051)}
                        />
                      </Form.FormItem>
                    </div>
                  </Form>
                </section>

                <section className="la-panel-card">
                  <div className="la-panel-card-head">
                    <h3>订阅入口 / NAT</h3>
                    <p>
                      只影响订阅里的 <code>server</code> / 端口。入站 <code>port</code> 仍是 Agent
                      本机监听口。
                    </p>
                  </div>
                  <Form labelAlign="top" className="la-drawer-form">
                    <Form.FormItem label="公网地址 / NAT IP">
                      <Input
                        value={editPublic}
                        onChange={(v) => setEditPublic(String(v))}
                        placeholder="客户端入口；空则回退控制面地址"
                        clearable
                      />
                    </Form.FormItem>

                    <Form.FormItem label="端口映射（监听口 → 公网口）">
                      <div className="la-map-table">
                        <div className="la-map-head">
                          <span>Agent 监听</span>
                          <span />
                          <span>NAT 外端口</span>
                          <span />
                        </div>
                        {editMappings.length === 0 ? (
                          <div className="la-map-empty">无改写时留空；同端口映射无需填写。</div>
                        ) : (
                          editMappings.map((row, idx) => (
                            <div className="la-map-row" key={idx}>
                              <InputNumber
                                theme="normal"
                                min={1}
                                max={65535}
                                value={row.listen_port || undefined}
                                placeholder="8443"
                                onChange={(v) => updateMapping(idx, { listen_port: Number(v) || 0 })}
                              />
                              <span className="la-map-arrow">→</span>
                              <InputNumber
                                theme="normal"
                                min={1}
                                max={65535}
                                value={row.public_port || undefined}
                                placeholder="443"
                                onChange={(v) => updateMapping(idx, { public_port: Number(v) || 0 })}
                              />
                              <Button
                                size="small"
                                variant="text"
                                theme="danger"
                                onClick={() =>
                                  setEditMappings(editMappings.filter((_, i) => i !== idx))
                                }
                              >
                                删除
                              </Button>
                            </div>
                          ))
                        )}
                        <Button
                          size="small"
                          variant="outline"
                          onClick={() =>
                            setEditMappings([...editMappings, { listen_port: 0, public_port: 0 }])
                          }
                        >
                          添加映射
                        </Button>
                      </div>
                    </Form.FormItem>
                  </Form>
                </section>

                <section className="la-panel-card">
                  <div className="la-panel-card-head">
                    <h3>TLS 与出口</h3>
                    <p>粘贴节点 <code>ca.crt</code>；出口网卡修改后需下发配置生效。</p>
                  </div>
                  <Form labelAlign="top" className="la-drawer-form">
                    <Form.FormItem label="CA 证书">
                      <Textarea
                        value={editCA}
                        onChange={(v) => setEditCA(String(v))}
                        placeholder="-----BEGIN CERTIFICATE----- ..."
                        autosize={{ minRows: 3, maxRows: 8 }}
                        style={{ fontFamily: 'var(--la-mono)', fontSize: 12 }}
                      />
                    </Form.FormItem>
                    <Form.FormItem>
                      <Checkbox checked={editTLSSkip} onChange={(c) => setEditTLSSkip(Boolean(c))}>
                        跳过 TLS 证书验证（仅 lab）
                      </Checkbox>
                    </Form.FormItem>
                    <Form.FormItem label="出口网卡">
                      <div className="la-egress-row">
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
                        <Button
                          size="small"
                          variant="outline"
                          loading={ifacesLoading}
                          onClick={() => void loadInterfaces()}
                        >
                          刷新
                        </Button>
                      </div>
                      {ifacesError ? (
                        <Alert
                          theme="warning"
                          message={`无法从 Agent 拉取网卡：${ifacesError}`}
                          style={{ marginTop: 8 }}
                        />
                      ) : null}
                    </Form.FormItem>
                  </Form>
                </section>

                <div className="la-drawer-actions">
                  <Button theme="primary" loading={busy} onClick={() => void onSaveConnection()}>
                    保存连接设置
                  </Button>
                  <Button variant="outline" onClick={() => void onShowInstall()}>
                    {installInfo ? '刷新安装命令' : '显示安装命令'}
                  </Button>
                </div>

                {installInfo ? (
                  <section className="la-panel-card la-panel-card-soft">
                    <div className="la-panel-card-head row">
                      <h3>一键安装</h3>
                      <Button size="small" variant="outline" onClick={() => void copyInstall()}>
                        {copied ? '已复制' : '复制'}
                      </Button>
                    </div>
                    <pre className="la-pre">{installInfo.install_command}</pre>
                    {installInfo.upgrade_command ? (
                      <>
                        <div className="la-panel-card-head row" style={{ marginTop: 16 }}>
                          <h3>
                            升级
                            {installInfo.recommended_agent_version
                              ? ` → ${installInfo.recommended_agent_version}`
                              : ''}
                          </h3>
                          <Button size="small" variant="outline" onClick={() => void copyUpgrade()}>
                            {copiedUpgrade ? '已复制' : '复制'}
                          </Button>
                        </div>
                        <p className="la-page-desc" style={{ marginTop: 0 }}>
                          节点上以 root 执行；保留 Token / TLS / 配置。
                        </p>
                        <pre className="la-pre">{installInfo.upgrade_command}</pre>
                      </>
                    ) : null}
                  </section>
                ) : null}
              </div>
            </Tabs.TabPanel>

            <Tabs.TabPanel value="inbounds" label={`入站${attachedCount ? ` (${attachedCount})` : ''}`}>
              <div className="la-drawer-panel">
                <section className="la-panel-card">
                  <div className="la-panel-card-head">
                    <h3>关联入站</h3>
                    <p>
                      勾选后保存会<strong>自动下发并启动核心</strong>。
                    </p>
                  </div>
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
                    <div className="la-inbound-list">
                      {allInbounds.map((inb) => {
                        const port = Number(inb.params?.port) || 0
                        const checked = attachedIds.has(inb.id)
                        return (
                          <label
                            key={inb.id}
                            className={`la-inbound-item${checked ? ' is-checked' : ''}${
                              !inb.enabled ? ' is-disabled' : ''
                            }`}
                          >
                            <Checkbox
                              checked={checked}
                              onChange={() => toggleInbound(inb.id)}
                            />
                            <div className="la-inbound-meta">
                              <div className="name">
                                {inb.name}
                                {!inb.enabled ? (
                                  <Tag size="small" variant="light" style={{ marginLeft: 8 }}>
                                    已禁用
                                  </Tag>
                                ) : null}
                              </div>
                              <div className="sub">
                                <code className="la-mono">{inb.protocol}</code>
                                {port ? (
                                  <>
                                    {' '}
                                    · 监听 <code className="la-mono">{port}</code>
                                  </>
                                ) : null}
                              </div>
                            </div>
                          </label>
                        )
                      })}
                    </div>
                  )}
                  <div className="la-drawer-actions" style={{ marginTop: 16 }}>
                    <Button theme="primary" loading={busy} onClick={() => void onSaveInbounds()}>
                      保存并下发
                    </Button>
                  </div>
                </section>

                {task ? (
                  <section className="la-panel-card">
                    <div className="la-panel-card-head">
                      <h3>最近任务</h3>
                    </div>
                    <div className="la-task-box">
                      <strong>
                        {task.id.slice(0, 8)}… — {taskKindLabel(task.type)} —{' '}
                        {taskStatusLabel(task.status)}
                      </strong>
                      <ul>
                        {(task.results || []).map((r) => (
                          <li key={r.node_id}>
                            {r.ok ? '✓' : '✗'} {r.message}
                          </li>
                        ))}
                      </ul>
                    </div>
                  </section>
                ) : null}
              </div>
            </Tabs.TabPanel>

            <Tabs.TabPanel value="ops" label="运维">
              <div className="la-drawer-panel">
                <section className="la-panel-card">
                  <div className="la-panel-card-head">
                    <h3>生命周期</h3>
                    <p>启停核心、预览下发配置、刷新监控。</p>
                  </div>
                  <div className="la-ops-grid">
                    <Button theme="primary" loading={busy} onClick={() => void runAction('start')}>
                      启动服务
                    </Button>
                    <Button
                      theme="danger"
                      variant="outline"
                      loading={busy}
                      onClick={() => void runAction('stop')}
                    >
                      停止服务
                    </Button>
                    <Button variant="outline" loading={busy} onClick={() => void onPreview()}>
                      预览配置
                    </Button>
                    <Button variant="outline" loading={busy} onClick={() => void onMetrics()}>
                      刷新监控
                    </Button>
                    <Button
                      variant="outline"
                      loading={busy}
                      onClick={() => void runAction('apply')}
                    >
                      重新下发
                    </Button>
                    <Button
                      theme="warning"
                      variant="outline"
                      loading={busy}
                      onClick={() => void onRemoteUpgrade()}
                    >
                      远程升级
                    </Button>
                  </div>
                  {task ? (
                    <div className="la-task-box" style={{ marginTop: 12 }}>
                      <strong>
                        {task.id.slice(0, 8)}… — {taskKindLabel(task.type)} —{' '}
                        {taskStatusLabel(task.status)}
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
                </section>

                {metrics ? (
                  <section className="la-panel-card">
                    <div className="la-panel-card-head">
                      <h3>监控指标</h3>
                    </div>
                    <div className="la-metrics-grid">
                      <div className="item">
                        <span className="label">连接</span>
                        <span className="value">{metrics.connections}</span>
                      </div>
                      <div className="item">
                        <span className="label">上行</span>
                        <span className="value">{formatBytes(metrics.uplink_bytes)}</span>
                      </div>
                      <div className="item">
                        <span className="label">下行</span>
                        <span className="value">{formatBytes(metrics.downlink_bytes)}</span>
                      </div>
                      <div className="item">
                        <span className="label">CPU</span>
                        <span className="value">
                          {metrics.cpu_percent?.toFixed?.(1) ?? metrics.cpu_percent}%
                        </span>
                      </div>
                      <div className="item">
                        <span className="label">内存</span>
                        <span className="value">{formatBytes(metrics.memory_rss_bytes)}</span>
                      </div>
                    </div>
                  </section>
                ) : null}

                {preview ? (
                  <section className="la-panel-card">
                    <div className="la-panel-card-head">
                      <h3>配置预览</h3>
                    </div>
                    <pre className="la-pre">{preview}</pre>
                  </section>
                ) : null}

                <section className="la-panel-card">
                  <div className="la-panel-card-head row">
                    <div>
                      <h3>节点日志</h3>
                      <p style={{ margin: '4px 0 0' }}>实时流式拉取 Agent 日志。</p>
                    </div>
                    {!streaming ? (
                      <Button size="small" theme="primary" onClick={() => void startLogs()}>
                        开启
                      </Button>
                    ) : (
                      <Button size="small" variant="outline" onClick={stopLogs}>
                        停止
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
                </section>
              </div>
            </Tabs.TabPanel>
          </Tabs>
        </div>
      )}
    </Drawer>
  )
}
