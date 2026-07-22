import { Card, CardContent } from './ui/card'
import type { FleetOverview } from '../api/client'
import { Server, Wifi, WifiOff, Cpu } from 'lucide-react'

export default function StatsBar({
  ov,
  loading = false,
  unavailable = false,
}: {
  ov: FleetOverview | null
  loading?: boolean
  unavailable?: boolean
}) {
  const placeholderState = unavailable ? '暂不可用' : '加载中'
  const isBusy = loading && !ov

  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4 mb-6" aria-busy={isBusy}>
      {/* Card 1: Total Nodes */}
      <Card className="relative overflow-hidden bg-card/50 backdrop-blur-sm border-border/80">
        <CardContent className="p-6">
          <div className="flex items-center justify-between space-y-0 pb-2">
            <span className="text-sm font-medium text-muted-foreground">节点总数</span>
            <Server className="h-4 w-4 text-zinc-400" />
          </div>
          <div className="flex items-baseline space-x-2">
            <span className="text-3xl font-bold tracking-tight">
              {ov ? ov.total_nodes : <span className="text-zinc-500">—</span>}
            </span>
          </div>
          {!ov && <p className="text-xs text-muted-foreground mt-1">{placeholderState}</p>}
        </CardContent>
      </Card>

      {/* Card 2: Online Nodes */}
      <Card className="relative overflow-hidden bg-card/50 backdrop-blur-sm border-border/80">
        <div className="absolute top-0 left-0 right-0 h-[2px] bg-emerald-500/20" />
        <CardContent className="p-6">
          <div className="flex items-center justify-between space-y-0 pb-2">
            <span className="text-sm font-medium text-muted-foreground">在线节点</span>
            <Wifi className="h-4 w-4 text-emerald-500" />
          </div>
          <div className="flex items-baseline space-x-2">
            <span className="text-3xl font-bold tracking-tight text-emerald-500">
              {ov ? ov.online_nodes : <span className="text-zinc-500">—</span>}
            </span>
          </div>
          {!ov && <p className="text-xs text-muted-foreground mt-1">{placeholderState}</p>}
        </CardContent>
      </Card>

      {/* Card 3: Offline Nodes */}
      <Card className="relative overflow-hidden bg-card/50 backdrop-blur-sm border-border/80">
        <div className="absolute top-0 left-0 right-0 h-[2px] bg-red-500/20" />
        <CardContent className="p-6">
          <div className="flex items-center justify-between space-y-0 pb-2">
            <span className="text-sm font-medium text-muted-foreground">非在线（含待安装）</span>
            <WifiOff className="h-4 w-4 text-red-500" />
          </div>
          <div className="flex items-baseline space-x-2">
            <span className="text-3xl font-bold tracking-tight text-red-500">
              {ov ? ov.offline_nodes : <span className="text-zinc-500">—</span>}
            </span>
          </div>
          {!ov && <p className="text-xs text-muted-foreground mt-1">{placeholderState}</p>}
        </CardContent>
      </Card>

      {/* Card 4: Running Core Nodes */}
      <Card className="relative overflow-hidden bg-card/50 backdrop-blur-sm border-border/80">
        <div className="absolute top-0 left-0 right-0 h-[2px] bg-blue-500/20" />
        <CardContent className="p-6">
          <div className="flex items-center justify-between space-y-0 pb-2">
            <span className="text-sm font-medium text-muted-foreground">核心运行中</span>
            <Cpu className="h-4 w-4 text-blue-500" />
          </div>
          <div className="flex items-baseline space-x-2">
            <span className="text-3xl font-bold tracking-tight text-blue-500">
              {ov ? ov.running_nodes : <span className="text-zinc-500">—</span>}
            </span>
          </div>
          {!ov && <p className="text-xs text-muted-foreground mt-1">{placeholderState}</p>}
        </CardContent>
      </Card>
    </div>
  )
}
