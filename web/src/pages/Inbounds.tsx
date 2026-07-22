import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Alert,
  AlertDescription,
  AlertTitle,
} from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Badge } from '@/components/ui/badge'
import { Edit, RefreshCw, X, Copy, Check, Info } from 'lucide-react'
import {
  createInbound,
  deleteInbound,
  listInbounds,
  listTemplates,
  updateInbound,
  type InboundConfig,
  type Template,
} from '../api/client'
import DynamicForm, {
  defaultsFromFields,
  validateDynamicFields,
  valuesFromFields,
} from '../components/DynamicForm'
import { copyText } from '../lib/clipboard'
import { toast } from '../lib/toast'

export default function Inbounds() {
  const [inbounds, setInbounds] = useState<InboundConfig[]>([])
  const [templates, setTemplates] = useState<Template[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [busy, setBusy] = useState(false)
  const [pendingIds, setPendingIds] = useState<Set<string>>(new Set())
  const busyRef = useRef(false)
  const pendingIdsRef = useRef<Set<string>>(new Set())
  const loadVersionRef = useRef(0)

  const [editingId, setEditingId] = useState<string | null>(null)
  const [name, setName] = useState('')
  const [protocol, setProtocol] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [params, setParams] = useState<Record<string, unknown>>({})
  const [nameError, setNameError] = useState('')
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [lastCreated, setLastCreated] = useState<InboundConfig | null>(null)
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)

  const selectedTemplate = useMemo(
    () => templates.find((t) => t.protocol === protocol),
    [templates, protocol],
  )

  const load = useCallback(async () => {
    const version = ++loadVersionRef.current
    setLoading(true)
    setLoadError('')
    const [inboundResult, templateResult] = await Promise.allSettled([
      listInbounds(),
      listTemplates(),
    ])
    if (version !== loadVersionRef.current) return
    const errors: string[] = []
    if (inboundResult.status === 'fulfilled') {
      setInbounds(inboundResult.value ?? [])
    } else {
      const message =
        inboundResult.reason instanceof Error ? inboundResult.reason.message : '未知错误'
      errors.push(`入站列表加载失败：${message}`)
    }
    if (templateResult.status === 'fulfilled') {
      setTemplates(templateResult.value ?? [])
    } else {
      const message =
        templateResult.reason instanceof Error ? templateResult.reason.message : '未知错误'
      errors.push(`协议模板加载失败：${message}`)
    }
    setLoadError(errors.join('；'))
    setLoading(false)
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  useEffect(() => {
    if (protocol || templates.length === 0) return
    setProtocol(templates[0].protocol)
    setParams(defaultsFromFields(templates[0].fields))
  }, [protocol, templates])

  function onProtocolChange(nextProtocol: string) {
    setProtocol(nextProtocol)
    setFieldErrors({})
    const template = templates.find((item) => item.protocol === nextProtocol)
    setParams(template ? defaultsFromFields(template.fields) : {})
  }

  function resetEditor(preferredProtocol = protocol) {
    const template =
      templates.find((item) => item.protocol === preferredProtocol) ?? templates[0]
    setEditingId(null)
    setName('')
    setNameError('')
    setFieldErrors({})
    setEnabled(true)
    setProtocol(template?.protocol ?? '')
    setParams(template ? defaultsFromFields(template.fields) : {})
  }

  function startEdit(inbound: InboundConfig) {
    const template = templates.find((item) => item.protocol === inbound.protocol)
    if (!template) {
      toast.error(`找不到协议 ${inbound.protocol} 的表单模板，无法编辑`)
      return
    }
    setEditingId(inbound.id)
    setName(inbound.name)
    setProtocol(inbound.protocol)
    setEnabled(inbound.enabled)
    setParams(valuesFromFields(template.fields, inbound.params))
    setNameError('')
    setFieldErrors({})
    setLastCreated(null)
    window.requestAnimationFrame(() => {
      document.getElementById('inbound-editor')?.scrollIntoView({
        behavior: 'smooth',
        block: 'start',
      })
    })
  }

  function validateEditor(): boolean {
    const nextNameError = name.trim() ? '' : '请填写配置名称'
    setNameError(nextNameError)
    if (!protocol || !selectedTemplate) {
      toast.warning('请选择有效的传输协议')
      return false
    }
    const nextFieldErrors = validateDynamicFields(selectedTemplate.fields, params)
    setFieldErrors(nextFieldErrors)
    if (nextNameError || Object.keys(nextFieldErrors).length > 0) {
      toast.warning('请修正表单中的错误后再保存')
      return false
    }
    return true
  }

  function requestParams(includeEmpty: boolean): Record<string, unknown> {
    const out: Record<string, unknown> = {}
    for (const field of selectedTemplate?.fields ?? []) {
      const current = params[field.name]
      const normalized = typeof current === 'string' ? current.trim() : current
      if (!includeEmpty && (normalized === '' || normalized === undefined || normalized === null)) {
        continue
      }
      out[field.name] = normalized ?? ''
    }
    return out
  }

  async function onSubmit() {
    if (busyRef.current || !validateEditor()) return
    busyRef.current = true
    loadVersionRef.current += 1
    setLoading(false)
    setBusy(true)
    const targetId = editingId
    try {
      if (targetId) {
        const updated = await updateInbound(targetId, {
          name: name.trim(),
          params: requestParams(true),
          enabled,
        })
        setInbounds((current) =>
          current.map((item) => (item.id === updated.id ? updated : item)),
        )
        toast.success(`入站配置「${updated.name}」已更新`)
        resetEditor(updated.protocol)
      } else {
        const created = await createInbound({
          name: name.trim(),
          protocol,
          params: requestParams(false),
          enabled,
        })
        setInbounds((current) => [...current, created])
        setLastCreated(created)
        toast.success('入站配置创建成功；密码 / UUID / 证书密钥已自动生成')
        resetEditor(created.protocol)
      }
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : targetId ? '更新失败' : '创建失败'
      )
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }

  async function copyCredentials() {
    if (!lastCreated) return
    try {
      await copyText(formatSecrets(lastCreated.params))
      setCopied(true)
      toast.success('凭据已复制')
      setTimeout(() => setCopied(false), 2000)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : '复制失败')
    }
  }

  function beginRowPending(id: string): boolean {
    if (pendingIdsRef.current.has(id)) return false
    const next = new Set(pendingIdsRef.current)
    next.add(id)
    pendingIdsRef.current = next
    setPendingIds(next)
    loadVersionRef.current += 1
    setLoading(false)
    return true
  }

  function endRowPending(id: string) {
    const next = new Set(pendingIdsRef.current)
    next.delete(id)
    pendingIdsRef.current = next
    setPendingIds(next)
  }

  async function onToggle(inbound: InboundConfig) {
    if (!beginRowPending(inbound.id)) return
    try {
      const updated = await updateInbound(inbound.id, { enabled: !inbound.enabled })
      setInbounds((current) =>
        current.map((item) => (item.id === updated.id ? updated : item)),
      )
      if (editingId === updated.id) setEnabled(updated.enabled)
      toast.success(updated.enabled ? '入站已启用' : '入站已禁用')
    } catch (err) {
      toast.error(err instanceof Error ? err.message : '更新状态失败')
    } finally {
      endRowPending(inbound.id)
    }
  }

  function onDelete(inbound: InboundConfig) {
    const confirmDelete = window.confirm(`删除入站\n确定要删除入站配置「${inbound.name}」吗？关联到节点和订阅的引用也会失效。`)
    if (!confirmDelete) return

    if (!beginRowPending(inbound.id)) return
    void (async () => {
      try {
        await deleteInbound(inbound.id)
        setInbounds((current) => current.filter((item) => item.id !== inbound.id))
        if (expandedId === inbound.id) setExpandedId(null)
        if (editingId === inbound.id) resetEditor(inbound.protocol)
        toast.success('入站已删除')
      } catch (err) {
        toast.error(err instanceof Error ? err.message : '删除失败')
      } finally {
        endRowPending(inbound.id)
      }
    })()
  }

  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-zinc-100">入站配置管理</h1>
          <p className="text-sm text-zinc-400 mt-1">创建协议传输模板并生成密钥凭据，再关联到业务节点下发启动</p>
        </div>
      </div>

      {loadError && (
        <Alert variant="destructive">
          <AlertTitle>部分数据加载失败</AlertTitle>
          <AlertDescription>{loadError}</AlertDescription>
          <Button
            size="sm"
            variant="outline"
            className="mt-2 text-red-400 border-red-900/30 hover:bg-red-950/20"
            loading={loading}
            disabled={busy || pendingIds.size > 0}
            onClick={() => void load()}
          >
            重试
          </Button>
        </Alert>
      )}

      {/* Editor Panel */}
      <section id="inbound-editor" className="scroll-mt-6">
        <Card className="border-zinc-900 bg-zinc-900/30">
          <CardHeader className="p-5 pb-3 flex flex-row items-center justify-between">
            <CardTitle className="text-base font-semibold">
              {editingId ? '编辑入站配置' : '创建入站配置'}
            </CardTitle>
            {editingId && (
              <Button variant="ghost" className="text-zinc-400 hover:text-zinc-200 h-8 px-3" disabled={busy} onClick={() => resetEditor()}>
                取消编辑
              </Button>
            )}
          </CardHeader>
          <CardContent className="p-5 pt-0 space-y-6">
            <p className="text-xs text-zinc-500 leading-relaxed">
              {editingId
                ? '修改公开的自定义参数不会重置服务端已生成的密码、UUID、证书或 Reality 密钥。保存后需至节点详情页重新下发同步。'
                : '只需填写名称、协议和监听端口等基础项。系统会在服务端自动配置并生成对应的强密码、UUID、自签名 TLS 证书或 Reality 混淆密钥。'}
            </p>

            <form
              onSubmit={(e) => {
                e.preventDefault()
                void onSubmit()
              }}
              className="space-y-6"
            >
              <div className="grid grid-cols-1 md:grid-cols-3 gap-4 items-end">
                <div className="flex flex-col space-y-1.5">
                  <Label htmlFor="inbound-name" className="text-zinc-300">
                    配置名称 <span className="text-red-500">*</span>
                  </Label>
                  <Input
                    id="inbound-name"
                    value={name}
                    disabled={busy}
                    maxLength={80}
                    onChange={(e) => {
                      setName(e.target.value)
                      if (nameError) setNameError('')
                    }}
                    placeholder="例如: ss-edge-1"
                    className={`bg-zinc-950 border-zinc-900 focus-visible:ring-zinc-800 ${
                      nameError ? 'border-red-500 focus-visible:ring-red-500' : ''
                    }`}
                  />
                  {nameError && <p className="text-xs text-red-500 font-medium">{nameError}</p>}
                </div>

                <div className="flex flex-col space-y-1.5">
                  <Label htmlFor="inbound-protocol" className="text-zinc-300">
                    传输协议 <span className="text-red-500">*</span>
                  </Label>
                  <Select
                    disabled={busy || loading || !!editingId}
                    value={protocol}
                    onValueChange={(val) => onProtocolChange(val)}
                  >
                    <SelectTrigger className="bg-zinc-950 border-zinc-900" id="inbound-protocol">
                      <SelectValue placeholder={loading ? '加载中…' : '请选择协议'} />
                    </SelectTrigger>
                    <SelectContent className="bg-zinc-900 border-zinc-800">
                      {templates.map((template) => (
                        <SelectItem key={template.protocol} value={template.protocol} className="text-zinc-200">
                          {template.name} ({template.protocol})
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  {editingId && (
                    <p className="text-[10px] text-zinc-500">为保护现有凭证安全，编辑时无法修改传输协议</p>
                  )}
                </div>

                <div className="flex items-center space-x-2 border border-zinc-900 bg-zinc-950/20 rounded-md px-3 h-9">
                  <Checkbox
                    id="inbound-enabled-chk"
                    checked={enabled}
                    disabled={busy}
                    onCheckedChange={(c) => setEnabled(Boolean(c))}
                  />
                  <Label htmlFor="inbound-enabled-chk" className="text-xs text-zinc-300 cursor-pointer select-none">
                    {editingId ? '保存后保持启用此配置' : '创建后立即启用配置'}
                  </Label>
                </div>
              </div>

              {selectedTemplate ? (
                <div className="space-y-4 pt-4 border-t border-zinc-900">
                  <h3 className="text-sm font-semibold text-zinc-200 flex items-center gap-1.5">
                    <Info className="h-4 w-4 text-zinc-500" />
                    {selectedTemplate.name} 特有参数配置
                  </h3>
                  <DynamicForm
                    fields={selectedTemplate.fields}
                    value={params}
                    errors={fieldErrors}
                    disabled={busy}
                    onChange={(next) => {
                      setParams(next)
                      if (Object.keys(fieldErrors).length > 0) {
                        setFieldErrors(validateDynamicFields(selectedTemplate.fields, next))
                      }
                    }}
                  />
                </div>
              ) : !loading ? (
                <Alert variant="warning">
                  <AlertDescription>当前没有可用的协议模板，请点击上方“重试”按钮重新加载。</AlertDescription>
                </Alert>
              ) : null}

              <div className="flex gap-2 pt-2 border-t border-zinc-900">
                <Button
                  type="submit"
                  loading={busy}
                  disabled={!selectedTemplate || loading}
                >
                  {editingId ? '保存配置修改' : '生成入站配置'}
                </Button>
                {editingId && (
                  <Button type="button" variant="outline" className="border-zinc-800 hover:bg-zinc-900" disabled={busy} onClick={() => resetEditor()}>
                    取消
                  </Button>
                )}
              </div>
            </form>

            {/* Generated credentials block */}
            {lastCreated && (
              <div className="rounded-lg border border-zinc-800 bg-zinc-900/40 p-5 mt-4 space-y-3">
                <div className="flex items-center justify-between">
                  <span className="text-sm font-semibold text-zinc-200">自动生成的凭据 · {lastCreated.name}</span>
                  <div className="flex items-center gap-1.5">
                    <Button
                      size="sm"
                      variant="outline"
                      className="border-zinc-800 text-zinc-300 hover:bg-zinc-900 gap-1.5"
                      onClick={() => void copyCredentials()}
                    >
                      {copied ? <Check className="h-3.5 w-3.5 text-emerald-500" /> : <Copy className="h-3.5 w-3.5" />}
                      {copied ? '已复制' : '复制凭据'}
                    </Button>
                    <Button
                      size="icon"
                      variant="ghost"
                      className="h-8 w-8 text-zinc-500 hover:text-zinc-300"
                      onClick={() => setLastCreated(null)}
                    >
                      <X className="h-4 w-4" />
                    </Button>
                  </div>
                </div>
                <pre className="p-4 bg-zinc-950 border border-zinc-900 rounded-md overflow-x-auto text-xs font-mono text-zinc-300 leading-relaxed max-h-[220px]">
                  {formatSecrets(lastCreated.params)}
                </pre>
              </div>
            )}
          </CardContent>
        </Card>
      </section>

      {/* Inbounds list table */}
      <Card className="border-zinc-900 bg-zinc-900/30">
        <CardHeader className="p-5 pb-3 flex flex-row items-center justify-between">
          <CardTitle className="text-base font-semibold">
            已有配置{inbounds.length > 0 ? ` (${inbounds.length})` : ''}
          </CardTitle>
          <Button
            variant="outline"
            className="border-zinc-800 text-zinc-300 hover:bg-zinc-900 h-8 px-3 gap-1.5"
            loading={loading}
            disabled={busy || pendingIds.size > 0}
            onClick={() => void load()}
          >
            <RefreshCw className="h-3.5 w-3.5" /> 刷新列表
          </Button>
        </CardHeader>
        <CardContent className="p-5 pt-0">
          <div className="overflow-x-auto">
            <Table>
              <TableHeader className="bg-zinc-900/40 border-zinc-800">
                <TableRow className="hover:bg-transparent">
                  <TableHead className="w-[180px]">配置名称</TableHead>
                  <TableHead className="w-[120px]">传输协议</TableHead>
                  <TableHead className="w-24">状态</TableHead>
                  <TableHead>核心参数预览</TableHead>
                  <TableHead className="w-[280px] text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody className="divide-y divide-zinc-900 border-zinc-900">
                {inbounds.length === 0 ? (
                  <TableRow className="hover:bg-transparent">
                    <TableCell colSpan={5} className="text-center py-12 text-zinc-500 font-medium">
                      暂无入站配置，请使用上方表单创建第一个。
                    </TableCell>
                  </TableRow>
                ) : (
                  inbounds.map((row) => {
                    const rowPending = pendingIds.has(row.id)
                    return (
                      <TableRow key={row.id} className="hover:bg-zinc-900/20 border-zinc-900/60">
                        <TableCell className="font-semibold text-zinc-200">
                          {row.name}
                        </TableCell>
                        <TableCell>
                          <code className="text-xs font-mono text-zinc-300">{row.protocol}</code>
                        </TableCell>
                        <TableCell>
                          <Badge variant={row.enabled ? 'success' : 'secondary'}>
                            {row.enabled ? '已启用' : '已禁用'}
                          </Badge>
                        </TableCell>
                        <TableCell>
                          <div className="space-y-2">
                            <code className="text-xs font-mono text-zinc-400 break-all block">
                              {summarizeParams(row.params)}
                            </code>
                            {expandedId === row.id && (
                              <pre className="p-3 bg-zinc-950 border border-zinc-900 rounded text-[10px] font-mono text-zinc-300 leading-normal max-h-[160px] overflow-y-auto">
                                {formatSecrets(row.params)}
                              </pre>
                            )}
                          </div>
                        </TableCell>
                        <TableCell className="text-right">
                          <div className="flex items-center justify-end gap-1">
                            <Button
                              size="sm"
                              variant="ghost"
                              disabled={rowPending}
                              onClick={() => startEdit(row)}
                              className="h-8 px-2 text-xs text-zinc-400 hover:text-zinc-200 cursor-pointer"
                            >
                              <Edit className="h-3.5 w-3.5 mr-1" /> 编辑
                            </Button>
                            <Button
                              size="sm"
                              variant="ghost"
                              loading={rowPending}
                              onClick={() => void onToggle(row)}
                              className="h-8 px-2 text-xs text-zinc-400 hover:text-zinc-200 cursor-pointer"
                            >
                              {row.enabled ? '禁用' : '启用'}
                            </Button>
                            <Button
                              size="sm"
                              variant="ghost"
                              disabled={rowPending}
                              onClick={() => setExpandedId(expandedId === row.id ? null : row.id)}
                              className="h-8 px-2 text-xs text-zinc-400 hover:text-zinc-200 cursor-pointer"
                            >
                              {expandedId === row.id ? '隐藏凭据' : '查看凭据'}
                            </Button>
                            <Button
                              size="sm"
                              variant="ghost"
                              disabled={rowPending}
                              onClick={() => onDelete(row)}
                              className="h-8 px-2 text-xs text-red-500 hover:text-red-400 hover:bg-red-950/20 cursor-pointer"
                            >
                              删除
                            </Button>
                          </div>
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
    </div>
  )
}

const SECRET_KEYS = new Set([
  'password',
  'private_key',
  'public_key',
  'uuid',
  'short_id',
  'tls_cert_pem',
  'tls_key_pem',
  'tls_cert_path',
  'tls_key_path',
])

function summarizeParams(params: Record<string, unknown> | null | undefined): string {
  if (!params) return '—'
  const parts: string[] = []
  for (const [key, value] of Object.entries(params)) {
    if (SECRET_KEYS.has(key) || key.endsWith('_pem')) {
      parts.push(`${key}=***`)
    } else {
      parts.push(`${key}=${String(value)}`)
    }
  }
  const summary = parts.join(' ')
  return summary.length > 80 ? `${summary.slice(0, 77)}…` : summary || '—'
}

function formatSecrets(params: Record<string, unknown> | null | undefined): string {
  if (!params) return '—'
  const lines: string[] = []
  const order = [
    'listen',
    'port',
    'method',
    'password',
    'uuid',
    'tls_mode',
    'private_key',
    'short_id',
    'server_name',
    'handshake_server',
    'handshake_server_port',
    'flow',
    'network',
    'up_mbps',
    'down_mbps',
  ]
  const seen = new Set<string>()
  for (const key of order) {
    if (params[key] === undefined || params[key] === null || params[key] === '') continue
    seen.add(key)
    if (key === 'tls_cert_pem' || key === 'tls_key_pem') continue
    lines.push(`${key}: ${String(params[key])}`)
  }
  if (params.tls_cert_pem) {
    lines.push('tls_cert_pem: <auto self-signed PEM, applied inline>')
  }
  if (params.tls_key_pem) {
    lines.push('tls_key_pem: <auto private key PEM, applied inline>')
  }
  for (const [key, value] of Object.entries(params)) {
    if (seen.has(key) || key.endsWith('_pem')) continue
    lines.push(`${key}: ${String(value)}`)
  }
  return lines.join('\n') || '—'
}
