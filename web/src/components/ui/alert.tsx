import * as React from 'react'
import { cn } from '@/lib/utils'

export const Alert = React.forwardRef<
  HTMLDivElement,
  React.HTMLAttributes<HTMLDivElement> & { variant?: 'default' | 'destructive' | 'warning' | 'success' }
>(({ className, variant = 'default', ...props }, ref) => (
  <div
    ref={ref}
    role="alert"
    className={cn(
      'relative w-full rounded-lg border px-4 py-3 text-sm [&>svg+div]:translate-y-[-3px] [&>svg]:absolute [&>svg]:left-4 [&>svg]:top-4 [&>svg]:text-foreground [&>svg~*]:pl-7',
      {
        'bg-background text-foreground': variant === 'default',
        'border-red-900/50 bg-red-950/20 text-red-500 [&>svg]:text-red-500': variant === 'destructive',
        'border-amber-900/50 bg-amber-950/20 text-amber-500 [&>svg]:text-amber-500': variant === 'warning',
        'border-emerald-900/50 bg-emerald-950/20 text-emerald-500 [&>svg]:text-emerald-500': variant === 'success',
      },
      className
    )}
    {...props}
  />
))
Alert.displayName = 'Alert'

export const AlertTitle = React.forwardRef<
  HTMLParagraphElement,
  React.HTMLAttributes<HTMLHeadingElement>
>(({ className, ...props }, ref) => (
  <h5
    ref={ref}
    className={cn('mb-1 font-semibold leading-none tracking-tight', className)}
    {...props}
  />
))
AlertTitle.displayName = 'AlertTitle'

export const AlertDescription = React.forwardRef<
  HTMLParagraphElement,
  React.HTMLAttributes<HTMLParagraphElement>
>(({ className, ...props }, ref) => (
  <div
    ref={ref}
    className={cn('text-sm opacity-90 [&_p]:leading-relaxed', className)}
    {...props}
  />
))
AlertDescription.displayName = 'AlertDescription'
