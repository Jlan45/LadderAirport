import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  Sheet,
  SheetContent,
} from './ui/sheet'
import { Button } from './ui/button'
import { Checkbox } from './ui/checkbox'
import { Input } from './ui/input'
import { Label } from './ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from './ui/select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from './ui/tabs'
import { Badge } from './ui/badge'
import { Alert, AlertDescription, AlertTitle } from './ui/alert'
import {
  X,
  RefreshCw,
  Play,
  Square,
  Eye,
  EyeOff,
  Copy,
  Check,
  Activity,
  Terminal,
  Wifi,
} from 'lucide-react'
import {
  applyNode,
  getNodeInstallCommand,
  getNodeMetrics,
  listInbounds,
  listNodeInbounds,
  listNodeInterfaces,
  listNodes,
  previewNodeConfig,
  setNodeInboundBindings,
  startNode,
  stopNode,
  streamNodeLogs,
  updateNode,
  upgradeNode,
  type InboundConfig,
  type Metrics,
  type NetworkInterface,
  type Node,
  type NodeInboundBinding,
  type NodeInstallInfo,
  type Task,
  type UpdateNodeInput,
} from '../api/client'
import {
  formatBytes,
  isAgentOutdated,
  isOnlineStatus,
  runtimeLabel,
  runtimeTheme,
  statusLabel,
  statusTheme,
  taskKindLabel,
  taskStatusLabel,
} from '../lib/nodeDisplay'
import { copyText } from '../lib/clipboard'
import { useUnsavedNavigation } from '../lib/useUnsavedNavigation'
import { toast } from '../lib/toast'

type Props = {
  nodeId: string | null
  onClose: () => void
  onChanged: () => void
}

type InboundNATEdit = { public_address: string; public_port: number }
type LogEntry = { id: number; text: string }

type LoadOptions = {
  syncConnection?: boolean
  syncInbounds?: boolean
  fatal?: boolean
}

type DrawerAction =
  | 'save-connection'
  | 'save-inbounds'
  | 'preview'
  | 'apply'
  | 'start'
  | 'stop'
  | 'metrics'
  | 'upgrade'
  | 'install'

type ConnectionErrors = Partial<
  Record<'name' | 'address' | 'grpcPort' | 'publicAddress', string>
>

function hostValidationError(value: string, label: string): string {
  const host = value.trim()
  if (!host) return ''
  if (host.includes('://')) return `${label}只填写主机名或 IP，不要包含协议`
  if (/[\s/?#@]/.test(host)) return `${label}包含无效字符`
  if (host.startsWith('[') || host.endsWith(']')) return `${label}中的 IPv6 地址无需方括号`

  if (host.includes(':')) {
    const [address, zone, ...extra] = host.split('%')
    const colonCount = (address.match(/:/g) || []).length
    if (
      extra.length > 0 ||
      colonCount < 2 ||
      !/^[0-9a-f:.]+$/i.test(address) ||
      (zone !== undefined && !/^[a-z0-9_.-]+$/i.test(zone))
    ) {
      return `${label}不是有效的 IPv6 地址`
    }
    return ''
  }

  if (host.length > 253 || !/^[a-z0-9](?:[a-z0-9._-]*[a-z0-9])?$/i.test(host)) {
    return `${label}不是有效的主机名或 IP`
  }
  return ''
}

function normalizeLabels(labels: string[]): string[] {
  return Array.from(new Set(labels.map((label) => label.trim()).filter(Boolean)))
}

function inboundNATSnapshot(value: Record<string, InboundNATEdit>): string {
  return JSON.stringify(
    Object.entries(value)
      .map(([inboundId, nat]) => [
        inboundId,
        nat.public_address.trim(),
        Number(nat.public_port) || 0,
      ])
      .sort(([left], [right]) => String(left).localeCompare(String(right))),
  )
}

export default function NodeDetailDrawer({ nodeId, onClose, onChanged }: Props) {
  const open = !!nodeId
  const id = nodeId ?? ''

  const [node, setNode] = useState<Node | null>(null)
  const [allInbounds, setAllInbounds] = useState<InboundConfig[]>([])
  const [inboundNAT, setInboundNAT] = useState<Record<string, InboundNATEdit>>({})
  const [savedInboundNAT, setSavedInboundNAT] = useState<Record<string, InboundNATEdit>>({})
  const [inboundsLoading, setInboundsLoading] = useState(false)
  const [inboundsError, setInboundsError] = useState('')
  const [preview, setPreview] = useState('')
  const [metrics, setMetrics] = useState<Metrics | null>(null)
  const [task, setTask] = useState<Task | null>(null)
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [streaming, setStreaming] = useState(false)
  const [logsFollowing, setLogsFollowing] = useState(true)
  const [busyAction, setBusyAction] = useState<DrawerAction | null>(null)
  const busy = busyAction !== null
  const [loading, setLoading] = useState(false)
  const [loadError, setLoadError] = useState('')
  const [activeTab, setActiveTab] = useState<string>('connection')
  const abortRef = useRef<AbortController | null>(null)
  const logViewerRef = useRef<HTMLDivElement | null>(null)
  const nodeIdRef = useRef<string | null>(nodeId)
  const generationRef = useRef(0)
  const loadRequestRef = useRef(0)
  const connectionRequestRef = useRef(0)
  const inboundsRequestRef = useRef(0)
  const interfacesRequestRef = useRef(0)
  const logRequestRef = useRef(0)
  const logSequenceRef = useRef(0)
  nodeIdRef.current = nodeId

  const [editName, setEditName] = useState('')
  const [editLabels, setEditLabels] = useState<string[]>([])
  const [editLabelDraft, setEditLabelDraft] = useState('')
  const [editToken, setEditToken] = useState('')
  const [editTokenChanged, setEditTokenChanged] = useState(false)
  const [tokenVisible, setTokenVisible] = useState(false)
  const [editAddress, setEditAddress] = useState('')
  const [editPort, setEditPort] = useState<number | string>(50051)
  const [editPublic, setEditPublic] = useState('')
  const [editCA, setEditCA] = useState('')
  const [editTLSSkip, setEditTLSSkip] = useState(false)
  const [editEgress, setEditEgress] = useState('')
  const [connectionErrors, setConnectionErrors] = useState<ConnectionErrors>({})
  const [ifaces, setIfaces] = useState<NetworkInterface[]>([])
  const [ifacesError, setIfacesError] = useState('')
  const [ifacesLoading, setIfacesLoading] = useState(false)
  const [installInfo, setInstallInfo] = useState<NodeInstallInfo | null>(null)
  const [copied, setCopied] = useState(false)
  const [copiedUpgrade, setCopiedUpgrade] = useState(false)

  const isCurrentNode = useCallback(
    (targetId: string, generation: number) =>
      nodeIdRef.current === targetId && generationRef.current === generation,
    [],
  )

  const loadInterfaces = useCallback(async (targetId: string, generation: number) => {
    if (!targetId) return
    const request = ++interfacesRequestRef.current
    if (isCurrentNode(targetId, generation)) {
      setIfacesLoading(true)
      setIfacesError('')
    }
    try {
      const res = await listNodeInterfaces(targetId)
      if (!isCurrentNode(targetId, generation) || request !== interfacesRequestRef.current) return
      setIfaces(res.interfaces ?? [])
    } catch (err) {
      if (!isCurrentNode(targetId, generation) || request !== interfacesRequestRef.current) return
      setIfaces([])
      setIfacesError(err instanceof Error ? err.message : '无法拉取网卡列表')
    } finally {
      if (isCurrentNode(targetId, generation) && request === interfacesRequestRef.current) {
        setIfacesLoading(false)
      }
    }
  }, [isCurrentNode])

  const load = useCallback(async (
    targetId: string,
    generation: number,
    options: LoadOptions = {},
  ) => {
    if (!targetId) return
    const request = ++loadRequestRef.current
    const connectionRequest = options.syncConnection ? ++connectionRequestRef.current : 0
    const inboundsRequest = options.syncInbounds ? ++inboundsRequestRef.current : 0
    if (isCurrentNode(targetId, generation)) setLoadError('')
    if (options.syncInbounds && isCurrentNode(targetId, generation)) {
      setInboundsLoading(true)
      setInboundsError('')
    }
    try {
      const nodes = await listNodes()
      if (!isCurrentNode(targetId, generation)) return
      const n = (nodes ?? []).find((item) => item.id === targetId) ?? null
      if (!n) {
        if (request === loadRequestRef.current) {
          setNode(null)
          setAllInbounds([])
          setInboundNAT({})
          setSavedInboundNAT({})
          setLoadError('节点不存在或已被删除')
        }
        if (options.syncInbounds && inboundsRequest === inboundsRequestRef.current) {
          setInboundsError('节点不存在或已被删除')
        }
        return
      }

      if (request === loadRequestRef.current) {
        setNode(n)
        if (!isOnlineStatus(n.status)) setMetrics(null)
        if (options.fatal) setLoading(false)
      }
      if (options.syncConnection && connectionRequest === connectionRequestRef.current) {
        setEditName(n.name || '')
        setEditLabels(normalizeLabels(n.labels ?? []))
        setEditLabelDraft('')
        setEditToken(n.token || '')
        setEditTokenChanged(false)
        setEditAddress(n.address || '')
        setEditPort(n.grpc_port || 50051)
        setEditPublic(n.public_address || '')
        setEditCA(n.ca_cert_pem || '')
        setEditTLSSkip(!!n.tls_skip_verify)
        setEditEgress(n.egress_interface || '')
        setConnectionErrors({})
      }
      if (options.syncInbounds) {
        const [allResult, attachedResult] = await Promise.allSettled([
          listInbounds(),
          listNodeInbounds(targetId),
        ])
        if (!isCurrentNode(targetId, generation) || inboundsRequest !== inboundsRequestRef.current) return
        let list: InboundConfig[] = []
        if (allResult.status === 'fulfilled') {
          list = allResult.value ?? []
          setAllInbounds(list)
        } else {
          throw allResult.reason
        }
        const edit: Record<string, InboundNATEdit> = {}
        if (attachedResult.status === 'fulfilled') {
          const active = attachedResult.value ?? []
          for (const item of active) {
            edit[item.id] = {
              public_address: item.public_address || '',
              public_port: item.public_port || 0,
            }
          }
        } else {
          throw attachedResult.reason
        }
        setInboundNAT(edit)
        setSavedInboundNAT(JSON.parse(JSON.stringify(edit)) as Record<string, InboundNATEdit>)
      }
    } catch (err) {
      if (!isCurrentNode(targetId, generation)) return
      if (options.fatal && request === loadRequestRef.current) {
        setLoadError(err instanceof Error ? err.message : '获取节点详情失败')
        setLoading(false)
      }
      if (options.syncInbounds && inboundsRequest === inboundsRequestRef.current) {
        setInboundsError(err instanceof Error ? err.message : '加载入站关联失败')
      }
    } finally {
      if (isCurrentNode(targetId, generation)) {
        if (options.fatal && request === loadRequestRef.current) setLoading(false)
        if (options.syncInbounds && inboundsRequest === inboundsRequestRef.current) {
          setInboundsLoading(false)
        }
      }
    }
  }, [isCurrentNode])

  const connectionDirty = (() => {
    if (!node) return false
    return (
      editName !== (node.name || '') ||
      JSON.stringify(editLabels) !== JSON.stringify(normalizeLabels(node.labels ?? [])) ||
      (editTokenChanged && editToken !== (node.token || '')) ||
      editAddress !== (node.address || '') ||
      String(editPort) !== String(node.grpc_port) ||
      editPublic !== (node.public_address || '') ||
      editCA !== (node.ca_cert_pem || '') ||
      editTLSSkip !== (!!node.tls_skip_verify) ||
      editEgress !== (node.egress_interface || '')
    )
  })()

  const inboundsDirty =
    inboundNATSnapshot(inboundNAT) !== inboundNATSnapshot(savedInboundNAT)

  useUnsavedNavigation({
    active: open && (connectionDirty || inboundsDirty),
    message: '离开节点详情会丢失尚未保存的更改。',
  })

  useEffect(() => {
    if (!open) return
    const currentGeneration = ++generationRef.current
    setNode(null)
    setAllInbounds([])
    setInboundNAT({})
    setSavedInboundNAT({})
    setPreview('')
    setMetrics(null)
    setTask(null)
    setLogs([])
    setStreaming(false)
    setBusyAction(null)
    setLoading(true)
    setLoadError('')
    setActiveTab('connection')
    setIfaces([])
    setIfacesError('')
    setIfacesLoading(false)
    setInstallInfo(null)
    setCopied(false)
    setCopiedUpgrade(false)

    void load(id, currentGeneration, { syncConnection: true, syncInbounds: true, fatal: true })
    void loadInterfaces(id, currentGeneration)
    return () => {
      if (abortRef.current) {
        abortRef.current.abort()
        abortRef.current = null
      }
    }
  }, [id, open, load, loadInterfaces])

  useEffect(() => {
    if (!open || activeTab !== 'ops' || !node || !isOnlineStatus(node.status)) {
      setMetrics(null)
      return
    }
    const currentGeneration = generationRef.current
    const loadMetrics = async () => {
      try {
        const data = await getNodeMetrics(id)
        if (isCurrentNode(id, currentGeneration)) setMetrics(data)
      } catch {
        /* ignore background failures */
      }
    }
    void loadMetrics()
    const t = window.setInterval(() => {
      void loadMetrics()
    }, 10000)
    return () => window.clearInterval(t)
  }, [id, open, activeTab, node, isOnlineStatus, isCurrentNode])

  useEffect(() => {
    if (activeTab === 'ops' && streaming && logViewerRef.current && logsFollowing) {
      const viewer = logViewerRef.current
      viewer.scrollTop = viewer.scrollHeight
    }
  }, [logs, activeTab, streaming, logsFollowing])

  useEffect(() => {
    if (!open) return
    const warnBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault()
      event.returnValue = ''
    }
    if (connectionDirty || inboundsDirty) {
      window.addEventListener('beforeunload', warnBeforeUnload)
    }
    return () => window.removeEventListener('beforeunload', warnBeforeUnload)
  }, [open, connectionDirty, inboundsDirty])

  function requestClose() {
    if (!connectionDirty && !inboundsDirty) {
      onClose()
      return
    }
    const confirmClose = window.confirm(
      '放弃更改？\n有未保存的设置，关闭后将丢失这些更改。'
    )
    if (confirmClose) {
      onClose()
    }
  }

  function retryLoad() {
    const current = generationRef.current
    setLoading(true)
    setLoadError('')
    void load(id, current, { syncConnection: true, syncInbounds: true, fatal: true })
    void loadInterfaces(id, current)
  }

  function retryInbounds() {
    void load(id, generationRef.current, { syncInbounds: true })
  }

  function toggleInbound(inboundId: string) {
    if (busy || inboundsLoading) return
    setInboundNAT((current) => {
      const next = { ...current }
      if (next[inboundId]) {
        delete next[inboundId]
      } else {
        const inb = allInbounds.find((x) => x.id === inboundId)
        const listenPort = Number(inb?.params?.port) || 0
        next[inboundId] = { public_address: '', public_port: listenPort }
      }
      return next
    })
  }

  function updateInboundNAT(inboundId: string, patch: Partial<InboundNATEdit>) {
    setInboundNAT((current) => {
      const active = current[inboundId]
      if (!active) return current
      return { ...current, [inboundId]: { ...active, ...patch } }
    })
  }

  function clearConnectionError(field: keyof ConnectionErrors) {
    setConnectionErrors((current) => {
      if (!current[field]) return current
      const next = { ...current }
      delete next[field]
      return next
    })
  }

  async function onSaveConnection() {
    if (busy || !node) return
    const name = editName.trim()
    const address = editAddress.trim()
    const publicAddress = editPublic.trim()
    const port = Number(editPort)

    const errors: ConnectionErrors = {}
    if (!name) errors.name = '请填写节点名称'
    const nameError = hostValidationError(name, '节点名称')
    if (nameError) errors.name = nameError
    const addressError = hostValidationError(address, '控制面地址')
    if (addressError) errors.address = addressError
    const publicError = hostValidationError(publicAddress, '默认公网地址')
    if (publicError) errors.publicAddress = publicError
    if (!Number.isInteger(port) || port < 1 || port > 65535) {
      errors.grpcPort = 'gRPC 端口必须在 1 到 65535 之间'
    }

    if (Object.keys(errors).length > 0) {
      setConnectionErrors(errors)
      toast.warning('请修正表单中的错误后再保存')
      return
    }

    setConnectionErrors({})
    setBusyAction('save-connection')
    const current = generationRef.current
    try {
      const input: UpdateNodeInput = {
        name,
        labels: editLabels,
        address: address || undefined,
        grpc_port: port,
        public_address: publicAddress || undefined,
        ca_cert_pem: editCA || undefined,
        tls_skip_verify: editTLSSkip,
        egress_interface: editEgress || undefined,
      }
      if (editTokenChanged) input.token = editToken
      const updated = await updateNode(id, input)
      if (isCurrentNode(id, current)) {
        setNode(updated)
        setEditToken(updated.token || '')
        setEditTokenChanged(false)
      }
      toast.success('节点连接设置保存成功')
      onChanged()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : '保存失败')
    } finally {
      if (isCurrentNode(id, current)) setBusyAction(null)
    }
  }

  async function onSaveInbounds() {
    if (busy || inboundsLoading || !node) return
    setBusyAction('save-inbounds')
    const current = generationRef.current
    const bindings: NodeInboundBinding[] = Object.entries(inboundNAT).map(
      ([inboundId, nat]) => ({
        inbound_id: inboundId,
        public_address: nat.public_address.trim() || undefined,
        public_port: Number(nat.public_port) || undefined,
      }),
    )
    try {
      const res = await setNodeInboundBindings(id, bindings)
      if (isCurrentNode(id, current)) {
        if (res.apply_task) setTask(res.apply_task)
        else if (res.start_task) setTask(res.start_task)
        setSavedInboundNAT(JSON.parse(JSON.stringify(inboundNAT)) as Record<string, InboundNATEdit>)
      }
      const base = res.deploy_message || (res.deployed ? '关联已保存，配置已下发且核心服务已自动拉起' : '关联已保存')
      if (!res.deployed && res.apply_task?.status === 'failed') {
        toast.error(base)
      } else {
        toast.success(base)
      }
      void load(id, current, { syncConnection: false, syncInbounds: false })
      // Multi-stage polling to ensure the auto-started running state is fetched and shown in header badges
      setTimeout(() => {
        if (isCurrentNode(id, current)) {
          void load(id, current, { syncConnection: false, syncInbounds: false })
        }
      }, 1500)
      setTimeout(() => {
        if (isCurrentNode(id, current)) {
          void load(id, current, { syncConnection: false, syncInbounds: false })
        }
      }, 3500)
      onChanged()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : '保存失败')
    } finally {
      if (isCurrentNode(id, current)) setBusyAction(null)
    }
  }

  async function runAction(action: 'apply' | 'start' | 'stop') {
    if (busy) return
    setBusyAction(action)
    const current = generationRef.current
    try {
      const fn = action === 'apply' ? applyNode : action === 'start' ? startNode : stopNode
      const task = await fn(id)
      if (isCurrentNode(id, current)) setTask(task)
      const ok = task.results?.[0]?.ok
      const text = `${action === 'apply' ? '配置下发' : action === 'start' ? '启动' : '停止'} ${
        ok ? (action === 'apply' ? '成功（核心已自动拉起）' : '成功') : '失败'
      }: ${task.results?.[0]?.message || taskStatusLabel(task.status)}`
      if (ok) toast.success(text)
      else toast.error(text)
      void load(id, current, { syncConnection: false, syncInbounds: false })
      if (action === 'apply' || action === 'start') {
        setTimeout(() => {
          if (isCurrentNode(id, current)) {
            void load(id, current, { syncConnection: false, syncInbounds: false })
          }
        }, 1500)
        setTimeout(() => {
          if (isCurrentNode(id, current)) {
            void load(id, current, { syncConnection: false, syncInbounds: false })
          }
        }, 3500)
      }
      onChanged()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : '操作失败')
    } finally {
      if (isCurrentNode(id, current)) setBusyAction(null)
    }
  }

  async function onPreview() {
    if (busy) return
    setBusyAction('preview')
    const current = generationRef.current
    try {
      const res = await previewNodeConfig(id)
      if (isCurrentNode(id, current)) setPreview((res as { config?: string })?.config || '')
      toast.success('配置生成成功')
    } catch (err) {
      toast.error(err instanceof Error ? err.message : '生成预览失败')
    } finally {
      if (isCurrentNode(id, current)) setBusyAction(null)
    }
  }

  async function onMetrics() {
    if (busy || !node) return
    setBusyAction('metrics')
    const current = generationRef.current
    try {
      const data = await getNodeMetrics(id)
      if (isCurrentNode(id, current)) setMetrics(data)
      toast.success('已刷新实时监控指标')
    } catch (err) {
      toast.error(err instanceof Error ? err.message : '获取监控指标失败')
    } finally {
      if (isCurrentNode(id, current)) setBusyAction(null)
    }
  }

  async function onRemoteUpgrade() {
    if (busy || !node) return
    const recommended = installInfo?.recommended_agent_version || 'latest'
    const upgradeConfirm = window.confirm(`远程升级\n确定要下发远程升级指令到 Agent 吗？推荐版本为 ${recommended}。`)
    if (!upgradeConfirm) return

    setBusyAction('upgrade')
    const current = generationRef.current
    try {
      const res = await upgradeNode(id, { version: recommended })
      if (!res.ok) {
        toast.error(res.message || '远程升级指令下发失败')
        return
      }
      toast.success(res.message ? `已下发升级任务：${res.message}` : '升级指令下发成功')
      setTimeout(() => {
        if (isCurrentNode(id, current)) {
          void load(id, current, { syncConnection: false, syncInbounds: false })
          onChanged()
        }
      }, 4000)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : '远程升级失败')
    } finally {
      if (isCurrentNode(id, current)) setBusyAction(null)
    }
  }

  async function onShowInstall() {
    if (busy) return
    setBusyAction('install')
    const current = generationRef.current
    setCopied(false)
    setCopiedUpgrade(false)
    try {
      const info = await getNodeInstallCommand(id)
      if (isCurrentNode(id, current)) setInstallInfo(info)
      toast.success('已生成安装命令')
    } catch (err) {
      toast.error(err instanceof Error ? err.message : '获取安装命令失败')
    } finally {
      if (isCurrentNode(id, current)) setBusyAction(null)
    }
  }

  async function copyInstall() {
    if (!installInfo?.install_command) return
    try {
      await copyText(installInfo.install_command)
      setCopied(true)
      toast.success('已复制安装命令')
      setTimeout(() => setCopied(false), 2000)
    } catch {
      toast.error('复制失败')
    }
  }

  async function copyUpgrade() {
    if (!installInfo?.upgrade_command) return
    try {
      await copyText(installInfo.upgrade_command)
      setCopiedUpgrade(true)
      toast.success('已复制升级命令')
      setTimeout(() => setCopiedUpgrade(false), 2000)
    } catch {
      toast.error('复制失败')
    }
  }

  async function startLogs() {
    if (streaming) return
    setStreaming(true)
    setLogs([])
    setLogsFollowing(true)
    const current = generationRef.current
    const request = ++logRequestRef.current

    if (abortRef.current) abortRef.current.abort()
    const ac = new AbortController()
    abortRef.current = ac

    try {
      await streamNodeLogs(id, {
        tail: 100,
        signal: ac.signal,
        onLine: (line) => {
          const ts = line.ts ? new Date(line.ts).toISOString() : new Date().toISOString()
          setLogs((prev) => {
            const nextId = ++logSequenceRef.current
            const list = [...prev, { id: nextId, text: `[${ts}] ${line.level || 'info'} ${line.message}` }]
            return list.length > 600 ? list.slice(list.length - 500) : list
          })
        },
      })
    } catch (err) {
      if (request === logRequestRef.current && isCurrentNode(id, current) && (err as Error).name !== 'AbortError') {
        toast.error(err instanceof Error ? err.message : '日志流连接失败')
      }
    } finally {
      if (request === logRequestRef.current && isCurrentNode(id, current)) {
        setStreaming(false)
        abortRef.current = null
      }
    }
  }

  function stopLogs() {
    setStreaming(false)
    if (abortRef.current) {
      abortRef.current.abort()
      abortRef.current = null
    }
  }

  const attachedCount = Object.keys(inboundNAT).length
  const mappingCount = Object.values(inboundNAT).filter(
    (nat) => nat.public_address.trim() || nat.public_port > 0,
  ).length

  function egressOptions() {
    const opts = [{ value: '', label: '系统默认' }]
    for (const iface of ifaces) {
      const ips = iface.addresses && iface.addresses.length > 0 ? ` (${iface.addresses.join(', ')})` : ''
      opts.push({ value: iface.name, label: `${iface.name}${ips}` })
    }
    return opts
  }

  return (
    <Sheet open={open} onOpenChange={(v) => { if (!v) requestClose() }}>
      <SheetContent className="bg-zinc-950 border-zinc-800 text-zinc-100 p-0 sm:max-w-3xl flex flex-col h-full focus-visible:outline-none">
        {loading ? (
          <div className="flex flex-col items-center justify-center flex-1 space-y-3">
            <RefreshCw className="h-6 w-6 animate-spin text-zinc-500" />
            <p className="text-sm text-zinc-400">正在加载节点详情…</p>
          </div>
        ) : loadError || !node ? (
          <div className="flex-1 p-6 flex flex-col justify-between">
            <div className="space-y-4">
              <Alert variant="destructive">
                <AlertTitle>无法打开节点详情</AlertTitle>
                <AlertDescription>{loadError || '节点详情暂不可用'}</AlertDescription>
              </Alert>
            </div>
            <div className="flex justify-end gap-2">
              <Button onClick={retryLoad} className="gap-1">
                <RefreshCw className="h-4 w-4" /> 重试
              </Button>
              <Button variant="outline" onClick={onClose} className="border-zinc-800 hover:bg-zinc-900">
                关闭
              </Button>
            </div>
          </div>
        ) : (
          <div className="flex flex-col h-full overflow-hidden">
            {/* Hero Header */}
            <header className="p-6 border-b border-zinc-800 bg-zinc-900/40">
              <div className="flex items-start justify-between">
                <div className="space-y-1">
                  <h2 className="text-xl font-bold tracking-tight text-zinc-100">{node.name}</h2>
                  <div className="flex flex-wrap gap-1.5 pt-1">
                    <Badge variant={statusTheme(node.status) as any}>{statusLabel(node.status)}</Badge>
                    {node.runtime_state ? (
                      <Badge variant={runtimeTheme(node.runtime_state) as any}>{runtimeLabel(node.runtime_state)}</Badge>
                    ) : null}
                    {installInfo ? (
                      isAgentOutdated(node.agent_version, installInfo.recommended_agent_version) ? (
                        <Badge variant="warning">
                          可升级
                          {installInfo.recommended_agent_version
                            ? ` → ${installInfo.recommended_agent_version}`
                            : ''}
                        </Badge>
                      ) : node.agent_version ? (
                        <Badge variant="outline" className="border-emerald-900/50 text-emerald-400">
                          已最新 ({node.agent_version})
                        </Badge>
                      ) : null
                    ) : null}
                  </div>
                </div>
              </div>

              <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 mt-6 pt-6 border-t border-zinc-800/80 text-xs">
                <div className="space-y-1">
                  <span className="text-zinc-500 block">控制面</span>
                  <code className="text-zinc-300 font-mono">
                    {node.address || '（待填）'}:{node.grpc_port}
                  </code>
                </div>
                <div className="space-y-1">
                  <span className="text-zinc-500 block">订阅入口</span>
                  <code className="text-zinc-300 font-mono break-all">
                    {node.public_address || node.address || '—'}
                    {mappingCount > 0 ? ` (${mappingCount} 条入站 NAT)` : ''}
                  </code>
                </div>
                <div className="space-y-1">
                  <span className="text-zinc-500 block">版本</span>
                  <span className="text-zinc-300">
                    Agent <code className="text-zinc-300 font-mono">{node.agent_version || '—'}</code>
                    {node.singbox_version ? (
                      <> · sing-box <code className="text-zinc-300 font-mono">{node.singbox_version}</code></>
                    ) : null}
                  </span>
                </div>
              </div>
            </header>

            {/* Content Tabs */}
            <Tabs value={activeTab} onValueChange={setActiveTab} className="flex-1 flex flex-col overflow-hidden">
              <div className="px-6 pt-4 border-b border-zinc-900 bg-zinc-900/10">
                <TabsList className="bg-zinc-900 border border-zinc-800">
                  <TabsTrigger value="connection" className="data-[state=active]:bg-zinc-800 data-[state=active]:text-zinc-100">连接 / NAT</TabsTrigger>
                  <TabsTrigger value="inbounds" className="data-[state=active]:bg-zinc-800 data-[state=active]:text-zinc-100">
                    入站{attachedCount ? ` (${attachedCount})` : ''}
                  </TabsTrigger>
                  <TabsTrigger value="ops" className="data-[state=active]:bg-zinc-800 data-[state=active]:text-zinc-100">运维 & 日志</TabsTrigger>
                </TabsList>
              </div>

              <div className="flex-1 overflow-y-auto p-6 space-y-6">
                {/* Connection Tab */}
                <TabsContent value="connection" className="space-y-6 mt-0">
                  <div className="space-y-6">
                    {/* Node Info Section */}
                    <div className="space-y-4 rounded-lg border border-zinc-800 bg-zinc-900/20 p-5">
                      <h3 className="text-sm font-semibold text-zinc-200">节点信息</h3>
                      <div className="space-y-4">
                        <div className="flex flex-col space-y-1.5">
                          <Label htmlFor="node-edit-name" className="text-zinc-400">节点名称 *</Label>
                          <Input
                            id="node-edit-name"
                            value={editName}
                            disabled={busy}
                            onChange={(e) => {
                              setEditName(e.target.value)
                              clearConnectionError('name')
                            }}
                            className="bg-zinc-900 border-zinc-800"
                          />
                          {connectionErrors.name && (
                            <p className="text-xs text-red-500 font-medium">{connectionErrors.name}</p>
                          )}
                        </div>

                        <div className="flex flex-col space-y-1.5">
                          <Label htmlFor="node-edit-labels" className="text-zinc-400 font-medium">标签</Label>
                          <div className="flex flex-wrap gap-1.5 p-2 border border-zinc-800 rounded-md bg-zinc-900 min-h-[42px]">
                            {editLabels.map((lbl) => (
                              <Badge key={lbl} variant="secondary" className="gap-1 pr-1 bg-zinc-800 text-zinc-300">
                                {lbl}
                                <button
                                  type="button"
                                  onClick={() => setEditLabels(editLabels.filter((l) => l !== lbl))}
                                  className="text-zinc-400 hover:text-zinc-200 cursor-pointer"
                                >
                                  <X className="h-3 w-3" />
                                </button>
                              </Badge>
                            ))}
                            <input
                              id="node-edit-labels"
                              value={editLabelDraft}
                              disabled={busy}
                              onChange={(e) => setEditLabelDraft(e.target.value)}
                              onKeyDown={(e) => {
                                if (e.key === 'Enter' || e.key === ',') {
                                  e.preventDefault()
                                  const tag = editLabelDraft.trim()
                                  if (tag && !editLabels.includes(tag)) {
                                    setEditLabels([...editLabels, tag])
                                  }
                                  setEditLabelDraft('')
                                }
                              }}
                              placeholder={editLabels.length === 0 ? "输入标签后按 Enter" : "新标签..."}
                              className="flex-1 bg-transparent border-0 outline-none text-sm p-0 focus:ring-0 text-zinc-100 placeholder:text-zinc-600"
                            />
                          </div>
                        </div>

                        <div className="flex flex-col space-y-1.5">
                          <Label htmlFor="node-edit-token" className="text-zinc-400">Agent Token</Label>
                          <div className="relative">
                            <Input
                              id="node-edit-token"
                              type={tokenVisible ? 'text' : 'password'}
                              value={editToken}
                              disabled={busy}
                              autoComplete="new-password"
                              onChange={(e) => {
                                setEditToken(e.target.value)
                                setEditTokenChanged(true)
                              }}
                              className="bg-zinc-900 border-zinc-800 pr-10"
                              placeholder="留空时安装命令会回退使用系统默认 Token"
                            />
                            <button
                              type="button"
                              onClick={() => setTokenVisible(!tokenVisible)}
                              className="absolute right-3 top-2.5 text-zinc-400 hover:text-zinc-200"
                            >
                              {tokenVisible ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                            </button>
                          </div>
                        </div>
                      </div>
                    </div>

                    {/* Panel Controller Section */}
                    <div className="space-y-4 rounded-lg border border-zinc-800 bg-zinc-900/20 p-5">
                      <div className="space-y-1">
                        <h3 className="text-sm font-semibold text-zinc-200">Panel 控制面</h3>
                        <p className="text-xs text-zinc-500">Panel 拨号用。NAT 时填映射后的公网/VPN 地址与外部 gRPC 端口。</p>
                      </div>
                      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                        <div className="flex flex-col space-y-1.5">
                          <Label htmlFor="node-edit-address" className="text-zinc-400">控制面地址</Label>
                          <Input
                            id="node-edit-address"
                            value={editAddress}
                            disabled={busy}
                            onChange={(e) => {
                              setEditAddress(e.target.value)
                              clearConnectionError('address')
                            }}
                            className="bg-zinc-900 border-zinc-800"
                            placeholder="公网 IP / DDNS / VPN 地址"
                          />
                          {connectionErrors.address && (
                            <p className="text-xs text-red-500 font-medium">{connectionErrors.address}</p>
                          )}
                        </div>

                        <div className="flex flex-col space-y-1.5">
                          <Label htmlFor="node-edit-grpc-port" className="text-zinc-400">gRPC 端口 (外部映射)</Label>
                          <Input
                            id="node-edit-grpc-port"
                            type="number"
                            value={editPort}
                            min={1}
                            max={65535}
                            disabled={busy}
                            onChange={(e) => {
                              setEditPort(e.target.value)
                              clearConnectionError('grpcPort')
                            }}
                            className="bg-zinc-900 border-zinc-800"
                          />
                          {connectionErrors.grpcPort && (
                            <p className="text-xs text-red-500 font-medium">{connectionErrors.grpcPort}</p>
                          )}
                        </div>
                      </div>
                    </div>

                    {/* Public Sub Entry */}
                    <div className="space-y-4 rounded-lg border border-zinc-800 bg-zinc-900/20 p-5">
                      <div className="space-y-1">
                        <h3 className="text-sm font-semibold text-zinc-200">订阅默认入口</h3>
                        <p className="text-xs text-zinc-500">节点级默认 server host。各入站可在「入站」单独覆盖公网 IP/端口。</p>
                      </div>
                      <div className="flex flex-col space-y-1.5">
                        <Label htmlFor="node-edit-public" className="text-zinc-400">默认公网地址 / NAT IP</Label>
                        <Input
                          id="node-edit-public"
                          value={editPublic}
                          disabled={busy}
                          onChange={(e) => {
                            setEditPublic(e.target.value)
                            clearConnectionError('publicAddress')
                          }}
                          className="bg-zinc-900 border-zinc-800"
                          placeholder="客户端默认入口；空则回退控制面地址"
                        />
                        {connectionErrors.publicAddress && (
                          <p className="text-xs text-red-500 font-medium">{connectionErrors.publicAddress}</p>
                        )}
                      </div>
                    </div>

                    {/* TLS & Egress */}
                    <div className="space-y-4 rounded-lg border border-zinc-800 bg-zinc-900/20 p-5">
                      <div className="space-y-1">
                        <h3 className="text-sm font-semibold text-zinc-200">TLS 与出口</h3>
                        <p className="text-xs text-zinc-500">粘贴节点 ca.crt；出口网卡修改后需下发配置生效。</p>
                      </div>
                      <div className="space-y-4">
                        <div className="flex flex-col space-y-1.5">
                          <Label htmlFor="node-edit-ca" className="text-zinc-400">CA 证书</Label>
                          <textarea
                            id="node-edit-ca"
                            value={editCA}
                            disabled={busy}
                            onChange={(e) => setEditCA(e.target.value)}
                            placeholder="-----BEGIN CERTIFICATE----- ..."
                            rows={4}
                            className="flex w-full rounded-md border border-zinc-800 bg-zinc-900 px-3 py-2 text-xs font-mono text-zinc-300 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
                          />
                        </div>

                        <div className="flex items-center space-x-2">
                          <Checkbox
                            id="node-edit-tls-skip"
                            checked={editTLSSkip}
                            onCheckedChange={(c) => setEditTLSSkip(Boolean(c))}
                          />
                          <Label htmlFor="node-edit-tls-skip" className="text-zinc-300 cursor-pointer">
                            跳过 TLS 证书验证 (仅用于测试环境)
                          </Label>
                        </div>

                        <div className="flex flex-col space-y-1.5">
                          <Label htmlFor="node-edit-egress" className="text-zinc-400">出口网卡</Label>
                          <div className="flex gap-2">
                            <div className="flex-1">
                              <Select
                                disabled={busy}
                                value={editEgress}
                                onValueChange={setEditEgress}
                              >
                                <SelectTrigger className="bg-zinc-900 border-zinc-800" id="node-edit-egress">
                                  <SelectValue placeholder="系统默认" />
                                </SelectTrigger>
                                <SelectContent className="bg-zinc-900 border-zinc-800">
                                  {egressOptions().map((opt) => (
                                    <SelectItem key={opt.value} value={opt.value} className="text-zinc-200">
                                      {opt.label}
                                    </SelectItem>
                                  ))}
                                </SelectContent>
                              </Select>
                            </div>
                            <Button
                              variant="outline"
                              onClick={() => void loadInterfaces(id, generationRef.current)}
                              loading={ifacesLoading}
                              disabled={busy}
                              className="border-zinc-800 hover:bg-zinc-900 gap-1.5 shrink-0"
                            >
                              <RefreshCw className="h-3.5 w-3.5" /> 刷新
                            </Button>
                          </div>
                          {ifacesError && (
                            <Alert variant="warning" className="mt-2">
                              <AlertDescription>无法从 Agent 拉取网卡：{ifacesError}</AlertDescription>
                            </Alert>
                          )}
                        </div>
                      </div>
                    </div>

                    {/* Actions Panel */}
                    <div className="flex flex-wrap gap-2 pt-2 border-t border-zinc-800 justify-between items-center">
                      <Button
                        loading={busyAction === 'save-connection'}
                        disabled={busy || !connectionDirty}
                        onClick={() => void onSaveConnection()}
                      >
                        保存连接设置
                      </Button>
                      <Button
                        variant="outline"
                        loading={busyAction === 'install'}
                        disabled={busy || connectionDirty}
                        onClick={() => void onShowInstall()}
                        className="border-zinc-800 hover:bg-zinc-900 text-zinc-300"
                      >
                        {connectionDirty ? '请先保存连接设置' : installInfo ? '刷新安装命令' : '显示安装命令'}
                      </Button>
                    </div>

                    {/* One-click install commands */}
                    {installInfo && !connectionDirty && (
                      <div className="space-y-4 rounded-lg border border-zinc-800 bg-zinc-900/30 p-5 mt-4">
                        <div className="flex items-center justify-between">
                          <h3 className="text-sm font-semibold text-zinc-200">一键安装</h3>
                          <Button
                            size="sm"
                            variant="outline"
                            className="border-zinc-800 text-zinc-300 hover:bg-zinc-900 gap-1"
                            onClick={() => void copyInstall()}
                          >
                            {copied ? <Check className="h-3.5 w-3.5 text-emerald-500" /> : <Copy className="h-3.5 w-3.5" />}
                            {copied ? '已复制' : '复制'}
                          </Button>
                        </div>
                        <pre className="p-3 bg-zinc-900 border border-zinc-800/80 rounded-md overflow-x-auto text-xs font-mono text-zinc-300 max-h-[140px]">
                          {installInfo.install_command}
                        </pre>

                        {installInfo.upgrade_command && (
                          <div className="space-y-2 mt-4 pt-4 border-t border-zinc-800/60">
                            <div className="flex items-center justify-between">
                              <h3 className="text-sm font-semibold text-zinc-200">
                                升级
                                {installInfo.recommended_agent_version
                                  ? ` → ${installInfo.recommended_agent_version}`
                                  : ''}
                              </h3>
                              <Button
                                size="sm"
                                variant="outline"
                                className="border-zinc-800 text-zinc-300 hover:bg-zinc-900 gap-1"
                                onClick={() => void copyUpgrade()}
                              >
                                {copiedUpgrade ? <Check className="h-3.5 w-3.5 text-emerald-500" /> : <Copy className="h-3.5 w-3.5" />}
                                {copiedUpgrade ? '已复制' : '复制'}
                              </Button>
                            </div>
                            <p className="text-xs text-zinc-500 leading-normal">
                              在节点上以 root 用户执行此升级命令；升级会保留现有的 Token、TLS 密钥和业务配置。
                            </p>
                            <pre className="p-3 bg-zinc-900 border border-zinc-800/80 rounded-md overflow-x-auto text-xs font-mono text-zinc-300 max-h-[140px]">
                              {installInfo.upgrade_command}
                            </pre>
                          </div>
                        )}
                      </div>
                    )}
                  </div>
                </TabsContent>

                {/* Inbounds Tab */}
                <TabsContent value="inbounds" className="space-y-6 mt-0">
                  <div className="space-y-6">
                    <div className="space-y-4 rounded-lg border border-zinc-800 bg-zinc-900/20 p-5">
                      <div className="space-y-1">
                        <div className="flex items-center justify-between">
                          <h3 className="text-sm font-semibold text-zinc-200">关联入站 + NAT 映射</h3>
                          <Badge variant="success" className="text-[10px] px-2 py-0.5 font-medium">
                            保存后自动下发并启动核心
                          </Badge>
                        </div>
                        <p className="text-xs text-zinc-500">
                          勾选入站后可填写该入站的公网 IP/域名 和公网端口（仅订阅用）。保存后会自动下发 sing-box 配置并<strong>自动启动核心服务</strong>。
                        </p>
                      </div>

                      {inboundsLoading ? (
                        <div className="flex items-center justify-center p-6 space-x-2">
                          <RefreshCw className="h-4 w-4 animate-spin text-zinc-500" />
                          <span className="text-sm text-zinc-400">正在加载入站关联…</span>
                        </div>
                      ) : inboundsError ? (
                        <Alert variant="destructive">
                          <AlertTitle>入站关联暂不可用</AlertTitle>
                          <AlertDescription>{inboundsError}</AlertDescription>
                          <Button size="sm" variant="outline" className="mt-2 text-red-400 border-red-900/30 hover:bg-red-950/20" onClick={retryInbounds}>
                            重试
                          </Button>
                        </Alert>
                      ) : allInbounds.length === 0 ? (
                        <Alert className="bg-zinc-900 border-zinc-800 text-zinc-300">
                          <AlertDescription>
                            无入站配置。请先在 <Link to="/inbounds" className="text-primary hover:underline underline-offset-4 font-semibold">入站配置管理</Link> 中创建。
                          </AlertDescription>
                        </Alert>
                      ) : (
                        <div className="space-y-3">
                          {allInbounds.map((inb) => {
                            const listenPort = Number(inb.params?.port) || 0
                            const checked = !!inboundNAT[inb.id]
                            const nat = inboundNAT[inb.id]
                            return (
                              <div
                                key={inb.id}
                                className={`flex flex-col gap-3 p-4 rounded-lg border transition-all ${
                                  checked
                                    ? 'border-zinc-700 bg-zinc-900/40 shadow-sm'
                                    : 'border-zinc-900 bg-zinc-950/20 hover:border-zinc-800'
                                } ${!inb.enabled ? 'opacity-50' : ''}`}
                              >
                                <div className="flex items-start justify-between">
                                  <div className="flex items-center space-x-3">
                                    <Checkbox
                                      id={`inb-check-${inb.id}`}
                                      checked={checked}
                                      disabled={busy || inboundsLoading || (!inb.enabled && !checked)}
                                      onCheckedChange={() => toggleInbound(inb.id)}
                                    />
                                    <div className="flex flex-col">
                                      <Label
                                        htmlFor={`inb-check-${inb.id}`}
                                        className="text-sm font-semibold text-zinc-200 flex items-center gap-2 cursor-pointer"
                                      >
                                        {inb.name}
                                        {!inb.enabled && (
                                          <Badge variant="secondary">已禁用</Badge>
                                        )}
                                      </Label>
                                      <span className="text-xs text-zinc-500 font-mono">
                                        {inb.protocol}
                                        {listenPort ? ` · 监听 ${listenPort}` : ''}
                                      </span>
                                    </div>
                                  </div>
                                </div>

                                {checked && (
                                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 pl-7 pt-2 border-t border-zinc-800/40">
                                    <div className="flex flex-col space-y-1.5">
                                      <span className="text-xs text-zinc-400">公网 IP / 域名</span>
                                      <Input
                                        disabled={busy || inboundsLoading}
                                        value={nat?.public_address || ''}
                                        placeholder="空 = 默认公网地址"
                                        onChange={(e) =>
                                          updateInboundNAT(inb.id, { public_address: e.target.value })
                                        }
                                        className="h-8 bg-zinc-900 border-zinc-800"
                                      />
                                    </div>
                                    <div className="flex flex-col space-y-1.5">
                                      <span className="text-xs text-zinc-400">公网端口</span>
                                      <Input
                                        type="number"
                                        disabled={busy || inboundsLoading}
                                        value={nat?.public_port || ''}
                                        placeholder={listenPort ? String(listenPort) : '同监听口'}
                                        onChange={(e) =>
                                          updateInboundNAT(inb.id, {
                                            public_port: Number(e.target.value) || 0,
                                          })
                                        }
                                        className="h-8 bg-zinc-900 border-zinc-800"
                                      />
                                    </div>
                                    <div className="col-span-1 sm:col-span-2 text-xs text-zinc-500 font-mono mt-1">
                                      订阅解析入口：
                                      <code className="text-zinc-300 bg-zinc-900 px-1 py-0.5 rounded font-mono">
                                        {(nat?.public_address || editPublic || node?.address || '—') +
                                          ':' +
                                          String(
                                            nat?.public_port && nat.public_port > 0
                                              ? nat.public_port
                                              : listenPort || '—',
                                          )}
                                      </code>
                                    </div>
                                  </div>
                                )}
                              </div>
                            )
                          })}
                        </div>
                      )}
                    </div>

                    {/* Actions */}
                    <div className="flex justify-start border-t border-zinc-850 pt-4">
                      <Button
                        loading={busyAction === 'save-inbounds'}
                        disabled={busy || inboundsLoading || Boolean(inboundsError) || !inboundsDirty}
                        onClick={() => void onSaveInbounds()}
                      >
                        保存关联并下发配置
                      </Button>
                    </div>

                    {/* Task details */}
                    {task && (
                      <div className="rounded-lg border border-zinc-800 bg-zinc-900/30 p-5">
                        <h3 className="text-sm font-semibold text-zinc-200 mb-3">最近任务结果</h3>
                        <div className="p-3.5 bg-zinc-950 rounded border border-zinc-800 text-xs font-mono space-y-2 text-zinc-300">
                          <div>
                            ID: <span className="text-zinc-400">{task.id}</span>
                          </div>
                          <div>
                            类型: <span className="text-zinc-400">{taskKindLabel(task.type)}</span>
                          </div>
                          <div>
                            状态: <span className="text-zinc-400">{taskStatusLabel(task.status)}</span>
                          </div>
                          <div className="pt-2 border-t border-zinc-800 mt-2 space-y-1">
                            {(task.results || []).map((r, idx) => (
                              <div key={idx} className="flex gap-2 items-center">
                                <span className={r.ok ? 'text-emerald-500 font-bold' : 'text-red-500 font-bold'}>
                                  {r.ok ? '✓' : '✗'}
                                </span>
                                <span>{r.message}</span>
                              </div>
                            ))}
                          </div>
                        </div>
                      </div>
                    )}
                  </div>
                </TabsContent>

                {/* Operations & Logs Tab */}
                <TabsContent value="ops" className="space-y-6 mt-0">
                  <div className="space-y-6">
                    {/* Lifecycle Control */}
                    <div className="space-y-4 rounded-lg border border-zinc-800 bg-zinc-900/20 p-5">
                      <div className="space-y-1">
                        <h3 className="text-sm font-semibold text-zinc-200">生命周期与管控</h3>
                        <p className="text-xs text-zinc-500">启停核心服务、预览配置、刷新性能监控指标。</p>
                      </div>
                      <div className="grid grid-cols-2 sm:grid-cols-3 gap-2">
                        <Button
                          loading={busyAction === 'start'}
                          disabled={busy}
                          onClick={() => void runAction('start')}
                          className="w-full gap-1.5"
                        >
                          <Play className="h-4 w-4" /> 启动服务
                        </Button>
                        <Button
                          variant="destructive"
                          loading={busyAction === 'stop'}
                          disabled={busy}
                          onClick={() => void runAction('stop')}
                          className="w-full gap-1.5"
                        >
                          <Square className="h-4 w-4" /> 停止服务
                        </Button>
                        <Button
                          variant="outline"
                          loading={busyAction === 'preview'}
                          disabled={busy}
                          onClick={() => void onPreview()}
                          className="w-full border-zinc-800 text-zinc-300 hover:bg-zinc-900 gap-1.5"
                        >
                          <Eye className="h-4 w-4" /> 预览配置
                        </Button>
                        <Button
                          variant="outline"
                          loading={busyAction === 'metrics'}
                          disabled={busy || !isOnlineStatus(node.status)}
                          onClick={() => void onMetrics()}
                          className="w-full border-zinc-800 text-zinc-300 hover:bg-zinc-900 gap-1.5"
                        >
                          <Activity className="h-4 w-4" /> 刷新监控
                        </Button>
                        <Button
                          variant="outline"
                          loading={busyAction === 'apply'}
                          disabled={busy}
                          onClick={() => void runAction('apply')}
                          className="w-full border-zinc-800 text-zinc-300 hover:bg-zinc-900 gap-1.5"
                        >
                          <RefreshCw className="h-4 w-4" /> 重新下发
                        </Button>
                        {(() => {
                          const isOutdated = installInfo ? isAgentOutdated(node.agent_version, installInfo.recommended_agent_version) : false
                          return (
                            <Button
                              variant="outline"
                              loading={busyAction === 'upgrade'}
                              disabled={busy}
                              onClick={() => void onRemoteUpgrade()}
                              title={isOutdated ? '远程升级 Agent 到最新推荐版本' : '当前已是最新版本，再次点击可重推送升级指令'}
                              className={`w-full border-zinc-800 gap-1.5 ${
                                isOutdated
                                  ? 'text-amber-400 hover:bg-zinc-900 hover:text-amber-300'
                                  : 'text-zinc-400 hover:bg-zinc-900 hover:text-zinc-200'
                              }`}
                            >
                              <Wifi className="h-4 w-4" /> {isOutdated ? '远程升级' : '重新升级 (已最新)'}
                            </Button>
                          )
                        })()}
                      </div>

                      {task && (
                        <div className="p-3.5 bg-zinc-950 rounded border border-zinc-800 text-xs font-mono space-y-1 text-zinc-300 mt-3">
                          <div className="font-semibold text-zinc-200">
                            任务: {task.id.slice(0, 8)}… — {taskKindLabel(task.type)} ({taskStatusLabel(task.status)})
                          </div>
                          <ul className="list-disc pl-4 space-y-0.5 text-zinc-400 mt-1">
                            {(task.results || []).map((r, i) => (
                              <li key={i}>
                                {r.ok ? '✓' : '✗'} {r.message}
                              </li>
                            ))}
                          </ul>
                        </div>
                      )}
                    </div>

                    {/* Metrics Dashboard */}
                    {metrics && isOnlineStatus(node.status) && (
                      <div className="space-y-4 rounded-lg border border-zinc-800 bg-zinc-900/20 p-5">
                        <h3 className="text-sm font-semibold text-zinc-200">监控指标</h3>
                        <div className="grid grid-cols-2 sm:grid-cols-5 gap-4">
                          <div className="bg-zinc-900/60 p-3 rounded-lg border border-zinc-800/80">
                            <span className="text-[10px] text-zinc-500 block uppercase tracking-wider">活动连接</span>
                            <span className="text-lg font-bold text-zinc-100 mt-1 block">{metrics.connections}</span>
                          </div>
                          <div className="bg-zinc-900/60 p-3 rounded-lg border border-zinc-800/80">
                            <span className="text-[10px] text-zinc-500 block uppercase tracking-wider">上行流量</span>
                            <span className="text-lg font-bold text-zinc-100 mt-1 block">{formatBytes(metrics.uplink_bytes)}</span>
                          </div>
                          <div className="bg-zinc-900/60 p-3 rounded-lg border border-zinc-800/80">
                            <span className="text-[10px] text-zinc-500 block uppercase tracking-wider">下行流量</span>
                            <span className="text-lg font-bold text-zinc-100 mt-1 block">{formatBytes(metrics.downlink_bytes)}</span>
                          </div>
                          <div className="bg-zinc-900/60 p-3 rounded-lg border border-zinc-800/80">
                            <span className="text-[10px] text-zinc-500 block uppercase tracking-wider">CPU 使用率</span>
                            <span className="text-lg font-bold text-zinc-100 mt-1 block">
                              {metrics.cpu_percent?.toFixed?.(1) ?? metrics.cpu_percent}%
                            </span>
                          </div>
                          <div className="bg-zinc-900/60 p-3 rounded-lg border border-zinc-800/80 col-span-2 sm:col-span-1">
                            <span className="text-[10px] text-zinc-500 block uppercase tracking-wider">内存占用</span>
                            <span className="text-lg font-bold text-zinc-100 mt-1 block">{formatBytes(metrics.memory_rss_bytes)}</span>
                          </div>
                        </div>
                      </div>
                    )}

                    {/* Preview configuration */}
                    {preview && (
                      <div className="space-y-4 rounded-lg border border-zinc-800 bg-zinc-900/20 p-5">
                        <h3 className="text-sm font-semibold text-zinc-200">配置预览</h3>
                        <pre className="p-4 rounded-lg bg-zinc-950 border border-zinc-850 overflow-x-auto text-xs font-mono text-zinc-300 max-h-[300px] leading-relaxed">
                          {preview}
                        </pre>
                      </div>
                    )}

                    {/* Live logs console */}
                    <div className="space-y-4 rounded-lg border border-zinc-800 bg-zinc-900/20 p-5">
                      <div className="flex flex-wrap gap-4 items-center justify-between pb-3 border-b border-zinc-800">
                        <div className="space-y-1">
                          <h3 className="text-sm font-semibold text-zinc-200 flex items-center gap-1.5">
                            <Terminal className="h-4 w-4 text-zinc-500" />
                            节点日志
                          </h3>
                          <p className="text-xs text-zinc-500">实时流式拉取 Agent 日志。</p>
                        </div>
                        <div className="flex items-center gap-4">
                          <div className="flex items-center space-x-2">
                            <Checkbox
                              id="logs-follow-chk"
                              checked={logsFollowing}
                              disabled={logs.length === 0 && !streaming}
                              onCheckedChange={(checked) => setLogsFollowing(Boolean(checked))}
                            />
                            <Label htmlFor="logs-follow-chk" className="text-xs text-zinc-400 cursor-pointer">
                              跟随日志
                            </Label>
                          </div>

                          {!streaming ? (
                            <Button
                              size="sm"
                              onClick={() => void startLogs()}
                              className="gap-1.5 h-8 px-3"
                            >
                              <Play className="h-3.5 w-3.5" /> 开启
                            </Button>
                          ) : (
                            <Button
                              size="sm"
                              variant="outline"
                              onClick={stopLogs}
                              className="border-zinc-850 hover:bg-zinc-900 text-zinc-300 gap-1.5 h-8 px-3"
                            >
                              <Square className="h-3.5 w-3.5" /> 停止
                            </Button>
                          )}
                        </div>
                      </div>

                      {/* Log viewer output */}
                      <div
                        ref={logViewerRef}
                        className="p-4 rounded-lg bg-black border border-zinc-900 overflow-y-auto text-xs font-mono text-zinc-400 h-[300px] leading-relaxed focus:outline-none focus:ring-1 focus:ring-zinc-800"
                        role="log"
                        aria-label="节点实时日志"
                        aria-relevant="additions"
                        tabIndex={0}
                        onScroll={(event) => {
                          const viewer = event.currentTarget
                          const distance = viewer.scrollHeight - viewer.scrollTop - viewer.clientHeight
                          setLogsFollowing(distance < 24)
                        }}
                      >
                        {logs.length === 0 ? (
                          <div className="flex items-center justify-center h-full text-zinc-600">
                            {streaming ? '正在等待日志输出…' : '点击“开启”连接日志流'}
                          </div>
                        ) : (
                          logs.map((line) => (
                            <div key={line.id} className="border-b border-zinc-950/20 py-0.5 hover:bg-zinc-900/30 whitespace-pre-wrap">
                              {line.text}
                            </div>
                          ))
                        )}
                      </div>
                    </div>
                  </div>
                </TabsContent>
              </div>
            </Tabs>
          </div>
        )}
      </SheetContent>
    </Sheet>
  )
}
