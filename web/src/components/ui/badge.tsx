import * as React from 'react'
import { cn } from '@/lib/utils'

export interface BadgeProps extends React.HTMLAttributes<HTMLDivElement> {
  variant?: 'default' | 'secondary' | 'destructive' | 'outline' | 'success' | 'info' | 'warning'
}

export function Badge({ className, variant = 'default', ...props }: BadgeProps) {
  return (
    <div
      className={cn(
        'inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold transition-colors focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2',
        {
          'border-transparent bg-primary text-primary-foreground shadow': variant === 'default',
          'border-transparent bg-secondary text-secondary-foreground': variant === 'secondary',
          'bg-destructive/15 text-destructive border-destructive/30': variant === 'destructive',
          'bg-success/15 text-success-foreground border-success/30': variant === 'success',
          'bg-info/15 text-info-foreground border-info/30': variant === 'info',
          'bg-warning/15 text-warning-foreground border-warning/30': variant === 'warning',
          'text-foreground border-border': variant === 'outline',
        },
        className
      )}
      {...props}
    />
  )
}
