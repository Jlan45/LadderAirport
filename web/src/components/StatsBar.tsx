import type { FleetOverview } from '../api/client'

export default function StatsBar({ ov }: { ov: FleetOverview | null }) {
  return (
    <div className="stat-grid">
      <div className="stat-card">
        <div className="stat-label">节点总数</div>
        <div className="stat-value">{ov?.total_nodes ?? 0}</div>
      </div>
      <div className="stat-card stat-ok">
        <div className="stat-label">在线</div>
        <div className="stat-value">{ov?.online_nodes ?? 0}</div>
      </div>
      <div className="stat-card stat-warn">
        <div className="stat-label">离线/未知</div>
        <div className="stat-value">{ov?.offline_nodes ?? 0}</div>
      </div>
      <div className="stat-card">
        <div className="stat-label">核心运行中</div>
        <div className="stat-value">{ov?.running_nodes ?? 0}</div>
      </div>
    </div>
  )
}
