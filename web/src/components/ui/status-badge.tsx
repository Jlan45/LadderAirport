import { Badge, type BadgeProps } from './badge'

export interface StatusBadgeProps {
  value: string
  label?: string
  showDot?: boolean
  className?: string
}

export function StatusBadge({ value, label, showDot = true, className }: StatusBadgeProps) {
  const normalized = value ? value.toLowerCase() : ''

  let variant: BadgeProps['variant'] = 'secondary'
  let defaultLabel = value

  switch (normalized) {
    case 'online':
    case 'healthy':
    case 'enabled':
    case 'active':
    case 'ready':
    case 'issued':
    case 'valid':
    case 'success':
    case 'ok':
      variant = 'success'
      defaultLabel = label || (normalized === 'online' ? '在线' : normalized === 'enabled' ? '已启用' : normalized === 'healthy' ? '健康' : normalized === 'active' ? '有效' : normalized === 'issued' ? '已签发' : '正常')
      break

    case 'offline':
    case 'error':
    case 'failed':
    case 'destructive':
    case 'revoked':
    case 'degraded':
      variant = 'destructive'
      defaultLabel = label || (normalized === 'offline' ? '离线' : normalized === 'revoked' ? '已吊销' : normalized === 'degraded' ? '降级' : normalized === 'error' ? '异常' : '失败')
      break

    case 'warning':
    case 'outdated':
    case 'deploying':
    case 'pending':
    case 'syncing':
    case 'expired':
    case 'renewing':
      variant = 'warning'
      defaultLabel = label || (normalized === 'outdated' ? '可升级' : normalized === 'deploying' ? '部署中' : normalized === 'expired' ? '已过期' : normalized === 'pending' ? '等待中' : '处理中')
      break

    case 'disabled':
    case 'inactive':
    case 'stopped':
    case 'replaced':
    case 'idle':
      variant = 'secondary'
      defaultLabel = label || (normalized === 'disabled' ? '已停用' : normalized === 'replaced' ? '已替换' : '已关闭')
      break

    default:
      variant = 'outline'
      defaultLabel = label || value
      break
  }

  const dotColorClass = {
    success: 'bg-success',
    destructive: 'bg-destructive',
    warning: 'bg-warning',
    info: 'bg-info',
    secondary: 'bg-muted-foreground',
    default: 'bg-primary',
    outline: 'bg-muted-foreground',
  }[variant || 'secondary']

  return (
    <Badge variant={variant} className={`gap-1.5 ${className || ''}`}>
      {showDot && <span className={`h-1.5 w-1.5 rounded-full ${dotColorClass}`} />}
      <span>{defaultLabel}</span>
    </Badge>
  )
}
