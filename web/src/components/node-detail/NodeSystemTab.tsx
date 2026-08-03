import { useCallback, useEffect, useRef, useState } from 'react'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import {
  Activity,
  ArrowDown,
  ArrowUp,
  Cpu,
  HardDrive,
  MemoryStick,
  RefreshCw,
} from 'lucide-react'
import {
  ApiError,
  getNodeBBR,
  getNodeSysmetrics,
  setNodeBBR,
  type NodeBBRStatus,
  type NodeSysmetrics,
} from '@/api/client'
import { formatBytes, formatTime } from '@/lib/nodeDisplay'
import { toast } from '@/lib/toast'

const POLL_INTERVAL_MS = 5000

export interface NodeSystemTabProps {
  nodeId: string
}

/** 系统状态：主机 CPU/内存/磁盘/实时速率 + BBR 拥塞控制开关。挂载时加载一次并每 5s 轮询。 */
export function NodeSystemTab({ nodeId }: NodeSystemTabProps) {
  const [sysmetrics, setSysmetrics] = useState<NodeSysmetrics | null>(null)
  const [sysmetricsError, setSysmetricsError] = useState('')
  const [sysmetricsUnsupported, setSysmetricsUnsupported] = useState(false)
  const [bbr, setBBR] = useState<NodeBBRStatus | null>(null)
  const [bbrError, setBBRError] = useState('')
  const [bbrBusy, setBBRBusy] = useState(false)
  const [loading, setLoading] = useState(true)

  const inFlightRef = useRef(false)
  const unsupportedRef = useRef(false)

  const loadSysmetrics = useCallback(async () => {
    if (inFlightRef.current || unsupportedRef.current) return
    inFlightRef.current = true
    try {
      const res = await getNodeSysmetrics(nodeId)
      setSysmetrics(res)
      setSysmetricsError('')
    } catch (err) {
      const message = err instanceof Error ? err.message : '系统状态拉取失败'
      setSysmetricsError(message)
      if (err instanceof ApiError && err.status === 400) {
        // 旧版本 agent 不支持该功能，停止轮询并灰化展示。
        unsupportedRef.current = true
        setSysmetricsUnsupported(true)
      }
    } finally {
      inFlightRef.current = false
    }
  }, [nodeId])

  const loadBBR = useCallback(async () => {
    try {
      const res = await getNodeBBR(nodeId)
      setBBR(res)
      setBBRError('')
    } catch (err) {
      setBBRError(err instanceof Error ? err.message : 'BBR 状态拉取失败')
    }
  }, [nodeId])

  useEffect(() => {
    let cancelled = false
    void Promise.all([loadSysmetrics(), loadBBR()]).finally(() => {
      if (!cancelled) setLoading(false)
    })
    const timer = window.setInterval(() => void loadSysmetrics(), POLL_INTERVAL_MS)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [loadSysmetrics, loadBBR])

  async function toggleBBR(enabled: boolean) {
    if (!bbr || bbrBusy) return
    const previous = bbr
    setBBR({ ...bbr, bbr_enabled: enabled })
    setBBRBusy(true)
    try {
      const res = await setNodeBBR(nodeId, enabled)
      toast.success(res.message || (enabled ? 'BBR 已启用' : 'BBR 已关闭'))
      await loadBBR()
    } catch (err) {
      setBBR(previous)
      toast.error(err instanceof Error ? err.message : 'BBR 设置失败')
    } finally {
      setBBRBusy(false)
    }
  }

  const memoryPercent = sysmetrics && sysmetrics.memory_total_bytes > 0
    ? Math.min(100, (sysmetrics.memory_used_bytes / sysmetrics.memory_total_bytes) * 100)
    : 0
  const diskPercent = sysmetrics && sysmetrics.disk_total_bytes > 0
    ? Math.min(100, (sysmetrics.disk_used_bytes / sysmetrics.disk_total_bytes) * 100)
    : 0

  return (
    <div className="space-y-6">
      {/* System metrics */}
      <div className="space-y-4 rounded-lg border border-border bg-card/40 p-5">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="space-y-1">
            <h3 className="text-sm font-semibold text-foreground flex items-center gap-1.5">
              <Activity className="h-4 w-4 text-muted-foreground" />
              系统状态
            </h3>
            <p className="text-xs text-muted-foreground">
              每 5 秒自动刷新
              {sysmetrics?.collected_at_unix
                ? ` · 采集于 ${formatTime(sysmetrics.collected_at_unix)}`
                : ''}
            </p>
          </div>
          <Button
            size="sm"
            variant="outline"
            disabled={sysmetricsUnsupported}
            onClick={() => void loadSysmetrics()}
            className="h-8 border-border text-foreground hover:bg-muted gap-1.5"
          >
            <RefreshCw className="h-3.5 w-3.5" /> 刷新
          </Button>
        </div>

        {sysmetricsUnsupported ? (
          <div className="opacity-60">
            <Alert variant="warning">
              <AlertDescription className="text-xs">{sysmetricsError}</AlertDescription>
            </Alert>
          </div>
        ) : sysmetricsError && !sysmetrics ? (
          <Alert variant="destructive">
            <AlertDescription className="text-xs">{sysmetricsError}</AlertDescription>
          </Alert>
        ) : loading && !sysmetrics ? (
          <div className="flex items-center justify-center py-10 gap-2 text-sm text-muted-foreground">
            <RefreshCw className="h-4 w-4 animate-spin" /> 正在加载系统状态…
          </div>
        ) : sysmetrics ? (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            <div className="bg-background p-3 rounded-lg border border-border space-y-2">
              <span className="text-[10px] text-muted-foreground uppercase tracking-wider flex items-center gap-1">
                <Cpu className="h-3 w-3" /> CPU 使用率
              </span>
              <span className="text-lg font-bold text-foreground block">
                {sysmetrics.cpu_percent.toFixed(1)}%
              </span>
              <div className="h-1.5 rounded-full bg-muted overflow-hidden">
                <div
                  className="h-full rounded-full bg-primary transition-all"
                  style={{ width: `${Math.min(100, Math.max(0, sysmetrics.cpu_percent))}%` }}
                />
              </div>
            </div>

            <div className="bg-background p-3 rounded-lg border border-border space-y-2">
              <span className="text-[10px] text-muted-foreground uppercase tracking-wider flex items-center gap-1">
                <MemoryStick className="h-3 w-3" /> 内存
              </span>
              <span className="text-lg font-bold text-foreground block">
                {formatBytes(sysmetrics.memory_used_bytes)}
                <span className="text-xs font-normal text-muted-foreground">
                  {' '}/ {formatBytes(sysmetrics.memory_total_bytes)} ({memoryPercent.toFixed(0)}%)
                </span>
              </span>
              <div className="h-1.5 rounded-full bg-muted overflow-hidden">
                <div
                  className="h-full rounded-full bg-primary transition-all"
                  style={{ width: `${memoryPercent}%` }}
                />
              </div>
            </div>

            <div className="bg-background p-3 rounded-lg border border-border space-y-2">
              <span className="text-[10px] text-muted-foreground uppercase tracking-wider flex items-center gap-1">
                <HardDrive className="h-3 w-3" /> 磁盘
              </span>
              <span className="text-lg font-bold text-foreground block">
                {formatBytes(sysmetrics.disk_used_bytes)}
                <span className="text-xs font-normal text-muted-foreground">
                  {' '}/ {formatBytes(sysmetrics.disk_total_bytes)} ({diskPercent.toFixed(0)}%)
                </span>
              </span>
              <div className="h-1.5 rounded-full bg-muted overflow-hidden">
                <div
                  className="h-full rounded-full bg-primary transition-all"
                  style={{ width: `${diskPercent}%` }}
                />
              </div>
            </div>

            <div className="bg-background p-3 rounded-lg border border-border">
              <span className="text-[10px] text-muted-foreground uppercase tracking-wider flex items-center gap-1">
                <ArrowUp className="h-3 w-3" /> 上行速率
              </span>
              <span className="text-lg font-bold text-foreground mt-1 block">
                {formatBytes(sysmetrics.uplink_bps)}/s
              </span>
            </div>

            <div className="bg-background p-3 rounded-lg border border-border">
              <span className="text-[10px] text-muted-foreground uppercase tracking-wider flex items-center gap-1">
                <ArrowDown className="h-3 w-3" /> 下行速率
              </span>
              <span className="text-lg font-bold text-foreground mt-1 block">
                {formatBytes(sysmetrics.downlink_bps)}/s
              </span>
            </div>
          </div>
        ) : null}
      </div>

      {/* BBR congestion control */}
      <div className="space-y-4 rounded-lg border border-border bg-card/40 p-5">
        <div className="space-y-1">
          <h3 className="text-sm font-semibold text-foreground">BBR 拥塞控制</h3>
          <p className="text-xs text-muted-foreground">
            开启 BBR 可提升高延迟链路吞吐；修改后即时生效，无需重启节点。
          </p>
        </div>

        {bbrError ? (
          <div className="opacity-60 space-y-3">
            <Alert variant="warning">
              <AlertDescription className="text-xs">{bbrError}</AlertDescription>
            </Alert>
            <div className="flex items-center justify-between rounded-md border border-border bg-background px-3 py-2 pointer-events-none">
              <span className="text-xs text-foreground">启用 BBR</span>
              <Switch checked={false} disabled />
            </div>
          </div>
        ) : loading && !bbr ? (
          <div className="flex items-center justify-center py-6 gap-2 text-sm text-muted-foreground">
            <RefreshCw className="h-4 w-4 animate-spin" /> 正在加载 BBR 状态…
          </div>
        ) : bbr ? (
          <div className="space-y-3">
            <div className="flex flex-wrap items-center gap-1.5">
              <Badge variant={bbr.bbr_available ? 'success' : 'secondary'}>
                {bbr.bbr_available ? '内核支持 BBR' : '内核不支持 BBR'}
              </Badge>
              <Badge variant={bbr.bbr_enabled ? 'success' : 'outline'}>
                {bbr.bbr_enabled ? '已启用' : '未启用'}
              </Badge>
              <span className="text-xs text-muted-foreground font-mono">
                拥塞算法 {bbr.current_congestion_control || '—'} · 队列 {bbr.current_qdisc || '—'}
              </span>
            </div>

            {(bbr.supported_controls?.length ?? 0) > 0 && (
              <p className="text-[11px] text-muted-foreground font-mono">
                支持的算法：{bbr.supported_controls.join(', ')}
              </p>
            )}

            <div className="flex items-center justify-between rounded-md border border-border bg-background px-3 py-2">
              <div>
                <span className="text-xs text-foreground block">启用 BBR</span>
                {!bbr.bbr_available && (
                  <span className="text-[10px] text-muted-foreground">当前内核不支持 BBR，无法开启</span>
                )}
              </div>
              <Switch
                checked={bbr.bbr_enabled}
                disabled={bbrBusy || !bbr.bbr_available}
                onCheckedChange={(value) => void toggleBBR(value)}
              />
            </div>
          </div>
        ) : null}
      </div>
    </div>
  )
}
