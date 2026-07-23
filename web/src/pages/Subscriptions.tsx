import { useCallback, useEffect, useRef, useState } from 'react'
import {
  Alert,
  AlertDescription,
  AlertTitle,
} from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Badge } from '@/components/ui/badge'
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from '@/components/ui/tabs'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import {
  Plus,
  RefreshCw,
  Edit,
  Copy,
  Check,
  Eye,
  RotateCw,
  Trash2,
  QrCode,
  ExternalLink,
  Rss,
  Globe,
  Radio,
  Layers,
  Server,
} from 'lucide-react'
import {
  createExternalSource,
  createSubscription,
  deleteExternalSource,
  deleteSubscription,
  listExternalSources,
  listInbounds,
  listSubscriptions,
  previewExternalSource,
  previewSubscription,
  refreshExternalSource,
  updateExternalSource,
  updateSubscription,
  type ExternalSource,
  type InboundConfig,
  type Subscription,
} from '../api/client'
import { copyText } from '../lib/clipboard'
import { formatTime } from '../lib/nodeDisplay'
import { toast } from '../lib/toast'
import { QRCodeModal } from '../components/QRCodeModal'

type LocalMode = 'all' | 'custom' | 'none'
type NumericDraft = number | string

type SubscriptionEditor = {
  open: boolean
  id: string | null
  name: string
  format: 'clash' | 'singbox'
  enabled: boolean
  localMode: LocalMode
  inboundIds: Set<string>
  sourceIds: Set<string>
}

type SourceEditor = {
  open: boolean
  id: string | null
  name: string
  url: string
  interval: NumericDraft
  headers: string
  enabled: boolean
}

type SourcePreview = Awaited<ReturnType<typeof previewExternalSource>>

const EMPTY_SUB_EDITOR: SubscriptionEditor = {
  open: false,
  id: null,
  name: '',
  format: 'clash',
  enabled: true,
  localMode: 'all',
  inboundIds: new Set(),
  sourceIds: new Set(),
}

const EMPTY_SOURCE_EDITOR: SourceEditor = {
  open: false,
  id: null,
  name: '',
  url: '',
  interval: 86400,
  headers: '',
  enabled: true,
}

export default function Subscriptions() {
  const [subscriptions, setSubscriptions] = useState<Subscription[]>([])
  const [inbounds, setInbounds] = useState<InboundConfig[]>([])
  const [sources, setSources] = useState<ExternalSource[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [pending, setPending] = useState<Set<string>>(new Set())
  const [saving, setSaving] = useState(false)
  const [editorError, setEditorError] = useState('')
  const [subEditor, setSubEditor] = useState<SubscriptionEditor>(EMPTY_SUB_EDITOR)
  const [sourceEditor, setSourceEditor] = useState<SourceEditor>(EMPTY_SOURCE_EDITOR)
  const [textPreview, setTextPreview] = useState<{ title: string; text: string } | null>(null)
  const [sourcePreview, setSourcePreview] = useState<SourcePreview | null>(null)
  const [sourcePreviewTitle, setSourcePreviewTitle] = useState('')
  const [copiedId, setCopiedId] = useState<string | null>(null)
  
  // QR Code Modal state
  const [qrModal, setQrModal] = useState<{ open: boolean; title: string; url: string }>({
    open: false,
    title: '',
    url: '',
  })

  const loadVersion = useRef(0)
  const pendingRef = useRef<Set<string>>(new Set())
  const savingRef = useRef(false)

  const load = useCallback(async () => {
    if (savingRef.current || pendingRef.current.size > 0) return
    const version = ++loadVersion.current
    setLoading(true)
    setLoadError('')
    const results = await Promise.allSettled([
      listSubscriptions(),
      listInbounds(),
      listExternalSources(),
    ])
    if (version !== loadVersion.current) return

    const errors: string[] = []
    if (results[0].status === 'fulfilled') setSubscriptions(results[0].value ?? [])
    else errors.push(errorText(results[0].reason, '订阅列表加载失败'))
    if (results[1].status === 'fulfilled') setInbounds(results[1].value ?? [])
    else errors.push(errorText(results[1].reason, '入站列表加载失败'))
    if (results[2].status === 'fulfilled') setSources(results[2].value ?? [])
    else errors.push(errorText(results[2].reason, '外部源列表加载失败'))
    setLoadError(errors.join('；'))
    setLoading(false)
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  function beginOperation(key: string): boolean {
    const separator = key.lastIndexOf(':')
    const namespace = key.startsWith('subscription-') ? 'subscription-' : 'source-'
    const entityID = separator >= 0 ? key.slice(separator + 1) : ''
    if (
      pendingRef.current.has(key) ||
      Array.from(pendingRef.current).some(
        (candidate) => candidate.startsWith(namespace) && candidate.endsWith(`:${entityID}`),
      )
    ) return false
    const next = new Set(pendingRef.current)
    next.add(key)
    pendingRef.current = next
    setPending(next)
    loadVersion.current += 1
    setLoading(false)
    return true
  }

  function endOperation(key: string) {
    const next = new Set(pendingRef.current)
    next.delete(key)
    pendingRef.current = next
    setPending(next)
  }

  function beginSaving(): boolean {
    if (savingRef.current) return false
    savingRef.current = true
    loadVersion.current += 1
    setLoading(false)
    setSaving(true)
    return true
  }

  function endSaving() {
    savingRef.current = false
    setSaving(false)
  }

  function entityOperationPending(namespace: 'subscription' | 'source', id: string): boolean {
    return Array.from(pending).some(
      (key) => key.startsWith(`${namespace}-`) && key.endsWith(`:${id}`),
    )
  }

  function openCreateSubscription() {
    setEditorError('')
    setSubEditor({ ...EMPTY_SUB_EDITOR, open: true, inboundIds: new Set(), sourceIds: new Set() })
  }

  function openEditSubscription(subscription: Subscription) {
    setEditorError('')
    const includeAll = subscription.include_all_inbounds ?? subscription.inbound_ids.length === 0
    setSubEditor({
      open: true,
      id: subscription.id,
      name: subscription.name,
      format: subscription.format === 'singbox' ? 'singbox' : 'clash',
      enabled: subscription.enabled,
      localMode: includeAll ? 'all' : subscription.inbound_ids.length ? 'custom' : 'none',
      inboundIds: new Set(subscription.inbound_ids ?? []),
      sourceIds: new Set(subscription.external_source_ids ?? []),
    })
  }

  function closeSubscriptionEditor() {
    if (!saving) setSubEditor({ ...EMPTY_SUB_EDITOR, inboundIds: new Set(), sourceIds: new Set() })
  }

  function toggleEditorInbound(id: string) {
    setSubEditor((current) => ({ ...current, inboundIds: toggleSet(current.inboundIds, id) }))
  }

  function toggleEditorSource(id: string) {
    setSubEditor((current) => ({ ...current, sourceIds: toggleSet(current.sourceIds, id) }))
  }

  async function saveSubscription() {
    if (savingRef.current) return
    const name = subEditor.name.trim()
    if (!name) {
      setEditorError('请填写订阅名称')
      return
    }
    if (
      subEditor.localMode === 'none' &&
      subEditor.sourceIds.size === 0
    ) {
      setEditorError('至少选择一个本地入站范围或外部源')
      return
    }
    if (
      subEditor.localMode === 'custom' &&
      subEditor.inboundIds.size === 0
    ) {
      setEditorError('请至少选择一个指定入站；仅使用外部源时请选择“不包含本地”')
      return
    }

    if (!beginSaving()) return
    setEditorError('')
    const body = {
      name,
      format: subEditor.format,
      enabled: subEditor.enabled,
      include_all_inbounds: subEditor.localMode === 'all',
      inbound_ids:
        subEditor.localMode === 'custom' ? Array.from(subEditor.inboundIds) : [],
      external_source_ids: Array.from(subEditor.sourceIds),
    }
    try {
      const saved = subEditor.id
        ? await updateSubscription(subEditor.id, body)
        : await createSubscription(body)
      setSubscriptions((current) =>
        subEditor.id
          ? current.map((item) => (item.id === saved.id ? saved : item))
          : [...current, saved],
      )
      toast.success(subEditor.id ? '订阅已更新' : '订阅已创建')
      setSubEditor({ ...EMPTY_SUB_EDITOR, inboundIds: new Set(), sourceIds: new Set() })
    } catch (err) {
      setEditorError(errorText(err, '保存订阅失败'))
    } finally {
      endSaving()
    }
  }

  async function toggleSubscription(subscription: Subscription) {
    const key = `subscription-toggle:${subscription.id}`
    if (!beginOperation(key)) return
    try {
      const updated = await updateSubscription(subscription.id, { enabled: !subscription.enabled })
      setSubscriptions((current) => current.map((item) => (item.id === updated.id ? updated : item)))
      toast.success(updated.enabled ? '订阅已启用' : '订阅已禁用')
    } catch (err) {
      toast.error(errorText(err, '切换订阅状态失败'))
    } finally {
      endOperation(key)
    }
  }

  async function rotateSubscriptionToken(subscription: Subscription) {
    const confirmRotate = window.confirm(`确定重置「${subscription.name}」的订阅 Token 吗？重置后旧链接将失效。`)
    if (!confirmRotate) return
    const key = `subscription-rotate:${subscription.id}`
    if (!beginOperation(key)) return
    try {
      const updated = await updateSubscription(subscription.id, { rotate_token: true })
      setSubscriptions((current) => current.map((item) => (item.id === updated.id ? updated : item)))
      toast.success('Token 已重置，旧链接已失效')
    } catch (err) {
      toast.error(errorText(err, '重置 Token 失败'))
    } finally {
      endOperation(key)
    }
  }

  async function onDeleteSubscription(subscription: Subscription) {
    const confirmDelete = window.confirm(`确定删除订阅「${subscription.name}」吗？此操作无法撤销。`)
    if (!confirmDelete) return
    const key = `subscription-delete:${subscription.id}`
    if (!beginOperation(key)) return
    try {
      await deleteSubscription(subscription.id)
      setSubscriptions((current) => current.filter((item) => item.id !== subscription.id))
      toast.success('订阅已删除')
    } catch (err) {
      toast.error(errorText(err, '删除订阅失败'))
    } finally {
      endOperation(key)
    }
  }

  async function onPreviewSubscription(subscription: Subscription) {
    const key = `subscription-preview:${subscription.id}`
    if (!beginOperation(key)) return
    try {
      const res = await previewSubscription(subscription.id)
      setTextPreview({
        title: `订阅内容预览 · ${subscription.name} (${subscription.format})`,
        text: res,
      })
    } catch (err) {
      toast.error(errorText(err, '预览订阅失败'))
    } finally {
      endOperation(key)
    }
  }

  function openCreateSource() {
    setEditorError('')
    setSourceEditor({ ...EMPTY_SOURCE_EDITOR, open: true })
  }

  function openEditSource(source: ExternalSource) {
    setEditorError('')
    setSourceEditor({
      open: true,
      id: source.id,
      name: source.name,
      url: source.url,
      interval: source.refresh_interval_sec,
      headers: formatHeadersObject(source.headers),
      enabled: source.enabled,
    })
  }

  function closeSourceEditor() {
    if (!saving) setSourceEditor(EMPTY_SOURCE_EDITOR)
  }

  async function saveSource() {
    if (savingRef.current) return
    const name = sourceEditor.name.trim()
    const url = sourceEditor.url.trim()
    if (!name || !url) {
      setEditorError('请填写名称与源 URL')
      return
    }
    const interval = Number(sourceEditor.interval)
    if (Number.isNaN(interval) || interval <= 0) {
      setEditorError('刷新间隔必须为大于 0 的数字（秒）')
      return
    }
    let headers: Record<string, string> = {}
    try {
      headers = parseHeadersText(sourceEditor.headers)
    } catch (err) {
      setEditorError(err instanceof Error ? err.message : 'HTTP 请求头格式不正确')
      return
    }

    if (!beginSaving()) return
    setEditorError('')
    const body = {
      name,
      url,
      interval_seconds: Math.floor(interval),
      headers,
      enabled: sourceEditor.enabled,
    }
    try {
      const saved = sourceEditor.id
        ? await updateExternalSource(sourceEditor.id, body)
        : await createExternalSource(body)
      setSources((current) =>
        sourceEditor.id
          ? current.map((item) => (item.id === saved.id ? saved : item))
          : [...current, saved],
      )
      toast.success(sourceEditor.id ? '外部源已更新' : '外部源已创建')
      setSourceEditor(EMPTY_SOURCE_EDITOR)
    } catch (err) {
      setEditorError(errorText(err, '保存外部源失败'))
    } finally {
      endSaving()
    }
  }

  async function onRefreshSource(source: ExternalSource) {
    const key = `source-refresh:${source.id}`
    if (!beginOperation(key)) return
    try {
      const updated = await refreshExternalSource(source.id)
      setSources((current) => current.map((item) => (item.id === updated.id ? updated : item)))
      toast.success(`外部源「${source.name}」已刷新，发现 ${updated.cached_proxy_count} 个节点`)
    } catch (err) {
      toast.error(errorText(err, '刷新外部源失败'))
    } finally {
      endOperation(key)
    }
  }

  async function onPreviewSource(source: ExternalSource) {
    const key = `source-preview:${source.id}`
    if (!beginOperation(key)) return
    try {
      const res = await previewExternalSource(source.id)
      setSourcePreview(res)
      setSourcePreviewTitle(`外部源解析预览 · ${source.name}`)
    } catch (err) {
      toast.error(errorText(err, '预览外部源失败'))
    } finally {
      endOperation(key)
    }
  }

  async function onDeleteSource(source: ExternalSource) {
    const confirmDelete = window.confirm(`确定删除外部源「${source.name}」吗？此操作无法撤销。`)
    if (!confirmDelete) return
    const key = `source-delete:${source.id}`
    if (!beginOperation(key)) return
    try {
      await deleteExternalSource(source.id)
      setSources((current) => current.filter((item) => item.id !== source.id))
      toast.success('外部源已删除')
    } catch (err) {
      toast.error(errorText(err, '删除外部源失败'))
    } finally {
      endOperation(key)
    }
  }

  function getFullSubscriptionUrl(path: string | undefined): string {
    if (!path) return ''
    if (path.startsWith('http://') || path.startsWith('https://')) return path
    const origin = typeof window !== 'undefined' ? window.location.origin : ''
    return `${origin}${path.startsWith('/') ? '' : '/'}${path}`
  }

  async function copySubscriptionUrl(path: string | undefined, id: string) {
    const fullUrl = getFullSubscriptionUrl(path)
    try {
      await copyText(fullUrl)
      setCopiedId(id)
      toast.success('已复制完整订阅链接')
      setTimeout(() => setCopiedId(null), 2000)
    } catch {
      toast.error('复制失败')
    }
  }

  return (
    <div className="space-y-6">
      {/* Header Title Section */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-zinc-100 flex items-center gap-2.5">
            <Rss className="h-6 w-6 text-cyan-400" />
            订阅配置管理
          </h1>
          <p className="text-sm text-zinc-400 mt-1 leading-normal">
            发布面向客户端的代理订阅分发链接 · 聚合与解析第三方外部订阅源
          </p>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <Button
            variant="outline"
            onClick={() => void load()}
            loading={loading}
            disabled={saving || pending.size > 0}
            className="border-zinc-800 text-zinc-300 hover:bg-zinc-900 gap-1.5 h-9"
          >
            <RefreshCw className="h-4 w-4" /> 刷新
          </Button>
        </div>
      </div>

      {loadError && (
        <Alert variant="destructive">
          <AlertTitle>数据加载遇到问题</AlertTitle>
          <AlertDescription>{loadError}</AlertDescription>
        </Alert>
      )}

      {/* Primary Tabs */}
      <Tabs defaultValue="publish" className="w-full space-y-6">
        <div className="border-b border-zinc-900 pb-2">
          <TabsList className="bg-zinc-900/60 p-1 border border-zinc-800/80 rounded-lg">
            <TabsTrigger
              value="publish"
              className="gap-2 text-xs font-semibold px-4 py-2 cursor-pointer data-[state=active]:bg-zinc-800 data-[state=active]:text-zinc-100"
            >
              <Radio className="h-4 w-4 text-cyan-400" />
              发布订阅 ({subscriptions.length})
            </TabsTrigger>
            <TabsTrigger
              value="sources"
              className="gap-2 text-xs font-semibold px-4 py-2 cursor-pointer data-[state=active]:bg-zinc-800 data-[state=active]:text-zinc-100"
            >
              <Globe className="h-4 w-4 text-violet-400" />
              外部订阅源 ({sources.length})
            </TabsTrigger>
          </TabsList>
        </div>

        {/* Tab 1: Publish Subscriptions */}
        <TabsContent value="publish" className="space-y-6 mt-0">
          <div className="flex items-center justify-between">
            <div className="space-y-1">
              <h2 className="text-sm font-semibold text-zinc-200">面向客户端的公网订阅分发</h2>
              <p className="text-xs text-zinc-500">
                支持编译出标准的 Clash (YAML) 与 sing-box (JSON) 订阅。客户端发起请求时动态包含在线节点。
              </p>
            </div>
            <Button onClick={openCreateSubscription} className="gap-1.5 h-9">
              <Plus className="h-4 w-4" /> 新建订阅
            </Button>
          </div>

          {loading && subscriptions.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 space-y-3">
              <RefreshCw className="h-6 w-6 animate-spin text-zinc-500" />
              <span className="text-sm text-zinc-400">正在加载订阅分发列表…</span>
            </div>
          ) : subscriptions.length === 0 ? (
            <Card className="border-zinc-900 bg-zinc-900/20 py-12 text-center">
              <CardContent className="space-y-3">
                <Rss className="h-10 w-10 text-zinc-700 mx-auto" />
                <div className="space-y-1">
                  <h3 className="text-base font-semibold text-zinc-300">暂无已发布的订阅</h3>
                  <p className="text-xs text-zinc-500 max-w-sm mx-auto">
                    点击右上角“新建订阅”按钮，创建一个支持 Clash 或 sing-box 的公网订阅链接。
                  </p>
                </div>
                <Button onClick={openCreateSubscription} className="gap-1.5 mt-2">
                  <Plus className="h-4 w-4" /> 创建第一个订阅
                </Button>
              </CardContent>
            </Card>
          ) : (
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-5">
              {subscriptions.map((sub) => {
                const subPending = entityOperationPending('subscription', sub.id)
                const isClash = sub.format === 'clash'
                const fullUrl = getFullSubscriptionUrl(sub.url)
                const clashScheme = `clash://install-config?url=${encodeURIComponent(fullUrl)}`
                const singboxScheme = `sing-box://import-remote?url=${encodeURIComponent(fullUrl)}`

                const themeGlow = isClash
                  ? 'border-cyan-950/50 bg-cyan-950/10 shadow-cyan-950/20'
                  : 'border-violet-950/50 bg-violet-950/10 shadow-violet-950/20'

                return (
                  <Card
                    key={sub.id}
                    className={`relative overflow-hidden transition-all duration-300 hover:translate-y-[-2px] ${themeGlow} border`}
                  >
                    <CardHeader className="p-5 pb-3">
                      <div className="flex items-start justify-between gap-3">
                        <div className="space-y-1">
                          <div className="flex items-center gap-2.5">
                            <CardTitle className="text-base font-bold text-zinc-100">
                              {sub.name}
                            </CardTitle>
                            <Badge
                              variant="outline"
                              className={
                                isClash
                                  ? 'border-cyan-800/80 bg-cyan-950/60 text-cyan-300 font-mono text-[11px]'
                                  : 'border-violet-800/80 bg-violet-950/60 text-violet-300 font-mono text-[11px]'
                              }
                            >
                              {isClash ? 'Clash (YAML)' : 'sing-box (JSON)'}
                            </Badge>
                          </div>
                        </div>

                        <div className="flex items-center gap-2 shrink-0">
                          <Switch
                            checked={sub.enabled}
                            disabled={subPending || saving}
                            onCheckedChange={() => void toggleSubscription(sub)}
                          />
                        </div>
                      </div>
                    </CardHeader>

                    <CardContent className="p-5 pt-0 space-y-4">
                      {/* Subscription URL Box */}
                      <div className="space-y-1.5">
                        <span className="text-[11px] font-medium text-zinc-400 block">公网订阅地址</span>
                        <div className="flex items-center gap-2 p-2 rounded-lg bg-zinc-950 border border-zinc-900">
                          <code className="text-xs font-mono text-zinc-300 truncate flex-1 select-all px-1">
                            {fullUrl}
                          </code>
                          <TooltipProvider>
                            <Tooltip>
                              <TooltipTrigger asChild>
                                <Button
                                  size="sm"
                                  variant="ghost"
                                  onClick={() => void copySubscriptionUrl(sub.url, sub.id)}
                                  className="h-7 px-2.5 text-xs text-zinc-300 hover:text-zinc-100 hover:bg-zinc-900 shrink-0 cursor-pointer"
                                >
                                  {copiedId === sub.id ? (
                                    <Check className="h-3.5 w-3.5 text-emerald-500" />
                                  ) : (
                                    <Copy className="h-3.5 w-3.5" />
                                  )}
                                  <span className="ml-1.5">{copiedId === sub.id ? '已复制' : '复制'}</span>
                                </Button>
                              </TooltipTrigger>
                              <TooltipContent>复制完整公网订阅 URL</TooltipContent>
                            </Tooltip>
                          </TooltipProvider>
                        </div>
                      </div>

                      {/* Client Quick Action Bar */}
                      <div className="flex flex-wrap items-center gap-2 pt-1">
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => setQrModal({ open: true, title: sub.name, url: fullUrl })}
                          className="h-8 text-xs border-zinc-800 bg-zinc-900/50 hover:bg-zinc-800 text-zinc-200 gap-1.5 cursor-pointer"
                        >
                          <QrCode className="h-3.5 w-3.5 text-cyan-400" /> 扫码
                        </Button>

                        <a
                          href={isClash ? clashScheme : singboxScheme}
                          target="_blank"
                          rel="noreferrer"
                          className="inline-flex items-center justify-center rounded-md text-xs font-medium border border-zinc-800 bg-zinc-900/50 hover:bg-zinc-800 text-zinc-200 h-8 px-3 transition-colors gap-1.5 cursor-pointer"
                        >
                          <ExternalLink className="h-3.5 w-3.5 text-emerald-400" />
                          一键导入 {isClash ? 'Clash' : 'sing-box'}
                        </a>

                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => void onPreviewSubscription(sub)}
                          loading={subPending}
                          disabled={subPending}
                          className="h-8 text-xs text-zinc-400 hover:text-zinc-200 gap-1.5 ml-auto cursor-pointer"
                        >
                          <Eye className="h-3.5 w-3.5" /> 预览结构
                        </Button>
                      </div>

                      {/* Content Aggregation Summary */}
                      <div className="grid grid-cols-2 gap-2 pt-3 border-t border-zinc-900/80 text-xs">
                        <div className="space-y-0.5">
                          <span className="text-[10px] text-zinc-500 block">本地节点范围</span>
                          <span className="text-zinc-300 font-medium flex items-center gap-1">
                            <Server className="h-3 w-3 text-zinc-500" />
                            {sub.include_all_inbounds
                              ? `全网可接入 (${inbounds.length} 个)`
                              : (sub.inbound_ids?.length ?? 0) > 0
                                ? `指定 ${sub.inbound_ids.length} 个入站`
                                : '不包含本地'}
                          </span>
                        </div>
                        <div className="space-y-0.5">
                          <span className="text-[10px] text-zinc-500 block">关联外部订阅源</span>
                          <span className="text-zinc-300 font-medium flex items-center gap-1">
                            <Layers className="h-3 w-3 text-zinc-500" />
                            {(sub.external_source_ids?.length ?? 0) > 0
                              ? `包含 ${sub.external_source_ids?.length ?? 0} 个外部源`
                              : '仅本地节点'}
                          </span>
                        </div>
                      </div>

                      {/* Footer Actions */}
                      <div className="flex items-center justify-between pt-3 border-t border-zinc-900/80 text-xs text-zinc-400">
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => void rotateSubscriptionToken(sub)}
                          loading={subPending}
                          disabled={subPending}
                          className="h-7 text-xs text-zinc-500 hover:text-amber-400 px-2 cursor-pointer gap-1"
                        >
                          <RotateCw className="h-3 w-3" /> 重置 Token
                        </Button>

                        <div className="flex items-center gap-1">
                          <Button
                            size="sm"
                            variant="ghost"
                            onClick={() => openEditSubscription(sub)}
                            disabled={subPending}
                            className="h-7 text-xs text-zinc-400 hover:text-zinc-200 px-2.5 cursor-pointer gap-1"
                          >
                            <Edit className="h-3.5 w-3.5" /> 编辑
                          </Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            onClick={() => void onDeleteSubscription(sub)}
                            disabled={subPending}
                            className="h-7 text-xs text-red-500 hover:text-red-400 hover:bg-red-950/20 px-2.5 cursor-pointer gap-1"
                          >
                            <Trash2 className="h-3.5 w-3.5" /> 删除
                          </Button>
                        </div>
                      </div>
                    </CardContent>
                  </Card>
                )
              })}
            </div>
          )}
        </TabsContent>

        {/* Tab 2: External Sources */}
        <TabsContent value="sources" className="space-y-6 mt-0">
          <div className="flex items-center justify-between">
            <div className="space-y-1">
              <h2 className="text-sm font-semibold text-zinc-200">第三方外部订阅源聚合</h2>
              <p className="text-xs text-zinc-500">
                配置外部机场或提供者的订阅 URL，系统后台会定时抓取并解析节点，合并下发给指定订阅。
              </p>
            </div>
            <Button onClick={openCreateSource} className="gap-1.5 h-9">
              <Plus className="h-4 w-4" /> 添加外部源
            </Button>
          </div>

          {loading && sources.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 space-y-3">
              <RefreshCw className="h-6 w-6 animate-spin text-zinc-500" />
              <span className="text-sm text-zinc-400">正在加载外部订阅源列表…</span>
            </div>
          ) : sources.length === 0 ? (
            <Card className="border-zinc-900 bg-zinc-900/20 py-12 text-center">
              <CardContent className="space-y-3">
                <Globe className="h-10 w-10 text-zinc-700 mx-auto" />
                <div className="space-y-1">
                  <h3 className="text-base font-semibold text-zinc-300">暂无第三方外部订阅源</h3>
                  <p className="text-xs text-zinc-500 max-w-sm mx-auto">
                    添加外部订阅源链接后，面板可以自动抓取并将其中的节点合并到您发布的订阅中。
                  </p>
                </div>
                <Button onClick={openCreateSource} className="gap-1.5 mt-2">
                  <Plus className="h-4 w-4" /> 添加第一个外部源
                </Button>
              </CardContent>
            </Card>
          ) : (
            <Card className="border-zinc-900 bg-zinc-900/30">
              <CardContent className="p-0">
                <div className="overflow-x-auto">
                  <Table>
                    <TableHeader className="bg-zinc-900/40 border-zinc-800">
                      <TableRow className="hover:bg-transparent">
                        <TableHead className="min-w-[140px]">源名称</TableHead>
                        <TableHead className="min-w-[220px]">抓取 URL</TableHead>
                        <TableHead className="w-24 text-center">解析节点数</TableHead>
                        <TableHead className="w-28 text-center">刷新间隔</TableHead>
                        <TableHead className="w-32">最后更新</TableHead>
                        <TableHead className="w-20">状态</TableHead>
                        <TableHead className="w-[200px] text-right">操作</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody className="divide-y divide-zinc-900 border-zinc-900">
                      {sources.map((src) => {
                        const srcPending = entityOperationPending('source', src.id)
                        return (
                          <TableRow key={src.id} className="hover:bg-zinc-900/30 border-zinc-900/60">
                            <TableCell className="font-semibold text-zinc-200">
                              {src.name}
                            </TableCell>
                            <TableCell>
                              <code className="text-xs font-mono text-zinc-400 break-all block max-w-xs truncate">
                                {src.url}
                              </code>
                              {src.last_error && (
                                <span className="text-[10px] font-mono text-red-400 block truncate mt-0.5">
                                  抓取失败: {src.last_error}
                                </span>
                              )}
                            </TableCell>
                            <TableCell className="text-center font-mono font-bold text-zinc-200">
                              {src.cached_proxy_count ?? 0}
                            </TableCell>
                            <TableCell className="text-center font-mono text-xs text-zinc-400">
                              {formatInterval(src.refresh_interval_sec)}
                            </TableCell>
                            <TableCell className="text-xs text-zinc-400 font-mono">
                              {formatTime(src.updated_at_unix)}
                            </TableCell>
                            <TableCell>
                              <Badge
                                variant={src.enabled ? 'success' : 'secondary'}
                                className="text-[10px]"
                              >
                                {src.enabled ? '已启用' : '已禁用'}
                              </Badge>
                            </TableCell>
                            <TableCell className="text-right">
                              <div className="flex items-center justify-end gap-1">
                                <Button
                                  size="sm"
                                  variant="ghost"
                                  loading={srcPending}
                                  disabled={srcPending}
                                  onClick={() => void onRefreshSource(src)}
                                  className="h-8 px-2 text-xs text-cyan-400 hover:text-cyan-300 hover:bg-cyan-950/20 cursor-pointer gap-1"
                                >
                                  <RefreshCw className="h-3.5 w-3.5" /> 刷新
                                </Button>
                                <Button
                                  size="sm"
                                  variant="ghost"
                                  loading={srcPending}
                                  disabled={srcPending}
                                  onClick={() => void onPreviewSource(src)}
                                  className="h-8 px-2 text-xs text-zinc-400 hover:text-zinc-200 cursor-pointer gap-1"
                                >
                                  <Eye className="h-3.5 w-3.5" /> 预览
                                </Button>
                                <Button
                                  size="sm"
                                  variant="ghost"
                                  disabled={srcPending}
                                  onClick={() => openEditSource(src)}
                                  className="h-8 px-2 text-xs text-zinc-400 hover:text-zinc-200 cursor-pointer gap-1"
                                >
                                  <Edit className="h-3.5 w-3.5" /> 编辑
                                </Button>
                                <Button
                                  size="sm"
                                  variant="ghost"
                                  disabled={srcPending}
                                  onClick={() => void onDeleteSource(src)}
                                  className="h-8 px-2 text-xs text-red-500 hover:text-red-400 hover:bg-red-950/20 cursor-pointer gap-1"
                                >
                                  <Trash2 className="h-3.5 w-3.5" /> 删除
                                </Button>
                              </div>
                            </TableCell>
                          </TableRow>
                        )
                      })}
                    </TableBody>
                  </Table>
                </div>
              </CardContent>
            </Card>
          )}
        </TabsContent>
      </Tabs>

      {/* Subscription Editor Dialog */}
      <Dialog open={subEditor.open} onOpenChange={(v) => !v && closeSubscriptionEditor()}>
        <DialogContent className="sm:max-w-lg bg-zinc-950 border-zinc-900 text-zinc-100 p-6 space-y-5">
          <DialogHeader className="space-y-1">
            <DialogTitle className="text-lg font-bold flex items-center gap-2">
              <Rss className="h-5 w-5 text-cyan-400" />
              {subEditor.id ? '编辑订阅设置' : '创建新订阅'}
            </DialogTitle>
            <p className="text-xs text-zinc-400">
              面向客户端生成标准 Clash (YAML) 或 sing-box (JSON) 订阅配置链接。
            </p>
          </DialogHeader>

          {editorError && (
            <Alert variant="destructive">
              <AlertDescription>{editorError}</AlertDescription>
            </Alert>
          )}

          <div className="space-y-4">
            <div className="space-y-1.5">
              <Label className="text-xs text-zinc-300">订阅名称</Label>
              <Input
                value={subEditor.name}
                onChange={(e) => setSubEditor({ ...subEditor, name: e.target.value })}
                placeholder="例：常用客户端订阅 / Clash 节点池"
                className="bg-zinc-900 border-zinc-800 text-sm"
              />
            </div>

            <div className="space-y-1.5">
              <Label className="text-xs text-zinc-300">客户端文件格式</Label>
              <div className="grid grid-cols-2 gap-3">
                <button
                  type="button"
                  onClick={() => setSubEditor({ ...subEditor, format: 'clash' })}
                  className={`p-3 rounded-lg border text-left transition-all cursor-pointer ${
                    subEditor.format === 'clash'
                      ? 'border-cyan-600 bg-cyan-950/40 text-cyan-200'
                      : 'border-zinc-800 bg-zinc-900/50 text-zinc-400 hover:border-zinc-700'
                  }`}
                >
                  <div className="font-bold text-sm">Clash 格式</div>
                  <div className="text-[11px] opacity-80 mt-0.5">标准 YAML 节点列表模版</div>
                </button>

                <button
                  type="button"
                  onClick={() => setSubEditor({ ...subEditor, format: 'singbox' })}
                  className={`p-3 rounded-lg border text-left transition-all cursor-pointer ${
                    subEditor.format === 'singbox'
                      ? 'border-violet-600 bg-violet-950/40 text-violet-200'
                      : 'border-zinc-800 bg-zinc-900/50 text-zinc-400 hover:border-zinc-700'
                  }`}
                >
                  <div className="font-bold text-sm">sing-box 格式</div>
                  <div className="text-[11px] opacity-80 mt-0.5">标准 JSON Outbounds 配置模版</div>
                </button>
              </div>
            </div>

            <div className="space-y-2 pt-2 border-t border-zinc-900">
              <Label className="text-xs text-zinc-300 block">包含本地节点范围</Label>
              <div className="grid grid-cols-3 gap-2">
                <button
                  type="button"
                  onClick={() => setSubEditor({ ...subEditor, localMode: 'all' })}
                  className={`p-2 text-center rounded border text-xs font-medium transition-all cursor-pointer ${
                    subEditor.localMode === 'all'
                      ? 'border-zinc-600 bg-zinc-800 text-zinc-100'
                      : 'border-zinc-800 bg-zinc-900/40 text-zinc-400'
                  }`}
                >
                  包含全部节点
                </button>
                <button
                  type="button"
                  onClick={() => setSubEditor({ ...subEditor, localMode: 'custom' })}
                  className={`p-2 text-center rounded border text-xs font-medium transition-all cursor-pointer ${
                    subEditor.localMode === 'custom'
                      ? 'border-zinc-600 bg-zinc-800 text-zinc-100'
                      : 'border-zinc-800 bg-zinc-900/40 text-zinc-400'
                  }`}
                >
                  选择指定节点
                </button>
                <button
                  type="button"
                  onClick={() => setSubEditor({ ...subEditor, localMode: 'none' })}
                  className={`p-2 text-center rounded border text-xs font-medium transition-all cursor-pointer ${
                    subEditor.localMode === 'none'
                      ? 'border-zinc-600 bg-zinc-800 text-zinc-100'
                      : 'border-zinc-800 bg-zinc-900/40 text-zinc-400'
                  }`}
                >
                  不包含本地节点
                </button>
              </div>

              {subEditor.localMode === 'custom' && (
                <div className="p-3 bg-zinc-900/60 rounded-lg border border-zinc-800 max-h-40 overflow-y-auto space-y-2 mt-2">
                  {inbounds.length === 0 ? (
                    <div className="text-xs text-zinc-500 text-center py-2">暂无可用入站</div>
                  ) : (
                    inbounds.map((ib) => (
                      <div key={ib.id} className="flex items-center space-x-2 text-xs">
                        <Checkbox
                          id={`ib-${ib.id}`}
                          checked={subEditor.inboundIds.has(ib.id)}
                          onCheckedChange={() => toggleEditorInbound(ib.id)}
                        />
                        <label htmlFor={`ib-${ib.id}`} className="text-zinc-300 font-mono cursor-pointer">
                          {ib.name} ({ib.protocol})
                        </label>
                      </div>
                    ))
                  )}
                </div>
              )}
            </div>

            <div className="space-y-2 pt-2 border-t border-zinc-900">
              <Label className="text-xs text-zinc-300 block">合并第三方外部订阅源</Label>
              {sources.length === 0 ? (
                <p className="text-xs text-zinc-500 italic">暂无已启用的外部订阅源</p>
              ) : (
                <div className="p-3 bg-zinc-900/60 rounded-lg border border-zinc-800 max-h-40 overflow-y-auto space-y-2">
                  {sources.map((src) => (
                    <div key={src.id} className="flex items-center space-x-2 text-xs">
                      <Checkbox
                        id={`src-${src.id}`}
                        checked={subEditor.sourceIds.has(src.id)}
                        onCheckedChange={() => toggleEditorSource(src.id)}
                      />
                      <label htmlFor={`src-${src.id}`} className="text-zinc-300 cursor-pointer">
                        {src.name} <span className="text-zinc-500 font-mono text-[10px]">({src.cached_proxy_count} 个节点)</span>
                      </label>
                    </div>
                  ))}
                </div>
              )}
            </div>

            <div className="flex items-center justify-between pt-2 border-t border-zinc-900">
              <Label className="text-xs text-zinc-300">启用该订阅分发</Label>
              <Switch
                checked={subEditor.enabled}
                onCheckedChange={(v) => setSubEditor({ ...subEditor, enabled: v })}
              />
            </div>
          </div>

          <DialogFooter className="gap-2 pt-2">
            <Button variant="outline" onClick={closeSubscriptionEditor} disabled={saving}>
              取消
            </Button>
            <Button onClick={() => void saveSubscription()} loading={saving}>
              {subEditor.id ? '更新订阅' : '创建订阅'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* External Source Editor Dialog */}
      <Dialog open={sourceEditor.open} onOpenChange={(v) => !v && closeSourceEditor()}>
        <DialogContent className="sm:max-w-lg bg-zinc-950 border-zinc-900 text-zinc-100 p-6 space-y-5">
          <DialogHeader className="space-y-1">
            <DialogTitle className="text-lg font-bold flex items-center gap-2">
              <Globe className="h-5 w-5 text-violet-400" />
              {sourceEditor.id ? '编辑外部订阅源' : '添加外部订阅源'}
            </DialogTitle>
            <p className="text-xs text-zinc-400">
              抓取并解析外部 HTTP/HTTPS 机场订阅链接，将其中的节点合并入本系统的订阅分发中。
            </p>
          </DialogHeader>

          {editorError && (
            <Alert variant="destructive">
              <AlertDescription>{editorError}</AlertDescription>
            </Alert>
          )}

          <div className="space-y-4">
            <div className="space-y-1.5">
              <Label className="text-xs text-zinc-300">源名称</Label>
              <Input
                value={sourceEditor.name}
                onChange={(e) => setSourceEditor({ ...sourceEditor, name: e.target.value })}
                placeholder="例：香港备用节点池 / 外部机场"
                className="bg-zinc-900 border-zinc-800 text-sm"
              />
            </div>

            <div className="space-y-1.5">
              <Label className="text-xs text-zinc-300">抓取订阅 URL</Label>
              <Input
                value={sourceEditor.url}
                onChange={(e) => setSourceEditor({ ...sourceEditor, url: e.target.value })}
                placeholder="https://example.com/api/v1/client/subscribe?token=..."
                className="bg-zinc-900 border-zinc-800 text-sm font-mono"
              />
            </div>

            <div className="space-y-1.5">
              <Label className="text-xs text-zinc-300">后台自动刷新间隔 (秒)</Label>
              <Input
                type="number"
                value={sourceEditor.interval}
                onChange={(e) => setSourceEditor({ ...sourceEditor, interval: e.target.value })}
                placeholder="86400 (默认 24 小时)"
                className="bg-zinc-900 border-zinc-800 text-sm font-mono"
              />
              <span className="text-[10px] text-zinc-500 block">
                常用提示：86400 秒 = 24 小时，43200 秒 = 12 小时。
              </span>
            </div>

            <div className="space-y-1.5">
              <Label className="text-xs text-zinc-300">自定义 HTTP 请求头 (可选，每行 Key: Value)</Label>
              <textarea
                value={sourceEditor.headers}
                onChange={(e) => setSourceEditor({ ...sourceEditor, headers: e.target.value })}
                rows={3}
                placeholder="User-Agent: ClashMeta&#10;Authorization: Bearer xyz"
                className="w-full rounded-md bg-zinc-900 border border-zinc-800 p-2.5 text-xs font-mono text-zinc-200 focus:outline-none focus:ring-1 focus:ring-zinc-700"
              />
            </div>

            <div className="flex items-center justify-between pt-2 border-t border-zinc-900">
              <Label className="text-xs text-zinc-300">启用自动抓取</Label>
              <Switch
                checked={sourceEditor.enabled}
                onCheckedChange={(v) => setSourceEditor({ ...sourceEditor, enabled: v })}
              />
            </div>
          </div>

          <DialogFooter className="gap-2 pt-2">
            <Button variant="outline" onClick={closeSourceEditor} disabled={saving}>
              取消
            </Button>
            <Button onClick={() => void saveSource()} loading={saving}>
              {sourceEditor.id ? '更新外部源' : '创建外部源'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* QR Code Modal */}
      <QRCodeModal
        open={qrModal.open}
        onClose={() => setQrModal({ open: false, title: '', url: '' })}
        title={qrModal.title}
        url={qrModal.url}
      />

      {/* Text Preview Modal */}
      <Dialog open={textPreview !== null} onOpenChange={(v) => !v && setTextPreview(null)}>
        <DialogContent className="sm:max-w-2xl bg-zinc-950 border-zinc-900 text-zinc-100 p-6 space-y-4">
          <DialogHeader className="space-y-1">
            <DialogTitle className="text-base font-bold text-zinc-100">
              {textPreview?.title}
            </DialogTitle>
          </DialogHeader>

          <div className="relative">
            <pre className="p-4 bg-zinc-900 border border-zinc-800 rounded-lg max-h-[60vh] overflow-y-auto text-xs font-mono text-zinc-300 whitespace-pre-wrap break-all leading-relaxed">
              {textPreview?.text}
            </pre>
          </div>

          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                if (textPreview) {
                  void copyText(textPreview.text)
                  toast.success('已复制预览内容')
                }
              }}
            >
              <Copy className="h-4 w-4 mr-2" /> 复制全部
            </Button>
            <Button onClick={() => setTextPreview(null)}>关闭</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Source Preview Modal */}
      <Dialog open={sourcePreview !== null} onOpenChange={(v) => !v && setSourcePreview(null)}>
        <DialogContent className="sm:max-w-2xl bg-zinc-950 border-zinc-900 text-zinc-100 p-6 space-y-4">
          <DialogHeader className="space-y-1">
            <DialogTitle className="text-base font-bold text-zinc-100">
              {sourcePreviewTitle}
            </DialogTitle>
          </DialogHeader>

          {sourcePreview && (
            <div className="space-y-3">
              <div className="text-xs text-zinc-400 font-mono">
                此外部源成功解析出 <strong className="text-cyan-400">{sourcePreview.count}</strong> 个节点。
              </div>

              <div className="p-3 bg-zinc-900 border border-zinc-800 rounded-lg max-h-[50vh] overflow-y-auto space-y-2">
                {sourcePreview.names.map((name, idx) => (
                  <div key={idx} className="p-2 rounded bg-zinc-950 border border-zinc-900 text-xs flex items-center justify-between font-mono">
                    <span className="font-bold text-zinc-200">{name}</span>
                    <Badge variant="outline" className="text-[10px] border-zinc-800 text-zinc-400">
                      外部节点
                    </Badge>
                  </div>
                ))}
              </div>
            </div>
          )}

          <DialogFooter>
            <Button onClick={() => setSourcePreview(null)}>关闭</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

function toggleSet<T>(set: Set<T>, item: T): Set<T> {
  const next = new Set(set)
  if (next.has(item)) next.delete(item)
  else next.add(item)
  return next
}

function formatHeadersObject(headers: Record<string, string> | undefined): string {
  if (!headers) return ''
  return Object.entries(headers)
    .map(([k, v]) => `${k}: ${v}`)
    .join('\n')
}

function parseHeadersText(text: string): Record<string, string> {
  const out: Record<string, string> = {}
  const lines = text.split('\n')
  for (const raw of lines) {
    const line = raw.trim()
    if (!line) continue
    const idx = line.indexOf(':')
    if (idx <= 0) {
      throw new Error(`请求头格式错误 (缺少冒号)：${line}`)
    }
    const key = line.slice(0, idx).trim()
    const val = line.slice(idx + 1).trim()
    if (!key) {
      throw new Error(`请求头 Key 不能为空：${line}`)
    }
    out[key] = val
  }
  return out
}

function formatInterval(seconds: number): string {
  if (!seconds || seconds <= 0) return '—'
  if (seconds >= 86400 && seconds % 86400 === 0) {
    return `${seconds / 86400} 天`
  }
  if (seconds >= 3600 && seconds % 3600 === 0) {
    return `${seconds / 3600} 小时`
  }
  if (seconds >= 60 && seconds % 60 === 0) {
    return `${seconds / 60} 分钟`
  }
  return `${seconds} 秒`
}

function errorText(err: unknown, fallback: string): string {
  if (!err) return fallback
  if (typeof err === 'string') return err
  if (err instanceof Error) return err.message
  return fallback
}
