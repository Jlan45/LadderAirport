import { Card, Statistic } from 'tdesign-react'
import type { FleetOverview } from '../api/client'

export default function StatsBar({ ov }: { ov: FleetOverview | null }) {
  return (
    <div className="la-stat-grid">
      <Card bordered className="la-stat-card" size="small">
        <Statistic title="节点总数" value={ov?.total_nodes ?? 0} />
      </Card>
      <Card bordered className="la-stat-card" size="small">
        <Statistic title="在线" value={ov?.online_nodes ?? 0} trend="increase" color="green" />
      </Card>
      <Card bordered className="la-stat-card" size="small">
        <Statistic title="离线/未知" value={ov?.offline_nodes ?? 0} color="red" />
      </Card>
      <Card bordered className="la-stat-card" size="small">
        <Statistic title="核心运行中" value={ov?.running_nodes ?? 0} color="blue" />
      </Card>
    </div>
  )
}
