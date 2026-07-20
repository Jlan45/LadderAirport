import { useCallback, useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  Button,
  Card,
  Checkbox,
  DialogPlugin,
  Input,
  MessagePlugin,
  Radio,
  Space,
  Switch,
  Table,
  Tag,
  type PrimaryTableCol,
} from 'tdesign-react'
import {
  ApiError,
  applyNode,
  batchApply,
  batchStart,
  batchStop,
  deleteNode,
  fleetOverview,
  fleetRefresh,
  getMeta,
  getNodeInstallCommand,
  upgradeNode,
  probeNode,
  startNode,
  stopNode,
  type FleetOverview,
  type MetaInfo,
  type Node,
  type NodeInstallInfo,
  type Task,
} from '../api/client'
import AddNodeModal from '../components/AddNodeModal'
import NodeDetailDrawer from '../components/NodeDetailDrawer'
import StatsBar from '../components/StatsBar'
import {
  formatBytes,
  formatTime,
  isAgentOutdated,
  isOnlineStatus,
  runtimeLabel,
  runtimeTheme,
  statusLabel,
  statusTheme,
} from '../lib/nodeDisplay'

type ViewMode = 'cards' | 'table'

export default function Fleet() {
  const [searchParams, setSearchParams] = useSearchParams()
  const detailId = searchParams.get('node')

  const [ov, setOv] = useState<FleetOverview | null>(null)
  const [busy, setBusy] = useState(false)
  const [auto, setAuto] = useState(true)
  const [view, setView] = useState<ViewMode>(() => {
    const v = localStorage.getItem('fleet-view')
    return v === 'table' ? 'table' : 'cards'
  })
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [labelFilter, setLabelFilter] = useState('')
  const [lastTask, setLastTask] = useState<Task | null>(null)
  const [actionId, setActionId] = useState<string | null>(null)
  const [addOpen, setAddOpen] = useState(false)
  const [installBanner, setInstallBanner] = useState<NodeInstallInfo | null>(null)
  const [upgradeBanner, setUpgradeBanner] = useState<{
    node: Node
    command: string
    recommended?: string
  } | null>(null)
  const [meta, setMeta] = useState<MetaInfo | null>(null)
  const [copied, setCopied] = useState(false)

  const loadCached = useCallback(async () => {
    try {
      const data = await fleetOverview()
      setOv(data)
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '加载失败')
    }
  }, [])

  const refreshLive = useCallback(async () => {
    setBusy(true)
    try {
      const data = await fleetRefresh()
      setOv(data)
      MessagePlugin.success(
        `已刷新 ${data.total_nodes} 个节点 · ${formatTime(data.refreshed_at)}`,
      )
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '刷新失败')
    } finally {
      setBusy(false)
    }
  }, [])

  useEffect(() => {
    void loadCached()
  }, [loadCached])

  useEffect(() => {
    void getMeta()
      .then(setMeta)
      .catch(() => {
        /* offline / no release info is fine */
      })
  }, [])

  useEffect(() => {
    if (!auto) return
    const t = window.setInterval(() => {
      void refreshLive()
    }, 15000)
    return () => window.clearInterval(t)
  }, [auto, refreshLive])

  function setViewMode(mode: ViewMode) {
    setView(mode)
    localStorage.setItem('fleet-view', mode)
  }

  const allNodes = ov?.nodes ?? []

  const nodes = useMemo(() => {
    const q = labelFilter.trim().toLowerCase()
    if (!q) return allNodes
    return allNodes.filter((n) => (n.labels ?? []).some((l) => l.toLowerCase().includes(q)))
  }, [allNodes, labelFilter])

  function openDetail(id: string) {
    setSearchParams({ node: id }, { replace: false })
  }

  function closeDetail() {
    setSearchParams({}, { replace: true })
  }

  function toggle(id: string) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  function toggleAllVisible() {
    if (nodes.length === 0) return
    const allSelected = nodes.every((n) => selected.has(n.id))
    setSelected((prev) => {
      const next = new Set(prev)
      if (allSelected) {
        for (const n of nodes) next.delete(n.id)
      } else {
        for (const n of nodes) next.add(n.id)
      }
      return next
    })
  }

  async function runBatch(kind: 'apply' | 'start' | 'stop') {
    const ids = Array.from(selected)
    if (ids.length === 0) {
      MessagePlugin.warning('请先勾选节点')
      return
    }
    setBusy(true)
    try {
      const fn = kind === 'apply' ? batchApply : kind === 'start' ? batchStart : batchStop
      const task = await fn({ node_ids: ids })
      setLastTask(task)
      const ok = task.results?.filter((r) => r.ok).length ?? 0
      const total = task.results?.length ?? 0
      MessagePlugin.success(
        `批量${kind === 'apply' ? '下发配置' : kind === 'start' ? '启动' : '停止'}完成：${ok}/${total} 成功（${task.status}）`,
      )
      await refreshLive()
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '批量操作失败')
    } finally {
      setBusy(false)
    }
  }

  async function runOne(id: string, kind: 'apply' | 'start' | 'stop') {
    setActionId(id)
    try {
      const task =
        kind === 'apply'
          ? await applyNode(id)
          : kind === 'start'
            ? await startNode(id)
            : await stopNode(id)
      setLastTask(task)
      const ok = task.results?.[0]?.ok
      const text = `${kind === 'apply' ? '下发' : kind === 'start' ? '启动' : '停止'} ${ok ? '成功' : '失败'}: ${task.results?.[0]?.message || task.status}`
      if (ok) MessagePlugin.success(text)
      else MessagePlugin.error(text)
      await refreshLive()
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '操作失败')
    } finally {
      setActionId(null)
    }
  }

  async function onProbe(id: string) {
    try {
      const r = await probeNode(id)
      MessagePlugin.success(
        `探测成功: Agent = ${r.agent_version}, sing-box = ${r.singbox_version}, 状态 = ${statusLabel(r.node.status)}`,
      )
      await loadCached()
    } catch (err) {
      if (err instanceof ApiError) MessagePlugin.error(err.message)
      else MessagePlugin.error(err instanceof Error ? err.message : '探测失败')
      await loadCached()
    }
  }

  function onDelete(id: string, name: string) {
    const dialog = DialogPlugin.confirm({
      header: '删除节点',
      body: `确定要删除节点「${name}」吗？`,
      theme: 'danger',
      confirmBtn: { content: '删除', theme: 'danger' },
      onConfirm: async () => {
        try {
          await deleteNode(id)
          if (detailId === id) closeDetail()
          if (installBanner?.node.id === id) setInstallBanner(null)
          setSelected((prev) => {
            const next = new Set(prev)
            next.delete(id)
            return next
          })
          await loadCached()
          MessagePlugin.success(`已删除节点「${name}」`)
          dialog.destroy()
        } catch (err) {
          MessagePlugin.error(err instanceof Error ? err.message : '删除失败')
        }
      },
    })
  }

  async function showInstall(id: string) {
    setCopied(false)
    setUpgradeBanner(null)
    try {
      const info = await getNodeInstallCommand(id)
      setInstallBanner(info)
      MessagePlugin.success(`已生成节点「${info.node.name}」的安装命令`)
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '获取安装命令失败')
    }
  }

  async function showUpgrade(id: string) {
    setCopied(false)
    setInstallBanner(null)
    const version = meta?.recommended_agent_version || 'latest'
    try {
      const res = await upgradeNode(id, { version })
      if (!res.ok) {
        MessagePlugin.error(res.message || '远程升级失败')
        // Fall back to copyable command for older agents / missing helper.
        try {
          const info = await getNodeInstallCommand(id, { version })
          const cmd = info.upgrade_command || meta?.agent_upgrade_command || ''
          if (cmd) {
            setUpgradeBanner({
              node: info.node,
              command: cmd,
              recommended: info.recommended_agent_version || version,
            })
            MessagePlugin.warning('已回退为手动升级命令（节点可能尚未安装升级助手）')
          }
        } catch {
          /* ignore */
        }
        return
      }
      MessagePlugin.success(
        res.message
          ? `已触发远程升级 → ${res.version || version}：${res.message}`
          : `已触发远程升级 → ${res.version || version}`,
      )
      // Probe a few times after helper restarts the agent.
      window.setTimeout(() => {
        void onProbe(id)
      }, 4000)
      window.setTimeout(() => {
        void onProbe(id)
      }, 10000)
    } catch (err) {
      const msg = err instanceof Error ? err.message : '远程升级失败'
      MessagePlugin.error(msg)
      try {
        const info = await getNodeInstallCommand(id, { version })
        const cmd = info.upgrade_command || meta?.agent_upgrade_command || ''
        if (cmd) {
          const n = (allNodes ?? []).find((x) => x.id === id)
          setUpgradeBanner({
            node: info.node || n!,
            command: cmd,
            recommended: info.recommended_agent_version || version,
          })
          MessagePlugin.warning('远程升级不可用，已生成手动升级命令')
        }
      } catch {
        /* ignore */
      }
    }
  }

  async function copyInstallBanner() {
    if (!installBanner?.install_command) return
    try {
      await navigator.clipboard.writeText(installBanner.install_command)
      setCopied(true)
      MessagePlugin.success('已复制')
      setTimeout(() => setCopied(false), 2000)
    } catch {
      MessagePlugin.error('复制失败，请手动选择命令')
    }
  }

  async function copyUpgradeBanner() {
    if (!upgradeBanner?.command) return
    try {
      await navigator.clipboard.writeText(upgradeBanner.command)
      setCopied(true)
      MessagePlugin.success('已复制升级命令')
      setTimeout(() => setCopied(false), 2000)
    } catch {
      MessagePlugin.error('复制失败，请手动选择命令')
    }
  }

  const recommended = meta?.recommended_agent_version || ''
  const outdatedNodes = useMemo(() => {
    if (!recommended) return [] as Node[]
    return allNodes.filter((n) => isAgentOutdated(n.agent_version, recommended))
  }, [allNodes, recommended])

  const visibleAllSelected = nodes.length > 0 && nodes.every((n) => selected.has(n.id))

  const columns: PrimaryTableCol<Node>[] = [
    {
      colKey: 'row-select',
      type: 'multiple',
      width: 46,
    },
    {
      colKey: 'name',
      title: '名称',
      cell: ({ row }) => (
        <Button theme="primary" variant="text" onClick={() => openDetail(row.id)}>
          {row.name}
        </Button>
      ),
    },
    {
      colKey: 'address',
      title: '控制面地址',
      cell: ({ row }) => (
        <div>
          <code className="la-mono">
            {row.address || '（待填）'}:{row.grpc_port}
          </code>
          {row.public_address && row.public_address !== row.address ? (
            <div className="la-page-desc" style={{ margin: '2px 0 0' }}>
              订阅 <code className="la-mono">{row.public_address}</code>
            </div>
          ) : null}
          {row.port_mappings && row.port_mappings.length > 0 ? (
            <div className="la-page-desc" style={{ margin: '2px 0 0' }}>
              遗留端口映射 {row.port_mappings.length}
            </div>
          ) : null}
        </div>
      ),
    },
    {
      colKey: 'status',
      title: '连通',
      width: 100,
      cell: ({ row }) => (
        <Tag theme={statusTheme(row.status)} variant="light">
          {statusLabel(row.status)}
        </Tag>
      ),
    },
    {
      colKey: 'runtime_state',
      title: '核心',
      width: 100,
      cell: ({ row }) => (
        <Tag theme={runtimeTheme(row.runtime_state)} variant="light">
          {runtimeLabel(row.runtime_state)}
        </Tag>
      ),
    },
    {
      colKey: 'inbound_count',
      title: '入站',
      width: 72,
      cell: ({ row }) => row.inbound_count ?? 0,
    },
    {
      colKey: 'connections',
      title: '连接',
      width: 72,
      cell: ({ row }) => row.connections ?? 0,
    },
    {
      colKey: 'uplink_bytes',
      title: '↑',
      width: 90,
      cell: ({ row }) => formatBytes(row.uplink_bytes),
    },
    {
      colKey: 'downlink_bytes',
      title: '↓',
      width: 90,
      cell: ({ row }) => formatBytes(row.downlink_bytes),
    },
    {
      colKey: 'memory_rss_bytes',
      title: '内存',
      width: 90,
      cell: ({ row }) => formatBytes(row.memory_rss_bytes),
    },
    {
      colKey: 'labels',
      title: '标签',
      cell: ({ row }) =>
        (row.labels ?? []).length ? (
          <Space size={4} breakLine>
            {(row.labels ?? []).map((l) => (
              <Tag key={l} size="small" variant="outline">
                {l}
              </Tag>
            ))}
          </Space>
        ) : (
          '—'
        ),
    },
    {
      colKey: 'last_seen_unix',
      title: '探测',
      width: 150,
      cell: ({ row }) => formatTime(row.last_seen_unix || row.metrics_at_unix),
    },
    {
      colKey: 'agent_version',
      title: 'Agent',
      width: 120,
      cell: ({ row }) => {
        const outdated = isAgentOutdated(row.agent_version, recommended)
        return (
          <span>
            <code className="la-mono">{row.agent_version || '—'}</code>
            {outdated ? (
              <Tag size="small" theme="warning" variant="light" style={{ marginLeft: 6 }}>
                可升级
              </Tag>
            ) : null}
          </span>
        )
      },
    },
    {
      colKey: 'ops',
      title: '操作',
      width: 220,
      fixed: 'right',
      cell: ({ row }) => (
        <Space size={4}>
          <Button size="small" variant="text" onClick={() => openDetail(row.id)}>
            详情
          </Button>
          <Button size="small" variant="text" onClick={() => void onProbe(row.id)}>
            探测
          </Button>
          <Button size="small" variant="text" onClick={() => void showInstall(row.id)}>
            安装
          </Button>
          {isAgentOutdated(row.agent_version, recommended) ? (
            <Button size="small" variant="text" theme="warning" onClick={() => void showUpgrade(row.id)}>
              升级
            </Button>
          ) : null}
          <Button
            size="small"
            variant="text"
            theme="danger"
            onClick={() => onDelete(row.id, row.name)}
          >
            删除
          </Button>
        </Space>
      ),
    },
  ]

  const selectedRowKeys = Array.from(selected)

  return (
    <div>
      <div className="la-page-header">
        <div>
          <h1 className="la-page-title">节点</h1>
          <p className="la-page-desc">
            状态监控 · 批量运维 · 安装与配置
            {meta?.panel_version ? (
              <>
                {' '}
                · Panel <code className="la-mono">{meta.panel_version}</code>
              </>
            ) : null}
            {recommended ? (
              <>
                {' '}
                · 推荐 Agent{' '}
                <code className="la-mono">{recommended}</code>
                {outdatedNodes.length > 0 ? (
                  <span style={{ color: 'var(--td-warning-color, #e37318)', marginLeft: 8 }}>
                    {outdatedNodes.length} 个节点可升级
                  </span>
                ) : (
                  <span style={{ color: 'var(--td-success-color, #2ba471)', marginLeft: 8 }}>
                    版本均已对齐
                  </span>
                )}
              </>
            ) : null}
          </p>
        </div>
        <Space breakLine align="center">
          <Radio.Group
            variant="default-filled"
            value={view}
            onChange={(v) => setViewMode(v as ViewMode)}
          >
            <Radio.Button value="cards">卡片</Radio.Button>
            <Radio.Button value="table">表格</Radio.Button>
          </Radio.Group>
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8, fontSize: 13 }}>
            <Switch size="small" value={auto} onChange={(v) => setAuto(Boolean(v))} />
            15s 自动刷新
          </span>
          <Button variant="outline" loading={busy} onClick={() => void loadCached()}>
            缓存
          </Button>
          <Button theme="primary" variant="outline" loading={busy} onClick={() => void refreshLive()}>
            探测全部
          </Button>
          <Button theme="primary" onClick={() => setAddOpen(true)}>
            添加节点
          </Button>
        </Space>
      </div>

      <StatsBar ov={ov} />

      {outdatedNodes.length > 0 && recommended ? (
        <Card bordered className="la-section" title="版本提醒">
          <p className="la-page-desc" style={{ marginTop: 0 }}>
            推荐 Agent <code className="la-mono">{recommended}</code>
            。以下节点版本落后或未知：优先点「升级」触发远程升级；失败时再复制手动命令：
          </p>
          <Space breakLine size={8} style={{ marginBottom: 12 }}>
            {outdatedNodes.map((n) => (
              <Tag
                key={n.id}
                theme="warning"
                variant="light"
                style={{ cursor: 'pointer' }}
                onClick={() => void showUpgrade(n.id)}
              >
                {n.name} · {n.agent_version || '未知'}
              </Tag>
            ))}
          </Space>
          <p className="la-page-desc" style={{ margin: 0 }}>
            点击节点「升级」会先尝试远程升级；失败时显示手动命令：
          </p>
          {meta?.agent_upgrade_command ? (
            <pre className="la-pre" style={{ marginTop: 8 }}>
              {meta.agent_upgrade_command}
            </pre>
          ) : null}
        </Card>
      ) : null}

      {upgradeBanner ? (
        <Card
          bordered
          className="la-section"
          title={`手动升级命令 · ${upgradeBanner.node.name}${
            upgradeBanner.recommended ? ` → ${upgradeBanner.recommended}` : ''
          }`}
          actions={
            <Space>
              <Button size="small" variant="outline" onClick={() => void copyUpgradeBanner()}>
                {copied ? '已复制' : '复制命令'}
              </Button>
              <Button size="small" variant="text" onClick={() => setUpgradeBanner(null)}>
                关闭
              </Button>
            </Space>
          }
        >
          <p className="la-page-desc" style={{ marginTop: 0 }}>
            当前 Agent <code className="la-mono">{upgradeBanner.node.agent_version || '—'}</code>
            。在目标服务器以 root 执行；不会改 Token / TLS / 配置。
          </p>
          <pre className="la-pre">{upgradeBanner.command}</pre>
        </Card>
      ) : null}

      {installBanner ? (
        <Card
          bordered
          className="la-section"
          title={`安装命令 · ${installBanner.node.name}`}
          actions={
            <Space>
              <Button size="small" variant="outline" onClick={() => void copyInstallBanner()}>
                {copied ? '已复制' : '复制命令'}
              </Button>
              <Button size="small" variant="text" onClick={() => setInstallBanner(null)}>
                关闭
              </Button>
            </Space>
          }
        >
          <pre className="la-pre">{installBanner.install_command}</pre>
          {installBanner.upgrade_command ? (
            <>
              <p className="la-page-desc">已装机后的升级命令：</p>
              <pre className="la-pre">{installBanner.upgrade_command}</pre>
            </>
          ) : null}
        </Card>
      ) : null}

      <Card
        bordered
        className="la-section"
        title={view === 'cards' ? '节点卡片' : '节点列表'}
        actions={
          <Space breakLine>
            <Input
              style={{ width: 180 }}
              value={labelFilter}
              onChange={(v) => setLabelFilter(String(v))}
              placeholder="按标签筛选…"
              clearable
            />
            <Button
              size="small"
              variant="outline"
              onClick={toggleAllVisible}
              disabled={nodes.length === 0}
            >
              {visibleAllSelected ? '取消全选' : '全选可见'}
            </Button>
            <Button
              size="small"
              variant="outline"
              disabled={busy || selected.size === 0}
              onClick={() => void runBatch('apply')}
            >
              下发配置 ({selected.size})
            </Button>
            <Button
              size="small"
              variant="outline"
              disabled={busy || selected.size === 0}
              onClick={() => void runBatch('start')}
            >
              启动
            </Button>
            <Button
              size="small"
              variant="outline"
              disabled={busy || selected.size === 0}
              onClick={() => void runBatch('stop')}
            >
              停止
            </Button>
          </Space>
        }
      >
        {nodes.length === 0 ? (
          <div style={{ textAlign: 'center', padding: '32px 0' }}>
            {allNodes.length === 0 ? (
              <Space direction="vertical">
                <span className="la-page-desc">暂无节点</span>
                <Button theme="primary" onClick={() => setAddOpen(true)}>
                  添加节点
                </Button>
              </Space>
            ) : (
              <span className="la-page-desc">没有匹配标签筛选的节点</span>
            )}
          </div>
        ) : view === 'cards' ? (
          <div className="la-node-grid">
            {nodes.map((n) => (
              <NodeCard
                key={n.id}
                node={n}
                recommended={recommended}
                checked={selected.has(n.id)}
                busy={actionId === n.id || busy}
                onToggle={() => toggle(n.id)}
                onDetail={() => openDetail(n.id)}
                onApply={() => void runOne(n.id, 'apply')}
                onStart={() => void runOne(n.id, 'start')}
                onStop={() => void runOne(n.id, 'stop')}
                onProbe={() => void onProbe(n.id)}
                onInstall={() => void showInstall(n.id)}
                onUpgrade={() => void showUpgrade(n.id)}
                onDelete={() => onDelete(n.id, n.name)}
              />
            ))}
          </div>
        ) : (
          <Table
            rowKey="id"
            data={nodes}
            columns={columns}
            selectedRowKeys={selectedRowKeys}
            onSelectChange={(keys) => setSelected(new Set(keys.map(String)))}
            hover
            size="small"
            tableLayout="auto"
          />
        )}
      </Card>

      {lastTask ? (
        <Card bordered className="la-section" title="最近任务">
          <p className="la-page-desc" style={{ marginTop: 0 }}>
            {lastTask.type} · {lastTask.status} · {lastTask.id}
          </p>
          <Table
            rowKey="node_id"
            size="small"
            data={lastTask.results ?? []}
            columns={[
              {
                colKey: 'node_id',
                title: '节点',
                cell: ({ row }) => (
                  <Button theme="primary" variant="text" onClick={() => openDetail(row.node_id)}>
                    <code className="la-mono">{row.node_id.slice(0, 8)}</code>
                  </Button>
                ),
              },
              {
                colKey: 'ok',
                title: '结果',
                width: 100,
                cell: ({ row }) => (
                  <Tag theme={row.ok ? 'success' : 'danger'} variant="light">
                    {row.ok ? '成功' : '失败'}
                  </Tag>
                ),
              },
              {
                colKey: 'message',
                title: '信息',
              },
            ]}
          />
        </Card>
      ) : null}

      <AddNodeModal
        open={addOpen}
        onClose={() => setAddOpen(false)}
        onCreated={() => void loadCached()}
        onOpenDetail={openDetail}
      />

      <NodeDetailDrawer
        nodeId={detailId}
        onClose={closeDetail}
        onChanged={() => void loadCached()}
      />
    </div>
  )
}

function NodeCard({
  node: n,
  recommended,
  checked,
  busy,
  onToggle,
  onDetail,
  onApply,
  onStart,
  onStop,
  onProbe,
  onInstall,
  onUpgrade,
  onDelete,
}: {
  node: Node
  recommended?: string
  checked: boolean
  busy: boolean
  onToggle: () => void
  onDetail: () => void
  onApply: () => void
  onStart: () => void
  onStop: () => void
  onProbe: () => void
  onInstall: () => void
  onUpgrade: () => void
  onDelete: () => void
}) {
  const online = isOnlineStatus(n.status)
  const outdated = isAgentOutdated(n.agent_version, recommended)
  return (
    <article
      className={`la-node-card ${online ? 'is-online' : 'is-offline'} ${checked ? 'is-selected' : ''}`}
    >
      <header className="la-node-card-header">
        <div className="la-node-card-title">
          <Checkbox checked={checked} onChange={onToggle} />
          <button type="button" className="name" style={linkBtnStyle} onClick={onDetail}>
            {n.name}
          </button>
        </div>
        <Space size={4}>
          <Tag theme={statusTheme(n.status)} variant="light" size="small">
            {statusLabel(n.status)}
          </Tag>
          <Tag theme={runtimeTheme(n.runtime_state)} variant="light" size="small">
            {runtimeLabel(n.runtime_state)}
          </Tag>
        </Space>
      </header>

      <div>
        <code className="la-mono">
          {n.address || '（待填）'}:{n.grpc_port}
        </code>
        {n.public_address && n.public_address !== n.address ? (
          <div className="la-page-desc" style={{ margin: '2px 0 0' }}>
            订阅 <code className="la-mono">{n.public_address}</code>
          </div>
        ) : null}
        {n.port_mappings && n.port_mappings.length > 0 ? (
          <div className="la-page-desc" style={{ margin: '2px 0 0' }}>
            遗留端口映射 {n.port_mappings.length}
          </div>
        ) : null}
      </div>

      {(n.labels?.length ?? 0) > 0 ? (
        <Space size={4} breakLine>
          {n.labels.map((l) => (
            <Tag key={l} size="small" variant="outline">
              {l}
            </Tag>
          ))}
        </Space>
      ) : null}

      <div className="la-node-metrics">
        <div className="item">
          <span className="label">入站</span>
          <span className="value">{n.inbound_count ?? 0}</span>
        </div>
        <div className="item">
          <span className="label">连接</span>
          <span className="value">{n.connections ?? 0}</span>
        </div>
        <div className="item">
          <span className="label">↑ 上传</span>
          <span className="value">{formatBytes(n.uplink_bytes)}</span>
        </div>
        <div className="item">
          <span className="label">↓ 下载</span>
          <span className="value">{formatBytes(n.downlink_bytes)}</span>
        </div>
        <div className="item">
          <span className="label">内存</span>
          <span className="value">{formatBytes(n.memory_rss_bytes)}</span>
        </div>
        <div className="item">
          <span className="label">CPU</span>
          <span className="value">{(n.cpu_percent ?? 0).toFixed(1)}%</span>
        </div>
      </div>

      <div className="la-node-meta">
        <div>
          Agent {n.agent_version || '—'} · sing-box {n.singbox_version || '—'}
          {outdated ? (
            <Tag size="small" theme="warning" variant="light" style={{ marginLeft: 6 }}>
              可升级{recommended ? ` → ${recommended}` : ''}
            </Tag>
          ) : null}
        </div>
        <div>探测 {formatTime(n.last_seen_unix || n.metrics_at_unix)}</div>
        {n.config_hash ? <div>配置 {n.config_hash.slice(0, 12)}…</div> : null}
        {n.last_error ? <div className="la-node-error">{n.last_error}</div> : null}
      </div>

      <footer className="la-node-actions">
        <Button size="small" variant="outline" onClick={onDetail}>
          详情
        </Button>
        <Button size="small" variant="outline" loading={busy} onClick={onApply}>
          下发
        </Button>
        <Button size="small" variant="outline" loading={busy} onClick={onStart}>
          启动
        </Button>
        <Button size="small" variant="outline" loading={busy} onClick={onStop}>
          停止
        </Button>
        <Button size="small" variant="outline" onClick={onProbe}>
          探测
        </Button>
        <Button size="small" variant="outline" onClick={onInstall}>
          安装
        </Button>
        {outdated ? (
          <Button size="small" theme="warning" variant="outline" onClick={onUpgrade}>
            升级
          </Button>
        ) : null}
        <Button size="small" theme="danger" variant="outline" onClick={onDelete}>
          删除
        </Button>
      </footer>
    </article>
  )
}

const linkBtnStyle: React.CSSProperties = {
  border: 'none',
  background: 'none',
  padding: 0,
  cursor: 'pointer',
  color: 'var(--td-brand-color)',
  font: 'inherit',
  fontWeight: 600,
}
