import { useCallback, useEffect, useMemo, useState } from 'react'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { ConfirmModal } from '@/components/ui/confirm-modal'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
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
import {
  ArrowDown,
  ArrowUp,
  Edit,
  Loader2,
  Plus,
  RefreshCw,
  Route,
  Trash2,
} from 'lucide-react'
import {
  createRoutePlan,
  deleteRoutePlan,
  listProxyChains,
  listRoutePlans,
  listSubscriptions,
  updateRoutePlan,
  type ProxyChain,
  type RoutePlan,
  type RoutePlanAction,
  type RoutePlanMatchType,
  type RoutePlanRule,
  type Subscription,
} from '../api/client'
import { formatTime } from '../lib/nodeDisplay'
import { toast } from '../lib/toast'

type RuleDraft = {
  match_type: RoutePlanMatchType
  match_value: string
  action: RoutePlanAction
  target_chain_id: string
  enabled: boolean
}

type PlanEditor = {
  open: boolean
  id: string | null
  name: string
  scope: 'global' | 'subscription'
  subscriptionId: string
  enabled: boolean
  rules: RuleDraft[]
}

const EMPTY_RULE: RuleDraft = {
  match_type: 'domain_suffix',
  match_value: '',
  action: 'direct',
  target_chain_id: '',
  enabled: true,
}

const EMPTY_EDITOR: PlanEditor = {
  open: false,
  id: null,
  name: '',
  scope: 'global',
  subscriptionId: '',
  enabled: true,
  rules: [],
}

const MATCH_TYPES: Array<{ value: RoutePlanMatchType; label: string }> = [
  { value: 'domain', label: '精确域名' },
  { value: 'domain_suffix', label: '域名后缀' },
  { value: 'domain_keyword', label: '域名关键字' },
  { value: 'ip_cidr', label: 'IP CIDR' },
  { value: 'process_name', label: '进程名' },
]

const ACTIONS: Array<{ value: RoutePlanAction; label: string }> = [
  { value: 'proxy', label: '走代理链' },
  { value: 'direct', label: '直连' },
  { value: 'block', label: '拦截' },
]

export default function RoutePlans() {
  const [plans, setPlans] = useState<RoutePlan[]>([])
  const [subscriptions, setSubscriptions] = useState<Subscription[]>([])
  const [chains, setChains] = useState<ProxyChain[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [pending, setPending] = useState<Set<string>>(new Set())
  const [editor, setEditor] = useState<PlanEditor>(EMPTY_EDITOR)
  const [editorError, setEditorError] = useState('')
  const [saving, setSaving] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<RoutePlan | null>(null)

  const subscriptionByID = useMemo(
    () => new Map(subscriptions.map((sub) => [sub.id, sub])),
    [subscriptions],
  )

  const load = useCallback(async () => {
    setLoading(true)
    setLoadError('')
    try {
      const [nextPlans, nextSubscriptions, nextChains] = await Promise.all([
        listRoutePlans(),
        listSubscriptions(),
        listProxyChains(),
      ])
      setPlans(nextPlans ?? [])
      setSubscriptions(nextSubscriptions ?? [])
      setChains(nextChains ?? [])
    } catch (err) {
      setLoadError(errorText(err, '路由计划加载失败'))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  function openCreate() {
    setEditorError('')
    setEditor({ ...EMPTY_EDITOR, open: true, rules: [{ ...EMPTY_RULE }] })
  }

  function openEdit(plan: RoutePlan) {
    setEditorError('')
    setEditor({
      open: true,
      id: plan.id,
      name: plan.name,
      scope: plan.scope === 'subscription' ? 'subscription' : 'global',
      subscriptionId: plan.subscription_id ?? '',
      enabled: plan.enabled,
      rules: [...(plan.rules ?? [])]
        .sort((a, b) => a.position - b.position)
        .map((rule) => ({
          match_type: rule.match_type,
          match_value: rule.match_value,
          action: rule.action,
          target_chain_id: rule.target_chain_id ?? '',
          enabled: rule.enabled,
        })),
    })
  }

  function closeEditor() {
    if (!saving) setEditor(EMPTY_EDITOR)
  }

  function updateRule(index: number, patch: Partial<RuleDraft>) {
    setEditor((current) => ({
      ...current,
      rules: current.rules.map((rule, ruleIndex) =>
        ruleIndex === index ? { ...rule, ...patch } : rule,
      ),
    }))
  }

  function moveRule(index: number, direction: -1 | 1) {
    setEditor((current) => {
      const target = index + direction
      if (target < 0 || target >= current.rules.length) return current
      const rules = [...current.rules]
      ;[rules[index], rules[target]] = [rules[target], rules[index]]
      return { ...current, rules }
    })
  }

  function removeRule(index: number) {
    setEditor((current) => ({
      ...current,
      rules: current.rules.filter((_, ruleIndex) => ruleIndex !== index),
    }))
  }

  async function save() {
    const name = editor.name.trim()
    if (!name) {
      setEditorError('请填写路由计划名称')
      return
    }
    if (editor.scope === 'subscription' && !editor.subscriptionId) {
      setEditorError('作用域为订阅时请选择一个订阅')
      return
    }
    const invalidValue = editor.rules.findIndex((rule) => !rule.match_value.trim())
    if (invalidValue >= 0) {
      setEditorError(`第 ${invalidValue + 1} 条规则需要填写匹配值`)
      return
    }
    const missingChain = editor.rules.findIndex(
      (rule) => rule.action === 'proxy' && !rule.target_chain_id,
    )
    if (missingChain >= 0) {
      setEditorError(`第 ${missingChain + 1} 条规则选择「走代理链」时需要指定代理链`)
      return
    }

    // 规则按当前顺序整体提交，position 与数组顺序一致。
    const rules: RoutePlanRule[] = editor.rules.map((rule, index) => ({
      position: index,
      match_type: rule.match_type,
      match_value: rule.match_value.trim(),
      action: rule.action,
      target_chain_id: rule.action === 'proxy' ? rule.target_chain_id : undefined,
      enabled: rule.enabled,
    }))

    setSaving(true)
    setEditorError('')
    try {
      const saved = editor.id
        ? await updateRoutePlan(editor.id, { name, enabled: editor.enabled, rules })
        : await createRoutePlan({
            name,
            scope: editor.scope,
            subscription_id:
              editor.scope === 'subscription' ? editor.subscriptionId : undefined,
            enabled: editor.enabled,
            rules,
          })
      setPlans((current) =>
        editor.id
          ? current.map((plan) => (plan.id === saved.id ? saved : plan))
          : [...current, saved],
      )
      toast.success(editor.id ? '路由计划已更新' : '路由计划已创建')
      setEditor(EMPTY_EDITOR)
    } catch (err) {
      setEditorError(errorText(err, '保存路由计划失败'))
    } finally {
      setSaving(false)
    }
  }

  async function togglePlan(plan: RoutePlan) {
    if (pending.has(plan.id)) return
    setPending((current) => new Set(current).add(plan.id))
    try {
      const updated = await updateRoutePlan(plan.id, { enabled: !plan.enabled })
      setPlans((current) => current.map((item) => (item.id === updated.id ? updated : item)))
      toast.success(updated.enabled ? '路由计划已启用' : '路由计划已停用')
    } catch (err) {
      toast.error(errorText(err, '切换路由计划状态失败'))
    } finally {
      setPending((current) => {
        const next = new Set(current)
        next.delete(plan.id)
        return next
      })
    }
  }

  async function confirmDeletePlan() {
    if (!deleteTarget) return
    const plan = deleteTarget
    setDeleteTarget(null)
    setPending((current) => new Set(current).add(plan.id))
    try {
      await deleteRoutePlan(plan.id)
      setPlans((current) => current.filter((item) => item.id !== plan.id))
      toast.success('路由计划已删除')
    } catch (err) {
      toast.error(errorText(err, '删除路由计划失败'))
    } finally {
      setPending((current) => {
        const next = new Set(current)
        next.delete(plan.id)
        return next
      })
    }
  }

  function scopeText(plan: RoutePlan): string {
    if (plan.scope === 'subscription') {
      const sub = plan.subscription_id ? subscriptionByID.get(plan.subscription_id) : undefined
      return sub ? `订阅 · ${sub.name}` : '订阅'
    }
    return '全局'
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between border-b border-border pb-5">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-foreground">路由计划</h1>
          <p className="text-sm text-muted-foreground mt-1">
            按域名、IP 或进程匹配流量，决定走代理链、直连还是拦截；规则自上而下依次匹配
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" onClick={() => void load()} loading={loading} className="gap-1.5">
            <RefreshCw className="h-4 w-4" /> 刷新
          </Button>
          <Button onClick={openCreate} className="gap-1.5">
            <Plus className="h-4 w-4" /> 新建路由计划
          </Button>
        </div>
      </div>

      {loadError && (
        <Alert variant="destructive">
          <AlertDescription>{loadError}</AlertDescription>
        </Alert>
      )}

      {loading && plans.length === 0 ? (
        <div className="flex items-center justify-center py-20 gap-3 text-sm text-muted-foreground">
          <Loader2 className="h-5 w-5 animate-spin" /> 正在加载路由计划…
        </div>
      ) : plans.length === 0 ? (
        <Card className="border-border bg-card">
          <CardContent className="py-16 text-center space-y-3">
            <Route className="h-10 w-10 text-muted-foreground mx-auto" />
            <h2 className="font-semibold text-foreground">暂无路由计划</h2>
            <p className="text-xs text-muted-foreground">
              创建路由计划后，可在订阅编辑中绑定到指定订阅。
            </p>
            <Button onClick={openCreate} className="gap-1.5">
              <Plus className="h-4 w-4" /> 新建路由计划
            </Button>
          </CardContent>
        </Card>
      ) : (
        <Card className="border-border bg-card">
          <CardContent className="p-0">
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow className="hover:bg-transparent">
                    <TableHead className="min-w-[160px]">名称</TableHead>
                    <TableHead className="min-w-[140px]">作用域</TableHead>
                    <TableHead className="w-20 text-center">规则数</TableHead>
                    <TableHead className="w-32">更新时间</TableHead>
                    <TableHead className="w-24">启用</TableHead>
                    <TableHead className="w-[140px] text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {plans.map((plan) => {
                    const busy = pending.has(plan.id)
                    return (
                      <TableRow key={plan.id}>
                        <TableCell className="font-semibold text-foreground">
                          {plan.name}
                          {!plan.enabled && (
                            <Badge variant="secondary" className="ml-2 text-[10px]">
                              已停用
                            </Badge>
                          )}
                        </TableCell>
                        <TableCell>
                          <Badge
                            variant={plan.scope === 'subscription' ? 'info' : 'outline'}
                            className="text-[10px]"
                          >
                            {scopeText(plan)}
                          </Badge>
                        </TableCell>
                        <TableCell className="text-center font-mono font-bold text-foreground">
                          {plan.rules?.length ?? 0}
                        </TableCell>
                        <TableCell className="text-xs text-muted-foreground font-mono">
                          {formatTime(plan.updated_at_unix)}
                        </TableCell>
                        <TableCell>
                          <Switch
                            checked={plan.enabled}
                            disabled={busy}
                            onCheckedChange={() => void togglePlan(plan)}
                          />
                        </TableCell>
                        <TableCell className="text-right">
                          <div className="flex items-center justify-end gap-1">
                            <Button
                              size="sm"
                              variant="ghost"
                              disabled={busy}
                              onClick={() => openEdit(plan)}
                              className="h-8 px-2 text-xs text-muted-foreground hover:text-foreground cursor-pointer gap-1"
                            >
                              <Edit className="h-3.5 w-3.5" /> 编辑
                            </Button>
                            <Button
                              size="sm"
                              variant="ghost"
                              disabled={busy}
                              onClick={() => setDeleteTarget(plan)}
                              className="h-8 px-2 text-xs text-destructive hover:text-destructive/80 cursor-pointer gap-1"
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

      {/* Plan Editor Dialog */}
      <Dialog open={editor.open} onOpenChange={(open) => !open && closeEditor()}>
        <DialogContent className="sm:max-w-3xl max-h-[90vh] overflow-y-auto bg-background border-border text-foreground p-6 space-y-5">
          <DialogHeader>
            <DialogTitle>{editor.id ? '编辑路由计划' : '新建路由计划'}</DialogTitle>
          </DialogHeader>
          {editorError && (
            <Alert variant="destructive">
              <AlertDescription>{editorError}</AlertDescription>
            </Alert>
          )}

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div className="space-y-2">
              <Label htmlFor="plan-name">计划名称</Label>
              <Input
                id="plan-name"
                value={editor.name}
                onChange={(event) => setEditor({ ...editor, name: event.target.value })}
                placeholder="例：国内直连 / 流媒体走香港链"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="plan-scope">作用域</Label>
              <select
                id="plan-scope"
                value={editor.scope}
                disabled={!!editor.id}
                onChange={(event) =>
                  setEditor({
                    ...editor,
                    scope: event.target.value === 'subscription' ? 'subscription' : 'global',
                  })
                }
                className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm text-foreground disabled:opacity-50"
              >
                <option value="global">全局（所有订阅）</option>
                <option value="subscription">指定订阅</option>
              </select>
              {editor.id && (
                <p className="text-[10px] text-muted-foreground">作用域创建后不可修改</p>
              )}
            </div>
            {editor.scope === 'subscription' && (
              <div className="space-y-2 sm:col-span-2">
                <Label htmlFor="plan-subscription">绑定订阅</Label>
                <select
                  id="plan-subscription"
                  value={editor.subscriptionId}
                  disabled={!!editor.id}
                  onChange={(event) => setEditor({ ...editor, subscriptionId: event.target.value })}
                  className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm text-foreground disabled:opacity-50"
                >
                  <option value="">请选择订阅…</option>
                  {subscriptions.map((sub) => (
                    <option key={sub.id} value={sub.id}>
                      {sub.name}
                    </option>
                  ))}
                </select>
              </div>
            )}
          </div>

          <div className="flex items-center justify-between rounded-md border border-border bg-card px-3 py-2">
            <Label htmlFor="plan-enabled" className="text-xs text-foreground">
              启用该路由计划
            </Label>
            <Switch
              id="plan-enabled"
              checked={editor.enabled}
              onCheckedChange={(value) => setEditor({ ...editor, enabled: value })}
            />
          </div>

          <div className="space-y-3">
            <div className="space-y-1">
              <Label>规则（自上而下依次匹配）</Label>
              <p className="text-[11px] text-muted-foreground">
                保存时按当前顺序整体提交；动作选择「走代理链」时需指定目标代理链。
              </p>
            </div>
            <div className="space-y-3">
              {editor.rules.map((rule, index) => (
                <div key={index} className="rounded-lg border border-border bg-card p-4 space-y-3">
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <Badge variant="outline" className="border-border">
                        规则 {index + 1}
                      </Badge>
                      {!rule.enabled && (
                        <span className="text-[10px] text-muted-foreground">已停用</span>
                      )}
                    </div>
                    <div className="flex items-center gap-1">
                      <Button
                        type="button"
                        size="sm"
                        variant="ghost"
                        disabled={index === 0}
                        onClick={() => moveRule(index, -1)}
                        className="h-7 w-7 p-0"
                      >
                        <ArrowUp className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        type="button"
                        size="sm"
                        variant="ghost"
                        disabled={index === editor.rules.length - 1}
                        onClick={() => moveRule(index, 1)}
                        className="h-7 w-7 p-0"
                      >
                        <ArrowDown className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        type="button"
                        size="sm"
                        variant="ghost"
                        onClick={() => removeRule(index)}
                        className="h-7 w-7 p-0 text-destructive"
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                  </div>
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                    <div className="space-y-1">
                      <Label className="text-xs text-muted-foreground">匹配类型</Label>
                      <select
                        value={rule.match_type}
                        onChange={(event) =>
                          updateRule(index, {
                            match_type: event.target.value as RoutePlanMatchType,
                          })
                        }
                        className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm text-foreground"
                      >
                        {MATCH_TYPES.map((item) => (
                          <option key={item.value} value={item.value}>
                            {item.label}
                          </option>
                        ))}
                      </select>
                    </div>
                    <div className="space-y-1">
                      <Label className="text-xs text-muted-foreground">匹配值</Label>
                      <Input
                        value={rule.match_value}
                        onChange={(event) => updateRule(index, { match_value: event.target.value })}
                        placeholder={matchValuePlaceholder(rule.match_type)}
                      />
                    </div>
                    <div className="space-y-1">
                      <Label className="text-xs text-muted-foreground">动作</Label>
                      <select
                        value={rule.action}
                        onChange={(event) =>
                          updateRule(index, { action: event.target.value as RoutePlanAction })
                        }
                        className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm text-foreground"
                      >
                        {ACTIONS.map((item) => (
                          <option key={item.value} value={item.value}>
                            {item.label}
                          </option>
                        ))}
                      </select>
                    </div>
                    {rule.action === 'proxy' && (
                      <div className="space-y-1">
                        <Label className="text-xs text-muted-foreground">目标代理链</Label>
                        <select
                          value={rule.target_chain_id}
                          onChange={(event) =>
                            updateRule(index, { target_chain_id: event.target.value })
                          }
                          className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm text-foreground"
                        >
                          <option value="">请选择代理链…</option>
                          {chains.map((chain) => (
                            <option key={chain.id} value={chain.id}>
                              {chain.name}
                              {chain.enabled ? '' : '（已停用）'}
                            </option>
                          ))}
                        </select>
                      </div>
                    )}
                  </div>
                  <div className="flex items-center justify-between rounded-md border border-border bg-background px-3 py-2">
                    <Label htmlFor={`rule-enabled-${index}`} className="text-xs text-foreground">
                      启用该规则
                    </Label>
                    <Switch
                      id={`rule-enabled-${index}`}
                      checked={rule.enabled}
                      onCheckedChange={(value) => updateRule(index, { enabled: value })}
                    />
                  </div>
                </div>
              ))}
            </div>
            <Button
              type="button"
              variant="outline"
              onClick={() =>
                setEditor((current) => ({
                  ...current,
                  rules: [...current.rules, { ...EMPTY_RULE }],
                }))
              }
              className="w-full border-dashed border-border gap-1.5"
            >
              <Plus className="h-4 w-4" /> 添加规则
            </Button>
          </div>

          <DialogFooter className="gap-2 sm:gap-0">
            <Button
              variant="outline"
              disabled={saving}
              onClick={closeEditor}
              className="border-border"
            >
              取消
            </Button>
            <Button loading={saving} onClick={() => void save()}>
              {editor.id ? '保存路由计划' : '创建路由计划'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete Route Plan Confirm Modal */}
      <ConfirmModal
        open={!!deleteTarget}
        title="删除路由计划"
        description={`确定要删除路由计划「${deleteTarget?.name || ''}」吗？绑定它的订阅将不再应用该路由规则。此操作无法撤销。`}
        confirmText="确认删除"
        confirmVariant="destructive"
        onConfirm={() => void confirmDeletePlan()}
        onCancel={() => setDeleteTarget(null)}
      />
    </div>
  )
}

function matchValuePlaceholder(matchType: RoutePlanMatchType): string {
  switch (matchType) {
    case 'domain':
      return '例：www.example.com'
    case 'domain_suffix':
      return '例：example.com'
    case 'domain_keyword':
      return '例：google'
    case 'ip_cidr':
      return '例：10.0.0.0/8'
    case 'process_name':
      return '例：curl.exe'
    default:
      return ''
  }
}

function errorText(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback
}
