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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
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
  HelpCircle,
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
      setSubscriptions((current) =>
        current.map((item) => (item.id === updated.id ? updated : item)),
      )
      toast.success(updated.enabled ? '订阅已启用' : '订阅已禁用')
    } catch (err) {
      toast.error(errorText(err, '更新订阅失败'))
    } finally {
      endOperation(key)
    }
  }

  async function copySubscriptionURL(subscription: Subscription) {
    try {
      await copyText(subscription.url || '')
      setCopiedId(subscription.id)
      toast.success('订阅链接已复制')
      setTimeout(() => setCopiedId(null), 2000)
    } catch (err) {
      toast.error(errorText(err, '复制失败'))
    }
  }

  async function showSubscriptionPreview(subscription: Subscription) {
    const key = `subscription-preview:${subscription.id}`
    if (!beginOperation(key)) return
    try {
      const text = await previewSubscription(subscription.id)
      setTextPreview({ title: `订阅预览 · ${subscription.name}`, text })
    } catch (err) {
      toast.error(errorText(err, '预览失败'))
    } finally {
      endOperation(key)
    }
  }

  function rotateSubscription(subscription: Subscription) {
    const confirmRotate = window.confirm(`轮换订阅 Token\n确定要轮换订阅「${subscription.name}」的 Token 吗？轮换后旧订阅链接将立即失效，所有客户端需要重新导入链接。`)
    if (!confirmRotate) return

    const key = `subscription-rotate:${subscription.id}`
    if (!beginOperation(key)) return
    void (async () => {
      try {
        const updated = await updateSubscription(subscription.id, { rotate_token: true })
        setSubscriptions((current) =>
          current.map((item) => (item.id === updated.id ? updated : item)),
        )
        toast.success('Token 已轮换成功')
      } catch (err) {
        toast.error(errorText(err, '轮换失败'))
      } finally {
        endOperation(key)
      }
    })()
  }

  function removeSubscription(subscription: Subscription) {
    const confirmRemove = window.confirm(`删除订阅\n确定要删除订阅「${subscription.name}」吗？此操作不可逆。`)
    if (!confirmRemove) return

    const key = `subscription-delete:${subscription.id}`
    if (!beginOperation(key)) return
    void (async () => {
      try {
        await deleteSubscription(subscription.id)
        setSubscriptions((current) => current.filter((item) => item.id !== subscription.id))
        toast.success('订阅已成功删除')
      } catch (err) {
        toast.error(errorText(err, '删除订阅失败'))
      } finally {
        endOperation(key)
      }
    })()
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
      interval: source.refresh_interval_sec || 0,
      headers:
        source.headers && Object.keys(source.headers).length
          ? JSON.stringify(source.headers, null, 2)
          : '',
      enabled: source.enabled,
    })
  }

  function closeSourceEditor() {
    if (!saving) setSourceEditor({ ...EMPTY_SOURCE_EDITOR })
  }

  async function saveSource() {
    if (savingRef.current) return
    const name = sourceEditor.name.trim()
    if (!name) {
      setEditorError('请填写外部源名称')
      return
    }
    const urlError = validateSourceURL(sourceEditor.url)
    if (urlError) {
      setEditorError(urlError)
      return
    }
    const interval = Number(sourceEditor.interval)
    if (!Number.isInteger(interval) || interval < 0 || interval > 31_536_000) {
      setEditorError('刷新间隔必须是 0 到 31536000 之间的整数秒')
      return
    }
    let headers: Record<string, string>
    try {
      headers = parseHeaders(sourceEditor.headers)
    } catch (err) {
      setEditorError(errorText(err, '请求头格式错误'))
      return
    }

    if (!beginSaving()) return
    setEditorError('')
    const body = {
      name,
      url: sourceEditor.url.trim(),
      refresh_interval_sec: interval,
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
      toast.success(sourceEditor.id ? '外部源已更新' : '外部源已添加')
      setSourceEditor({ ...EMPTY_SOURCE_EDITOR })
    } catch (err) {
      setEditorError(errorText(err, '保存外部源失败'))
    } finally {
      endSaving()
    }
  }

  async function toggleSource(source: ExternalSource) {
    const key = `source-toggle:${source.id}`
    if (!beginOperation(key)) return
    try {
      const updated = await updateExternalSource(source.id, { enabled: !source.enabled })
      setSources((current) =>
        current.map((item) => (item.id === updated.id ? updated : item)),
      )
      toast.success(updated.enabled ? '外部源已启用' : '外部源已禁用')
    } catch (err) {
      toast.error(errorText(err, '更新外部源失败'))
    } finally {
      endOperation(key)
    }
  }

  async function refreshSource(source: ExternalSource) {
    const key = `source-refresh:${source.id}`
    if (!beginOperation(key)) return
    try {
      const updated = await refreshExternalSource(source.id)
      setSources((current) =>
        current.map((item) => (item.id === updated.id ? updated : item)),
      )
      toast.success(`已刷新 ${updated.cached_proxy_count ?? 0} 个节点`)
    } catch (err) {
      toast.error(errorText(err, '刷新外部源失败'))
    } finally {
      endOperation(key)
    }
  }

  async function showSourcePreview(source: ExternalSource) {
    const key = `source-preview:${source.id}`
    if (!beginOperation(key)) return
    try {
      const data = await previewExternalSource(source.id)
      setSourcePreview(data)
      setSourcePreviewTitle(`外部源预览 · ${source.name}`)
    } catch (err) {
      toast.error(errorText(err, '预览外部源失败'))
    } finally {
      endOperation(key)
    }
  }

  function removeSource(source: ExternalSource) {
    const confirmRemove = window.confirm(`删除外部源\n确定要删除外部源「${source.name}」吗？订阅关联会一并移除。`)
    if (!confirmRemove) return

    const key = `source-delete:${source.id}`
    if (!beginOperation(key)) return
    void (async () => {
      try {
        await deleteExternalSource(source.id)
        setSources((current) => current.filter((item) => item.id !== source.id))
        setSubscriptions((current) =>
          current.map((item) => ({
            ...item,
            external_source_ids: (item.external_source_ids ?? []).filter((id) => id !== source.id),
          })),
        )
        toast.success('外部源已成功删除')
      } catch (err) {
        toast.error(errorText(err, '删除外部源失败'))
      } finally {
        endOperation(key)
      }
    })()
  }

  const sourceName = (id: string) => sources.find((source) => source.id === id)?.name || id.slice(0, 8)

  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-zinc-100">订阅</h1>
          <p className="text-sm text-zinc-400 mt-1">管理本地节点和入站对外发布订阅链接，以及远程节点配置源</p>
        </div>
        <Button
          variant="outline"
          className="border-zinc-800 text-zinc-300 hover:bg-zinc-900 h-9 gap-1.5"
          loading={loading}
          disabled={saving || pending.size > 0}
          onClick={() => void load()}
        >
          <RefreshCw className="h-4 w-4" /> 刷新
        </Button>
      </div>

      {loadError && (
        <Alert variant="destructive">
          <AlertTitle>部分数据加载失败</AlertTitle>
          <AlertDescription>{loadError}</AlertDescription>
          <Button
            size="sm"
            variant="outline"
            className="mt-2 text-red-400 border-red-900/30 hover:bg-red-950/20"
            onClick={() => void load()}
          >
            重试
          </Button>
        </Alert>
      )}

      {/* Subscriptions Card */}
      <Card className="border-zinc-900 bg-zinc-900/30">
        <CardHeader className="p-5 pb-3 flex flex-row items-center justify-between">
          <CardTitle className="text-base font-semibold">
            订阅列表{subscriptions.length > 0 ? ` (${subscriptions.length})` : ''}
          </CardTitle>
          <Button size="sm" onClick={openCreateSubscription} className="gap-1.5">
            <Plus className="h-4 w-4" /> 创建订阅
          </Button>
        </CardHeader>
        <CardContent className="p-5 pt-0">
          <div className="overflow-x-auto">
            <Table>
              <TableHeader className="bg-zinc-900/40 border-zinc-800">
                <TableRow className="hover:bg-transparent">
                  <TableHead className="w-40">名称</TableHead>
                  <TableHead className="w-24">格式</TableHead>
                  <TableHead className="w-36">本地入站</TableHead>
                  <TableHead className="w-44">包含外部源</TableHead>
                  <TableHead className="w-20">启用</TableHead>
                  <TableHead>订阅链接</TableHead>
                  <TableHead className="w-[200px] text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody className="divide-y divide-zinc-900 border-zinc-900">
                {subscriptions.length === 0 ? (
                  <TableRow className="hover:bg-transparent">
                    <TableCell colSpan={7} className="text-center py-8 text-zinc-500 font-medium">
                      暂无本地节点订阅配置
                    </TableCell>
                  </TableRow>
                ) : (
                  subscriptions.map((row) => {
                    const rowPending = entityOperationPending('subscription', row.id)
                    const isCopied = copiedId === row.id
                    return (
                      <TableRow key={row.id} className="hover:bg-zinc-900/20 border-zinc-900/60">
                        <TableCell className="font-semibold text-zinc-200">
                          {row.name}
                        </TableCell>
                        <TableCell>
                          <code className="text-xs font-mono text-zinc-300">{row.format}</code>
                        </TableCell>
                        <TableCell className="text-zinc-300 text-xs font-medium">
                          {row.include_all_inbounds ?? row.inbound_ids.length === 0
                            ? '全部启用项'
                            : row.inbound_ids.length
                              ? `${row.inbound_ids.length} 个指定项`
                              : '无本地入站'}
                        </TableCell>
                        <TableCell>
                          {(row.external_source_ids ?? []).length ? (
                            <div className="flex flex-wrap gap-1">
                              {(row.external_source_ids ?? []).map((sourceId) => (
                                <Badge key={sourceId} variant="outline" className="text-[10px] border-zinc-800 text-zinc-400">
                                  {sourceName(sourceId)}
                                </Badge>
                              ))}
                            </div>
                          ) : (
                            <span className="text-zinc-600">—</span>
                          )}
                        </TableCell>
                        <TableCell>
                          <Switch
                            checked={row.enabled}
                            disabled={rowPending}
                            onCheckedChange={() => void toggleSubscription(row)}
                          />
                        </TableCell>
                        <TableCell>
                          <code className="text-xs font-mono text-zinc-400 break-all select-all block bg-zinc-950/40 p-1.5 rounded border border-zinc-900/40">
                            {row.url || '—'}
                          </code>
                        </TableCell>
                        <TableCell className="text-right">
                          <TooltipProvider>
                            <div className="flex items-center justify-end gap-1">
                              <Tooltip>
                                <TooltipTrigger asChild>
                                  <Button size="icon" variant="ghost" disabled={rowPending} onClick={() => openEditSubscription(row)} className="h-8 w-8 text-zinc-400 hover:text-zinc-200">
                                    <Edit className="h-4 w-4" />
                                  </Button>
                                </TooltipTrigger>
                                <TooltipContent>编辑订阅</TooltipContent>
                              </Tooltip>

                              <Tooltip>
                                <TooltipTrigger asChild>
                                  <Button size="icon" variant="ghost" disabled={rowPending || !row.url} onClick={() => void copySubscriptionURL(row)} className="h-8 w-8 text-zinc-400 hover:text-zinc-200">
                                    {isCopied ? <Check className="h-4 w-4 text-emerald-500" /> : <Copy className="h-4 w-4" />}
                                  </Button>
                                </TooltipTrigger>
                                <TooltipContent>复制订阅链接</TooltipContent>
                              </Tooltip>

                              <Tooltip>
                                <TooltipTrigger asChild>
                                  <Button size="icon" variant="ghost" disabled={rowPending} onClick={() => void showSubscriptionPreview(row)} className="h-8 w-8 text-zinc-400 hover:text-zinc-200">
                                    <Eye className="h-4 w-4" />
                                  </Button>
                                </TooltipTrigger>
                                <TooltipContent>预览生成的配置</TooltipContent>
                              </Tooltip>

                              <Tooltip>
                                <TooltipTrigger asChild>
                                  <Button size="icon" variant="ghost" disabled={rowPending} onClick={() => rotateSubscription(row)} className="h-8 w-8 text-zinc-400 hover:text-zinc-200">
                                    <RotateCw className="h-4 w-4" />
                                  </Button>
                                </TooltipTrigger>
                                <TooltipContent>轮换 Token</TooltipContent>
                              </Tooltip>

                              <Tooltip>
                                <TooltipTrigger asChild>
                                  <Button size="icon" variant="ghost" disabled={rowPending} onClick={() => removeSubscription(row)} className="h-8 w-8 text-red-500 hover:text-red-400 hover:bg-red-950/20">
                                    <Trash2 className="h-4 w-4" />
                                  </Button>
                                </TooltipTrigger>
                                <TooltipContent>删除订阅</TooltipContent>
                              </Tooltip>
                            </div>
                          </TooltipProvider>
                        </TableCell>
                      </TableRow>
                    )
                  })
                )}
              </TableBody>
            </Table>
          </div>
        </CardContent>
      </Card>

      {/* External Sources Card */}
      <Card className="border-zinc-900 bg-zinc-900/30">
        <CardHeader className="p-5 pb-3 flex flex-row items-center justify-between">
          <CardTitle className="text-base font-semibold">
            外部节点订阅源{sources.length > 0 ? ` (${sources.length})` : ''}
          </CardTitle>
          <Button size="sm" variant="outline" onClick={openCreateSource} className="border-zinc-800 text-zinc-300 hover:bg-zinc-900 gap-1.5">
            <Plus className="h-4 w-4" /> 添加外部源
          </Button>
        </CardHeader>
        <CardContent className="p-5 pt-0">
          <div className="overflow-x-auto">
            <Table>
              <TableHeader className="bg-zinc-900/40 border-zinc-800">
                <TableRow className="hover:bg-transparent">
                  <TableHead className="w-40">名称</TableHead>
                  <TableHead>配置 URL</TableHead>
                  <TableHead className="w-20 text-center">解出节点</TableHead>
                  <TableHead className="w-32">格式</TableHead>
                  <TableHead className="w-40">更新成功时间</TableHead>
                  <TableHead className="w-20">启用</TableHead>
                  <TableHead className="w-44">最近同步错误</TableHead>
                  <TableHead className="w-[180px] text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody className="divide-y divide-zinc-900 border-zinc-900">
                {sources.length === 0 ? (
                  <TableRow className="hover:bg-transparent">
                    <TableCell colSpan={8} className="text-center py-8 text-zinc-500 font-medium">
                      暂无外部节点订阅源
                    </TableCell>
                  </TableRow>
                ) : (
                  sources.map((row) => {
                    const rowPending = entityOperationPending('source', row.id)
                    return (
                      <TableRow key={row.id} className="hover:bg-zinc-900/20 border-zinc-900/60">
                        <TableCell className="font-semibold text-zinc-200">
                          {row.name}
                        </TableCell>
                        <TableCell>
                          <code className="text-xs font-mono text-zinc-500 break-all block">
                            {row.url}
                          </code>
                        </TableCell>
                        <TableCell className="text-center font-bold text-zinc-300">
                          {row.cached_proxy_count ?? 0}
                        </TableCell>
                        <TableCell className="text-xs text-zinc-400">
                          {row.content_type || '—'}
                        </TableCell>
                        <TableCell className="text-xs font-mono text-zinc-400">
                          {formatTime(row.last_success_unix)}
                        </TableCell>
                        <TableCell>
                          <Switch
                            checked={row.enabled}
                            disabled={rowPending}
                            onCheckedChange={() => void toggleSource(row)}
                          />
                        </TableCell>
                        <TableCell className="text-xs font-mono text-red-400 break-all leading-normal max-w-[200px]">
                          {row.last_error || <span className="text-zinc-600">—</span>}
                        </TableCell>
                        <TableCell className="text-right">
                          <TooltipProvider>
                            <div className="flex items-center justify-end gap-1">
                              <Tooltip>
                                <TooltipTrigger asChild>
                                  <Button size="icon" variant="ghost" disabled={rowPending} onClick={() => openEditSource(row)} className="h-8 w-8 text-zinc-400 hover:text-zinc-200">
                                    <Edit className="h-4 w-4" />
                                  </Button>
                                </TooltipTrigger>
                                <TooltipContent>编辑外部源</TooltipContent>
                              </Tooltip>

                              <Tooltip>
                                <TooltipTrigger asChild>
                                  <Button size="icon" variant="ghost" disabled={rowPending} onClick={() => void refreshSource(row)} className="h-8 w-8 text-zinc-400 hover:text-zinc-200">
                                    <RefreshCw className="h-4 w-4" />
                                  </Button>
                                </TooltipTrigger>
                                <TooltipContent>刷新并同步节点</TooltipContent>
                              </Tooltip>

                              <Tooltip>
                                <TooltipTrigger asChild>
                                  <Button size="icon" variant="ghost" disabled={rowPending} onClick={() => void showSourcePreview(row)} className="h-8 w-8 text-zinc-400 hover:text-zinc-200">
                                    <Eye className="h-4 w-4" />
                                  </Button>
                                </TooltipTrigger>
                                <TooltipContent>预览解出节点</TooltipContent>
                              </Tooltip>

                              <Tooltip>
                                <TooltipTrigger asChild>
                                  <Button size="icon" variant="ghost" disabled={rowPending} onClick={() => removeSource(row)} className="h-8 w-8 text-red-500 hover:text-red-400 hover:bg-red-950/20">
                                    <Trash2 className="h-4 w-4" />
                                  </Button>
                                </TooltipTrigger>
                                <TooltipContent>删除外部源</TooltipContent>
                              </Tooltip>
                            </div>
                          </TooltipProvider>
                        </TableCell>
                      </TableRow>
                    )
                  })
                )}
              </TableBody>
            </Table>
          </div>
        </CardContent>
      </Card>

      {/* Subscription Editor Dialog */}
      <Dialog open={subEditor.open} onOpenChange={(v) => { if (!v && !saving) closeSubscriptionEditor() }}>
        <DialogContent className="max-w-2xl bg-zinc-950 border-zinc-800 text-zinc-100 max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle className="text-lg font-bold">
              {subEditor.id ? '编辑订阅' : '创建订阅'}
            </DialogTitle>
          </DialogHeader>

          {editorError && (
            <Alert variant="destructive">
              <AlertDescription>{editorError}</AlertDescription>
            </Alert>
          )}

          <div className="space-y-4 my-2">
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div className="flex flex-col space-y-1.5">
                <Label htmlFor="sub-name" className="text-zinc-300">订阅名称 *</Label>
                <Input
                  id="sub-name"
                  value={subEditor.name}
                  maxLength={80}
                  disabled={saving}
                  onChange={(e) => setSubEditor((c) => ({ ...c, name: e.target.value }))}
                  placeholder="例如: clash-fast"
                  className="bg-zinc-900 border-zinc-800"
                />
              </div>

              <div className="flex flex-col space-y-1.5">
                <Label htmlFor="sub-format" className="text-zinc-300">配置文件格式</Label>
                <Select
                  disabled={saving}
                  value={subEditor.format}
                  onValueChange={(val) => setSubEditor((c) => ({ ...c, format: val as 'clash' | 'singbox' }))}
                >
                  <SelectTrigger className="bg-zinc-900 border-zinc-800" id="sub-format">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent className="bg-zinc-900 border-zinc-800">
                    <SelectItem value="clash" className="text-zinc-200">Clash / Mihomo (YAML)</SelectItem>
                    <SelectItem value="singbox" className="text-zinc-200">sing-box (JSON)</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </div>

            <div className="flex items-center space-x-2 pt-2">
              <Switch
                id="sub-enabled"
                checked={subEditor.enabled}
                disabled={saving}
                onCheckedChange={(checked) => setSubEditor((c) => ({ ...c, enabled: checked }))}
              />
              <Label htmlFor="sub-enabled" className="text-zinc-300 cursor-pointer select-none">启用此发布订阅</Label>
            </div>

            {/* Local Inbound Range Radio Group */}
            <div className="space-y-2 pt-4 border-t border-zinc-900">
              <Label className="text-zinc-300 block font-medium">包含本地入站节点</Label>
              <div className="flex border border-zinc-800 rounded-md bg-zinc-900 p-0.5 max-w-sm">
                {(['all', 'custom', 'none'] as const).map((mode) => (
                  <button
                    key={mode}
                    type="button"
                    disabled={saving}
                    onClick={() => setSubEditor(c => ({ ...c, localMode: mode }))}
                    className={`flex-1 text-center py-1.5 rounded text-xs font-medium transition-colors cursor-pointer ${
                      subEditor.localMode === mode
                        ? 'bg-zinc-850 text-zinc-100 shadow-sm border border-zinc-700/50'
                        : 'text-zinc-400 hover:text-zinc-200'
                    }`}
                  >
                    {mode === 'all' ? '全部启用项' : mode === 'custom' ? '指定入站' : '不包含本地'}
                  </button>
                ))}
              </div>
            </div>

            {/* Custom inbounds checklist */}
            {subEditor.localMode === 'custom' && (
              <div className="space-y-2 p-3 bg-zinc-900/50 border border-zinc-900 rounded-lg max-h-[160px] overflow-y-auto">
                <span className="text-xs text-zinc-400 font-medium block mb-2">选择指定入站节点配置：</span>
                {inbounds.length > 0 ? (
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                    {inbounds.map((inbound) => (
                      <div key={inbound.id} className="flex items-center space-x-2">
                        <Checkbox
                          id={`sub-inb-${inbound.id}`}
                          checked={subEditor.inboundIds.has(inbound.id)}
                          onCheckedChange={() => toggleEditorInbound(inbound.id)}
                        />
                        <Label htmlFor={`sub-inb-${inbound.id}`} className="text-xs text-zinc-300 font-mono cursor-pointer flex items-center gap-1">
                          {inbound.name} <span className="text-zinc-500 font-sans">({inbound.protocol})</span>
                          {!inbound.enabled && <Badge variant="secondary" className="scale-75">禁用</Badge>}
                        </Label>
                      </div>
                    ))}
                  </div>
                ) : (
                  <span className="text-xs text-zinc-500">系统中暂无入站配置</span>
                )}
              </div>
            )}

            {/* External sources checklist */}
            <div className="space-y-2 pt-4 border-t border-zinc-900">
              <Label className="text-zinc-300 block font-medium">合并外部节点订阅源</Label>
              {sources.length > 0 ? (
                <div className="p-3 bg-zinc-900/50 border border-zinc-900 rounded-lg space-y-2 max-h-[160px] overflow-y-auto">
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                    {sources.map((source) => {
                      const checked = subEditor.sourceIds.has(source.id)
                      return (
                        <div key={source.id} className="flex items-center space-x-2">
                          <Checkbox
                            id={`sub-src-${source.id}`}
                            checked={checked}
                            disabled={!source.enabled && !checked}
                            onCheckedChange={() => toggleEditorSource(source.id)}
                          />
                          <Label htmlFor={`sub-src-${source.id}`} className="text-xs text-zinc-300 cursor-pointer flex items-center gap-1.5">
                            {source.name}
                            {!source.enabled && <Badge variant="secondary" className="scale-75">禁用</Badge>}
                            {source.cached_proxy_count > 0 && (
                              <Badge variant="outline" className="scale-75 text-zinc-500 border-zinc-800">
                                {source.cached_proxy_count} 节点
                              </Badge>
                            )}
                          </Label>
                        </div>
                      )
                    })}
                  </div>
                </div>
              ) : (
                <span className="text-xs text-zinc-500 leading-normal block">暂无外部源。添加外部源后，您可以将购买/托管的外部订阅源混合并入此处生成的 Clash/sing-box 发布订阅中。</span>
              )}
            </div>
          </div>

          <DialogFooter className="mt-4">
            <Button variant="outline" disabled={saving} onClick={closeSubscriptionEditor} className="border-zinc-800 hover:bg-zinc-900">
              取消
            </Button>
            <Button loading={saving} onClick={() => void saveSubscription()}>
              {subEditor.id ? '保存修改' : '创建订阅'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* External Source Editor Dialog */}
      <Dialog open={sourceEditor.open} onOpenChange={(v) => { if (!v && !saving) closeSourceEditor() }}>
        <DialogContent className="max-w-2xl bg-zinc-950 border-zinc-800 text-zinc-100 max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle className="text-lg font-bold">
              {sourceEditor.id ? '编辑外部源' : '添加外部源'}
            </DialogTitle>
          </DialogHeader>

          {editorError && (
            <Alert variant="destructive">
              <AlertDescription>{editorError}</AlertDescription>
            </Alert>
          )}

          <div className="space-y-4 my-2">
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div className="flex flex-col space-y-1.5">
                <Label htmlFor="src-name" className="text-zinc-300">外部源名称 *</Label>
                <Input
                  id="src-name"
                  value={sourceEditor.name}
                  maxLength={80}
                  disabled={saving}
                  onChange={(e) => setSourceEditor((c) => ({ ...c, name: e.target.value }))}
                  placeholder="例如: 机场 A"
                  className="bg-zinc-900 border-zinc-800"
                />
              </div>

              <div className="flex flex-col space-y-1.5">
                <Label htmlFor="src-interval" className="text-zinc-300">自动同步间隔（秒）</Label>
                <Input
                  id="src-interval"
                  type="number"
                  min={0}
                  max={31536000}
                  value={sourceEditor.interval}
                  disabled={saving}
                  onChange={(e) => setSourceEditor((c) => ({ ...c, interval: Number(e.target.value) || 0 }))}
                  placeholder="0 表示使用默认 24 小时"
                  className="bg-zinc-900 border-zinc-800"
                />
              </div>
            </div>

            <div className="flex flex-col space-y-1.5">
              <Label htmlFor="src-url" className="text-zinc-300">订阅 URL *</Label>
              <Input
                id="src-url"
                value={sourceEditor.url}
                disabled={saving}
                onChange={(e) => setSourceEditor((c) => ({ ...c, url: e.target.value }))}
                placeholder="https://example.com/sub/token?clash=1"
                className="bg-zinc-900 border-zinc-800 font-mono text-xs"
              />
            </div>

            <div className="flex flex-col space-y-1.5">
              <div className="flex items-center justify-between">
                <Label htmlFor="src-headers" className="text-zinc-300">自定义 HTTP 请求头 (JSON格式)</Label>
                <span className="text-[10px] text-zinc-500 flex items-center gap-0.5">
                  <HelpCircle className="h-3 w-3" /> 可选，设置 User-Agent 等
                </span>
              </div>
              <textarea
                id="src-headers"
                value={sourceEditor.headers}
                disabled={saving}
                onChange={(e) => setSourceEditor((c) => ({ ...c, headers: e.target.value }))}
                placeholder='{\n  "User-Agent": "clash.meta"\n}'
                rows={3}
                className="flex w-full rounded-md border border-zinc-800 bg-zinc-900 px-3 py-2 text-xs font-mono text-zinc-300 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              />
            </div>

            <div className="flex items-center space-x-2 pt-2">
              <Switch
                id="src-enabled"
                checked={sourceEditor.enabled}
                disabled={saving}
                onCheckedChange={(checked) => setSourceEditor((c) => ({ ...c, enabled: checked }))}
              />
              <Label htmlFor="src-enabled" className="text-zinc-300 cursor-pointer select-none">启用此外部源同步</Label>
            </div>
          </div>

          <DialogFooter className="mt-4">
            <Button variant="outline" disabled={saving} onClick={closeSourceEditor} className="border-zinc-800 hover:bg-zinc-900">
              取消
            </Button>
            <Button loading={saving} onClick={() => void saveSource()}>
              {sourceEditor.id ? '保存修改' : '添加外部源'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Configuration Text Preview Dialog */}
      <Dialog open={textPreview !== null} onOpenChange={(v) => { if (!v) setTextPreview(null) }}>
        <DialogContent className="max-w-4xl bg-zinc-950 border-zinc-800 text-zinc-100">
          <DialogHeader>
            <DialogTitle className="text-base font-bold">
              {textPreview?.title}
            </DialogTitle>
          </DialogHeader>
          <pre className="p-4 bg-zinc-900 border border-zinc-800 rounded-md overflow-auto text-xs font-mono text-zinc-300 leading-relaxed max-h-[500px] select-all">
            {textPreview?.text}
          </pre>
          <DialogFooter>
            <Button onClick={() => setTextPreview(null)}>关闭</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* External Source Preview Dialog */}
      <Dialog open={sourcePreview !== null} onOpenChange={(v) => { if (!v) setSourcePreview(null) }}>
        <DialogContent className="max-w-2xl bg-zinc-950 border-zinc-800 text-zinc-100">
          <DialogHeader>
            <DialogTitle className="text-base font-bold">
              {sourcePreviewTitle}
            </DialogTitle>
          </DialogHeader>

          {sourcePreview && (
            <div className="space-y-4 my-2">
              <div className="flex gap-2">
                <Badge>{sourcePreview.count} 个节点</Badge>
                {sourcePreview.source.content_type && (
                  <Badge variant="outline" className="border-zinc-800 text-zinc-500">
                    {sourcePreview.source.content_type}
                  </Badge>
                )}
              </div>

              {(sourcePreview.warnings ?? []).length > 0 && (
                <Alert variant="warning">
                  <AlertTitle>解析警告信息</AlertTitle>
                  <AlertDescription>
                    {(sourcePreview.warnings ?? []).join('；')}
                  </AlertDescription>
                </Alert>
              )}

              <div className="space-y-1">
                <span className="text-xs text-zinc-400 font-semibold uppercase tracking-wider block mb-1">节点名称列表 (显示前 300 个)</span>
                <div className="p-4 bg-zinc-900 border border-zinc-800 rounded-lg max-h-[300px] overflow-y-auto flex flex-wrap gap-1.5 text-xs text-zinc-300 leading-relaxed">
                  {sourcePreview.names.length === 0 ? (
                    <span className="text-zinc-600 italic">未解析出任何节点，请确认 URL 及节点格式。</span>
                  ) : (
                    sourcePreview.names.slice(0, 300).map((name, index) => (
                      <span key={`${name}-${index}`} className="px-2 py-0.5 rounded bg-zinc-950 border border-zinc-900 text-zinc-400 font-mono">
                        {name}
                      </span>
                    ))
                  )}
                </div>
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

function toggleSet(source: Set<string>, id: string): Set<string> {
  const next = new Set(source)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  return next
}

function validateSourceURL(value: string): string {
  const trimmed = value.trim()
  if (!trimmed) return '请填写订阅 URL'
  try {
    const url = new URL(trimmed)
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return '订阅 URL 仅支持 http 或 https'
    if (url.username || url.password) return '订阅 URL 不能包含明文用户名或密码'
  } catch {
    return '请输入完整有效的订阅 URL'
  }
  return ''
}

function parseHeaders(value: string): Record<string, string> {
  if (!value.trim()) return {}
  const parsed = JSON.parse(value) as unknown
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error('请求头必须是 JSON 对象')
  }
  const headers: Record<string, string> = {}
  for (const [key, headerValue] of Object.entries(parsed)) {
    if (!key.trim() || typeof headerValue !== 'string') {
      throw new Error('请求头名称不能为空，值必须是字符串')
    }
    headers[key.trim()] = headerValue
  }
  return headers
}

function errorText(error: unknown, fallback: string): string {
  return error instanceof Error && error.message ? error.message : fallback
}
