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
          'border-transparent bg-red-950/80 text-red-400 border border-red-900/50': variant === 'destructive',
          'border-transparent bg-emerald-950/80 text-emerald-400 border border-emerald-900/50': variant === 'success',
          'border-transparent bg-blue-950/80 text-blue-400 border border-blue-900/50': variant === 'info',
          'border-transparent bg-amber-950/80 text-amber-400 border border-amber-900/50': variant === 'warning',
          'text-foreground': variant === 'outline',
        },
        className
      )}
      {...props}
    />
  )
}
