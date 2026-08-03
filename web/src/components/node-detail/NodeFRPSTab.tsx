import { useState } from 'react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import {
  RefreshCw,
  ShieldCheck,
  ChevronDown,
  RadioTower,
  Server,
  Network,
  ArrowRight,
  Eye,
  EyeOff,
  Copy,
  Check,
  KeyRound,
  Play,
  Square,
  Plus,
  Trash2,
  Lock,
} from 'lucide-react'
import {
  type FRPServerConfig,
  type FRPServerMapping,
  type FRPServerMappings,
  type PutFRPServerConfigInput,
  type ManagedDomain,
  type Node,
} from '@/api/client'
import { formatBytes, runtimeLabel, runtimeTheme } from '@/lib/nodeDisplay'
import { copyText } from '@/lib/clipboard'
import { toast } from '@/lib/toast'

/** Radix Select rejects empty-string item values; sentinel for the default node address. */
const FRPS_DOMAIN_DEFAULT_VALUE = '__node_address__'

export interface NodeFRPSTabProps {
  node: Node
  nodeId: string
  busy: boolean
  busyAction: string | null
  frps: FRPServerConfig | null
  frpsDraft: PutFRPServerConfigInput | null
  frpsDirty: boolean
  frpsLoading: boolean
  frpsError: string
  frpsMappings: FRPServerMappings | null
  frpsMappingsLoading: boolean
  frpsMappingsError: string
  managedDomains: ManagedDomain[]
  updateFRPSDraft: (patch: Partial<PutFRPServerConfigInput>) => void
  loadFRPS: () => void
  loadFRPSMappings: () => void
  saveFRPS: () => void
  runFRPSAction: (action: 'refresh' | 'start' | 'stop') => void
  revealFRPSToken: () => Promise<string>
  rotateFRPSToken: () => void
}

function frpsMappingExposure(mapping: FRPServerMapping, host: string): string {
  if (mapping.remote_port) return `${host || '节点地址'}:${mapping.remote_port}`
  if (mapping.custom_domains?.length) return mapping.custom_domains.join(', ')
  if (mapping.subdomain) return mapping.subdomain
  return '由访问端协商'
}

function frpsMappingBackend(mapping: FRPServerMapping): string {
  if (mapping.plugin) return `plugin:${mapping.plugin}`
  if (mapping.local_port) return `${mapping.local_ip || '127.0.0.1'}:${mapping.local_port}`
  return mapping.local_ip || '客户端服务'
}

export function NodeFRPSTab({
  node,
  busy,
  busyAction,
  frps,
  frpsDraft,
  frpsDirty,
  frpsLoading,
  frpsError,
  frpsMappings,
  frpsMappingsLoading,
  frpsMappingsError,
  managedDomains,
  updateFRPSDraft,
  loadFRPS,
  loadFRPSMappings,
  saveFRPS,
  runFRPSAction,
  revealFRPSToken,
  rotateFRPSToken,
}: NodeFRPSTabProps) {
  const [frpsAdvanced, setFRPSAdvanced] = useState(false)
  const [revealedToken, setRevealedToken] = useState('')
  const [tokenVisible, setTokenVisible] = useState(false)
  const [tokenLoading, setTokenLoading] = useState(false)
  const [copiedToken, setCopiedToken] = useState(false)

  const handleToggleToken = async () => {
    if (tokenVisible) {
      setTokenVisible(false)
      return
    }
    if (revealedToken) {
      setTokenVisible(true)
      return
    }
    setTokenLoading(true)
    try {
      const token = await revealFRPSToken()
      setRevealedToken(token)
      setTokenVisible(true)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : '拉取 Token 失败')
    } finally {
      setTokenLoading(false)
    }
  }

  const handleCopyToken = async () => {
    let tokenToCopy = revealedToken
    if (!tokenToCopy) {
      try {
        tokenToCopy = await revealFRPSToken()
        setRevealedToken(tokenToCopy)
      } catch (err) {
        toast.error(err instanceof Error ? err.message : '获取 Token 失败')
        return
      }
    }
    try {
      await copyText(tokenToCopy)
      setCopiedToken(true)
      toast.success('FRPS 认证 Token 已复制到剪贴板')
      setTimeout(() => setCopiedToken(false), 2000)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : '复制失败')
    }
  }

  if (frpsLoading) {
    return (
      <div className="flex items-center justify-center p-8 gap-2 text-sm text-muted-foreground">
        <RefreshCw className="h-4 w-4 animate-spin" /> 正在加载 FRPS 配置…
      </div>
    )
  }

  if (frpsError || !frpsDraft) {
    return (
      <Alert variant="destructive">
        <AlertTitle>FRPS 配置暂不可用</AlertTitle>
        <AlertDescription>{frpsError || '无法读取 FRPS 配置'}</AlertDescription>
        <Button size="sm" variant="outline" className="mt-3" onClick={loadFRPS}>
          重试
        </Button>
      </Alert>
    )
  }

  const boundDomain = managedDomains.find((domain) => domain.id === frpsDraft.managed_domain_id)
  const frpcServerAddr =
    boundDomain?.fqdn || frps?.server_addr || node.public_address || node.address || '节点地址'

  return (
    <div className="space-y-6">
      {node.capabilities?.length && !node.capabilities.includes('frps-v1') ? (
        <Alert variant="warning">
          <AlertTitle>Agent 版本不支持 FRPS</AlertTitle>
          <AlertDescription>请先升级节点 Agent；配置仍可编辑，但当前版本无法接收下发。</AlertDescription>
        </Alert>
      ) : null}

      <div className="space-y-4 rounded-lg border border-border bg-card/40 p-5">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="space-y-1">
            <h3 className="text-sm font-semibold text-foreground">内嵌 FRP Server</h3>
            <p className="text-xs text-muted-foreground">
              FRPS 由 Agent 进程托管，配置持久化后会随 Agent 自动恢复。
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Badge variant={runtimeTheme(frps?.runtime_state || 'stopped')}>
              {runtimeLabel(frps?.runtime_state || 'stopped')}
            </Badge>
            {frps?.frps_version ? (
              <Badge variant="outline">frps {frps.frps_version}</Badge>
            ) : null}
          </div>
        </div>

        <div className="flex items-center gap-3 rounded-md border border-border bg-background px-3 py-3">
          <Checkbox
            id="frps-enabled"
            checked={frpsDraft.enabled}
            disabled={busy}
            onCheckedChange={(checked) => updateFRPSDraft({ enabled: Boolean(checked) })}
          />
          <div>
            <Label htmlFor="frps-enabled" className="cursor-pointer text-foreground">启用 FRPS</Label>
            <p className="text-xs text-muted-foreground">保存后立即应用；关闭时会停止 FRPS，但保留配置。</p>
          </div>
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="frps-managed-domain" className="text-muted-foreground">绑定域名</Label>
          <Select
            value={frpsDraft.managed_domain_id || FRPS_DOMAIN_DEFAULT_VALUE}
            onValueChange={(value) =>
              updateFRPSDraft({ managed_domain_id: value === FRPS_DOMAIN_DEFAULT_VALUE ? '' : value })
            }
            disabled={busy}
          >
            <SelectTrigger id="frps-managed-domain">
              <SelectValue placeholder="使用节点地址" />
            </SelectTrigger>
            <SelectContent className="bg-popover border-border">
              <SelectItem value={FRPS_DOMAIN_DEFAULT_VALUE}>使用节点地址（默认）</SelectItem>
              {managedDomains.map((domain) => (
                <SelectItem key={domain.id} value={domain.id}>
                  {domain.fqdn}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <p className="text-xs text-muted-foreground">
            绑定后 frpc 客户端通过域名连接 FRPS，A 记录由托管域名自动维护；FRPS 监听配置不变。
          </p>
        </div>

        <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
          <div className="rounded-md border border-border bg-background p-3">
            <span className="block text-[10px] uppercase tracking-wider text-muted-foreground">控制入口</span>
            <code className="mt-1 block text-sm text-foreground">
              {frpcServerAddr}:{frpsDraft.bind_port}
            </code>
          </div>
          <div className="rounded-md border border-border bg-background p-3">
            <span className="block text-[10px] uppercase tracking-wider text-muted-foreground">代理端口池</span>
            <code className="mt-1 block text-sm text-foreground">
              {frpsDraft.allow_ports.map((range) => `${range.start}-${range.end}`).join(', ') || '自动分配'}
            </code>
          </div>
          <div className="rounded-md border border-success/30 bg-success/10 p-3">
            <span className="block text-[10px] uppercase tracking-wider text-success">安全策略</span>
            <span className="mt-1 flex items-center gap-1.5 text-sm text-success-foreground font-medium">
              <ShieldCheck className="h-3.5 w-3.5" />
              随机令牌 · {frpsDraft.tls_force ? '强制 TLS' : '可选 TLS'}
            </span>
          </div>
        </div>

        {/* FRPS Auth Token Card & Client Setup Helper */}
        <div className="rounded-md border border-border bg-muted/30 p-4 space-y-3">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="space-y-0.5">
              <div className="text-xs font-semibold text-foreground flex items-center gap-1.5">
                <Lock className="h-3.5 w-3.5 text-primary" />
                客户端认证 Token (auth_token)
              </div>
              <p className="text-[11px] text-muted-foreground">
                客户端 (frpc) 必须配置此 Token 才能连接。默认为 256 位高强度随机密钥。
              </p>
            </div>

            <div className="flex items-center gap-2">
              <Button
                type="button"
                size="sm"
                variant="outline"
                loading={tokenLoading}
                onClick={handleToggleToken}
                className="h-8 text-xs gap-1"
              >
                {tokenVisible ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
                {tokenVisible ? '隐藏 Token' : '显示 Token'}
              </Button>
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={handleCopyToken}
                className="h-8 text-xs gap-1"
              >
                {copiedToken ? <Check className="h-3.5 w-3.5 text-success" /> : <Copy className="h-3.5 w-3.5" />}
                {copiedToken ? '已复制' : '复制 Token'}
              </Button>
            </div>
          </div>

          {tokenVisible && revealedToken ? (
            <div className="p-3 bg-background border border-border rounded font-mono text-xs text-foreground break-all">
              {revealedToken}
            </div>
          ) : null}

          <div className="rounded border border-border/60 bg-background/50 p-3 text-xs space-y-1.5 text-muted-foreground">
            <div className="font-semibold text-foreground flex items-center gap-1">
              💡 FRPC 客户端配置要点（加不上客户端排查）：
            </div>
            <ul className="list-disc pl-4 space-y-1 font-mono text-[11px]">
              <li>
                <span className="text-foreground font-medium">1. 服务端地址 & 端口：</span>
                <code>server_addr = "{frpcServerAddr}"</code>, <code>server_port = {frpsDraft.bind_port}</code>
              </li>
              <li>
                <span className="text-foreground font-medium">2. 认证 Token：</span>
                <code>auth.token = "{tokenVisible && revealedToken ? revealedToken : '<点击“显示 Token”获取>'}"</code>
              </li>
              <li>
                <span className="text-foreground font-medium">3. TLS 传输加密：</span>
                {frpsDraft.tls_force ? (
                  <span className="text-warning">已启用强制 TLS，frpc 配置文件必须设置 <code>transport.tls.enable = true</code> (TOML) 或 <code>tls_enable = true</code> (INI)</span>
                ) : (
                  <span>建议配置 <code>transport.tls.enable = true</code></span>
                )}
              </li>
              <li>
                <span className="text-foreground font-medium">4. 映射端口：</span>
                <code>remote_port</code> 必须属于允许端口范围 <code>{frpsDraft.allow_ports.map((r) => `${r.start}-${r.end}`).join(', ') || '任意'}</code>
              </li>
            </ul>
          </div>
        </div>

        <button
          type="button"
          onClick={() => setFRPSAdvanced((open) => !open)}
          className="flex w-full items-center justify-between rounded-md border border-border bg-background px-3 py-2 text-left text-xs text-muted-foreground transition-colors hover:border-border/80 hover:text-foreground"
        >
          <span>高级设置：绑定地址、端口范围、TLS 强制与客户端限额</span>
          <ChevronDown className={`h-4 w-4 transition-transform ${frpsAdvanced ? 'rotate-180' : ''}`} />
        </button>

        {frpsAdvanced ? (
          <div className="space-y-4 rounded-md border border-border bg-background p-4">
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div className="space-y-1.5">
                <Label htmlFor="frps-bind-addr" className="text-muted-foreground">控制面绑定 IP</Label>
                <Input
                  id="frps-bind-addr"
                  value={frpsDraft.bind_addr}
                  disabled={busy}
                  onChange={(event) => updateFRPSDraft({ bind_addr: event.target.value })}
                  className="font-mono"
                  placeholder="0.0.0.0"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="frps-bind-port" className="text-muted-foreground">控制端口</Label>
                <Input
                  id="frps-bind-port"
                  type="number"
                  min={1}
                  max={65535}
                  value={frpsDraft.bind_port}
                  disabled={busy}
                  onChange={(event) => updateFRPSDraft({ bind_port: Number(event.target.value) })}
                  className="font-mono"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="frps-proxy-bind-addr" className="text-muted-foreground">代理端口绑定 IP</Label>
                <Input
                  id="frps-proxy-bind-addr"
                  value={frpsDraft.proxy_bind_addr}
                  disabled={busy}
                  onChange={(event) => updateFRPSDraft({ proxy_bind_addr: event.target.value })}
                  className="font-mono"
                  placeholder="0.0.0.0"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="frps-max-ports" className="text-muted-foreground">每客户端最大端口数</Label>
                <Input
                  id="frps-max-ports"
                  type="number"
                  min={0}
                  value={frpsDraft.max_ports_per_client}
                  disabled={busy}
                  onChange={(event) => updateFRPSDraft({ max_ports_per_client: Number(event.target.value) })}
                  placeholder="默认 8；0 = 不限制"
                />
              </div>
            </div>

            <div className="flex items-center gap-3 pt-2 border-t border-border">
              <Checkbox
                id="frps-tls-force"
                checked={frpsDraft.tls_force}
                disabled={busy}
                onCheckedChange={(checked) => updateFRPSDraft({ tls_force: Boolean(checked) })}
              />
              <div>
                <Label htmlFor="frps-tls-force" className="cursor-pointer text-foreground">强制客户端使用 TLS 加密连接 (tls_force)</Label>
                <p className="text-xs text-muted-foreground">勾选时，未开启 TLS 的 frpc 客户端将被拒绝连接；取消勾选可兼容旧版明文 frpc。</p>
              </div>
            </div>

            {/* Allowed Ports List Manager */}
            <div className="space-y-3 pt-3 border-t border-border">
              <div className="flex items-center justify-between gap-3">
                <div>
                  <h4 className="text-xs font-semibold text-foreground">允许的代理端口范围</h4>
                  <p className="text-[11px] text-muted-foreground">客户端申请映射的端口必须在下列范围之内。</p>
                </div>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  disabled={busy}
                  onClick={() =>
                    updateFRPSDraft({
                      allow_ports: [...frpsDraft.allow_ports, { start: 20000, end: 30000 }],
                    })
                  }
                  className="gap-1 h-7 text-xs"
                >
                  <Plus className="h-3.5 w-3.5" /> 添加范围
                </Button>
              </div>

              <div className="space-y-2">
                {frpsDraft.allow_ports.map((range, index) => (
                  <div key={index} className="grid grid-cols-[1fr_auto_1fr_auto] items-center gap-2">
                    <Input
                      type="number"
                      min={1}
                      max={65535}
                      value={range.start}
                      disabled={busy}
                      onChange={(event) => {
                        const next = frpsDraft.allow_ports.map((item, itemIndex) =>
                          itemIndex === index ? { ...item, start: Number(event.target.value) } : item
                        )
                        updateFRPSDraft({ allow_ports: next })
                      }}
                      className="font-mono h-8"
                    />
                    <span className="text-muted-foreground">—</span>
                    <Input
                      type="number"
                      min={1}
                      max={65535}
                      value={range.end}
                      disabled={busy}
                      onChange={(event) => {
                        const next = frpsDraft.allow_ports.map((item, itemIndex) =>
                          itemIndex === index ? { ...item, end: Number(event.target.value) } : item
                        )
                        updateFRPSDraft({ allow_ports: next })
                      }}
                      className="font-mono h-8"
                    />
                    <Button
                      type="button"
                      size="icon"
                      variant="ghost"
                      disabled={busy}
                      onClick={() =>
                        updateFRPSDraft({
                          allow_ports: frpsDraft.allow_ports.filter((_, itemIndex) => itemIndex !== index),
                        })
                      }
                      className="h-8 w-8 text-muted-foreground hover:text-destructive"
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                ))}
                {frpsDraft.allow_ports.length === 0 ? (
                  <p className="rounded-md border border-dashed border-border p-3 text-center text-xs text-muted-foreground">
                    未设定限制，客户端可使用任意未占用端口。
                  </p>
                ) : null}
              </div>
            </div>
          </div>
        ) : null}

        {/* FRPS Security & Token Rotation */}
        <div className="flex flex-wrap items-center justify-between gap-3 border-t border-border pt-4">
          <div className="flex items-center gap-3">
            <div className="rounded-md border border-success/30 bg-success/10 p-2 text-success">
              <ShieldCheck className="h-4 w-4" />
            </div>
            <div>
              <h4 className="text-xs font-semibold text-foreground">安全与 Token 轮换</h4>
              <p className="text-[11px] text-muted-foreground">
                {frps?.has_auth_token
                  ? '密钥由 Panel 安全加密保存；轮换后需同步更新所有已有 frpc 客户端。'
                  : '首次启用时自动生成随机 Token。'}
              </p>
            </div>
          </div>
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={busy || frpsDirty || !frps?.has_auth_token}
            onClick={rotateFRPSToken}
            className="gap-1 text-xs"
          >
            <KeyRound className="h-3.5 w-3.5" /> 轮换 Token
          </Button>
        </div>

        {frps?.last_error ? (
          <Alert variant="destructive">
            <AlertTitle>最近一次 FRPS 错误</AlertTitle>
            <AlertDescription>{frps.last_error}</AlertDescription>
          </Alert>
        ) : null}

        {/* Action Controls Bar */}
        <div className="flex flex-wrap items-center justify-between gap-3 border-t border-border pt-4">
          <Button
            loading={busyAction === 'save-frps'}
            disabled={busy || !frpsDirty}
            onClick={saveFRPS}
          >
            {!frps?.desired_hash && frpsDraft.enabled ? '保存配置并自动启动' : '保存并应用配置'}
          </Button>

          <div className="flex flex-wrap gap-2">
            <Button
              variant="outline"
              loading={busyAction === 'refresh-frps'}
              disabled={busy || frpsDirty}
              onClick={() => runFRPSAction('refresh')}
              className="gap-1 text-xs"
            >
              <RefreshCw className="h-3.5 w-3.5" /> 刷新状态
            </Button>
            <Button
              loading={busyAction === 'start-frps'}
              disabled={busy || frpsDirty || !frps?.desired_hash}
              onClick={() => runFRPSAction('start')}
              className="gap-1 text-xs"
            >
              <Play className="h-3.5 w-3.5" /> 启动 FRPS
            </Button>
            <Button
              variant="destructive"
              loading={busyAction === 'stop-frps'}
              disabled={busy || frpsDirty || !frps?.desired_hash}
              onClick={() => runFRPSAction('stop')}
              className="gap-1 text-xs"
            >
              <Square className="h-3.5 w-3.5" /> 停止 FRPS
            </Button>
          </div>
        </div>
      </div>

      <div className="overflow-hidden rounded-lg border border-border bg-card p-5 space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-3 border-b border-border pb-4">
          <div>
            <h3 className="flex items-center gap-2 text-sm font-semibold text-foreground">
              <RadioTower className="h-4 w-4 text-primary" />
              在线设备与端口映射
              {frps?.runtime_state === 'running' ? (
                <span className="relative flex h-2 w-2">
                  <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-success opacity-60" />
                  <span className="relative inline-flex h-2 w-2 rounded-full bg-success" />
                </span>
              ) : null}
            </h3>
            <p className="mt-1 text-xs text-muted-foreground">
              运行态直接来自节点 Agent；仅在此页面打开时自动刷新。
            </p>
          </div>
          <div className="flex items-center gap-3">
            <Button
              size="sm"
              variant="outline"
              loading={frpsMappingsLoading}
              disabled={
                frps?.runtime_state !== 'running' ||
                !node.capabilities?.includes('frps-mappings-v1')
              }
              onClick={loadFRPSMappings}
              className="h-8 gap-1"
            >
              <RefreshCw className="h-3.5 w-3.5" /> 刷新在线设备
            </Button>
          </div>
        </div>

        {!node.capabilities?.includes('frps-mappings-v1') ? (
          <div className="py-5 text-sm text-warning font-medium">
            当前 Agent 不支持在线映射查询，请升级到包含该能力的版本。
          </div>
        ) : frps?.runtime_state !== 'running' ? (
          <div className="flex items-center gap-3 py-6 text-sm text-muted-foreground">
            <Server className="h-5 w-5 text-muted-foreground" />
            FRPS 启动后会在这里显示已连接设备和映射关系。
          </div>
        ) : (
          <div className="space-y-5">
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
              <div className="border-l-2 border-primary bg-muted/40 px-3 py-2">
                <span className="block font-mono text-xl text-foreground">
                  {frpsMappings?.clients?.length ?? 0}
                </span>
                <span className="text-[10px] uppercase tracking-[0.16em] text-muted-foreground">在线设备</span>
              </div>
              <div className="border-l-2 border-success bg-muted/40 px-3 py-2">
                <span className="block font-mono text-xl text-foreground">
                  {frpsMappings?.mappings?.length ?? 0}
                </span>
                <span className="text-[10px] uppercase tracking-[0.16em] text-muted-foreground">在线映射</span>
              </div>
              <div className="border-l-2 border-warning bg-muted/40 px-3 py-2">
                <span className="block font-mono text-xl text-foreground">
                  {(frpsMappings?.mappings ?? []).reduce(
                    (total, mapping) => total + (mapping.current_connections ?? 0),
                    0,
                  )}
                </span>
                <span className="text-[10px] uppercase tracking-[0.16em] text-muted-foreground">活动连接</span>
              </div>
              <div className="border-l-2 border-border bg-muted/40 px-3 py-2">
                <span className="block font-mono text-sm leading-7 text-foreground">
                  {formatBytes((frpsMappings?.mappings ?? []).reduce(
                    (total, mapping) =>
                      total +
                      (mapping.traffic_in_bytes ?? 0) +
                      (mapping.traffic_out_bytes ?? 0),
                    0,
                  ))}
                </span>
                <span className="text-[10px] uppercase tracking-[0.16em] text-muted-foreground">今日流量</span>
              </div>
            </div>

            {frpsMappingsError ? (
              <div className="rounded border border-destructive bg-destructive/10 px-3 py-2 text-xs text-destructive">
                自动刷新失败：{frpsMappingsError}
              </div>
            ) : null}

            <div className="space-y-2">
              <div className="flex items-center gap-2 text-[10px] font-medium uppercase tracking-[0.16em] text-muted-foreground">
                <Network className="h-3.5 w-3.5" /> Clients
              </div>
              {(frpsMappings?.clients?.length ?? 0) > 0 ? (
                <div className="grid gap-2 sm:grid-cols-2">
                  {(frpsMappings?.clients ?? []).map((client) => (
                    <div
                      key={client.key || client.run_id}
                      className="flex items-center justify-between gap-3 rounded border border-border bg-background px-3 py-2.5"
                    >
                      <div className="min-w-0">
                        <div className="truncate font-mono text-xs text-foreground">
                          {client.client_id || client.run_id || 'anonymous'}
                        </div>
                        <div className="mt-1 truncate text-[11px] text-muted-foreground">
                          {client.hostname || '未报告主机名'} · {client.client_ip || '未知来源'}
                        </div>
                      </div>
                      <div className="shrink-0 text-right">
                        <span className="block text-[10px] text-success font-semibold">ONLINE</span>
                        <span className="font-mono text-[10px] text-muted-foreground">
                          {client.version || 'frpc'}
                        </span>
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <div className="rounded border border-dashed border-border px-3 py-4 text-center text-xs text-muted-foreground">
                  暂无在线 FRPC 设备
                </div>
              )}
            </div>

            <div className="space-y-2">
              <div className="flex items-center gap-2 text-[10px] font-medium uppercase tracking-[0.16em] text-muted-foreground">
                <ArrowRight className="h-3.5 w-3.5" /> Live mappings
              </div>
              {(frpsMappings?.mappings?.length ?? 0) > 0 ? (
                <div className="space-y-2">
                  {(frpsMappings?.mappings ?? []).map((mapping) => (
                    <div
                      key={`${mapping.type}:${mapping.name}`}
                      className="rounded border border-border bg-background px-3 py-3 transition-colors hover:border-primary/50"
                    >
                      <div className="flex flex-wrap items-center justify-between gap-3">
                        <div className="flex min-w-0 items-center gap-2">
                          <Badge variant="outline" className="font-mono uppercase text-primary">
                            {mapping.type}
                          </Badge>
                          <span className="truncate font-mono text-xs font-semibold text-foreground">
                            {mapping.name}
                          </span>
                        </div>
                        <div className="flex items-center gap-3 font-mono text-xs text-muted-foreground">
                          <span>{mapping.current_connections ?? 0} conn</span>
                          <span>↓ {formatBytes(mapping.traffic_in_bytes)}</span>
                          <span>↑ {formatBytes(mapping.traffic_out_bytes)}</span>
                        </div>
                      </div>
                      <div className="mt-2 grid gap-1 border-t border-border pt-2 text-xs font-mono text-muted-foreground sm:grid-cols-2">
                        <div>公网入口：<span className="text-foreground">{frpsMappingExposure(mapping, node.public_address || node.address || '')}</span></div>
                        <div>后端服务：<span className="text-foreground">{frpsMappingBackend(mapping)}</span></div>
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <div className="rounded border border-dashed border-border px-3 py-4 text-center text-xs text-muted-foreground">
                  暂无建立好的端口映射
                </div>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
