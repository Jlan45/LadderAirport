/** Shared display helpers for node status, metrics, and tasks. */

export function formatBytes(n?: number): string {
  if (n == null || !Number.isFinite(n) || n < 0) return n == null ? '—' : '0 B'
  if (n === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v < 10 && i > 0 ? v.toFixed(1) : i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

export function formatTime(unix?: number): string {
  if (!unix) return '—'
  return new Date(unix * 1000).toLocaleString()
}

export function statusLabel(s?: string): string {
  switch (s) {
    case 'online':
    case 'running': // legacy: apply once wrote runtime into status
      return '在线'
    case 'unreachable':
      return '离线'
    case 'unauthorized':
      return '鉴权失败'
    case 'pending':
      return '待安装'
    case 'unknown':
      return '未知'
    default:
      return s || '未知'
  }
}

export function statusClass(s?: string): string {
  if (s === 'online' || s === 'running') return 'status status-success'
  if (s === 'unreachable' || s === 'unauthorized') return 'status status-failed'
  if (s === 'pending') return 'status status-pending'
  return 'status'
}

export function isOnlineStatus(s?: string): boolean {
  return s === 'online' || s === 'running'
}

export function runtimeLabel(s?: string): string {
  switch (s) {
    case 'running':
      return '运行中'
    case 'stopped':
      return '已停止'
    case 'error':
      return '错误'
    default:
      return s || '未知'
  }
}

export function taskKindLabel(kind: string): string {
  switch (kind) {
    case 'apply':
      return '应用配置'
    case 'start':
      return '启动服务'
    case 'stop':
      return '停止服务'
    default:
      return kind
  }
}

export function taskStatusLabel(status: string): string {
  switch (status) {
    case 'online':
      return '在线'
    case 'unreachable':
      return '无法连接'
    case 'success':
      return '成功'
    case 'failed':
      return '失败'
    case 'pending':
      return '等待中'
    case 'running':
      return '进行中'
    default:
      return status || '未知'
  }
}
