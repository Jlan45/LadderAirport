import { useEffect, useState } from 'react'
import { toast, type ToastMessage } from '@/lib/toast'
import { X, CheckCircle, AlertCircle, Info, AlertTriangle } from 'lucide-react'

export function Toaster() {
  const [toasts, setToasts] = useState<ToastMessage[]>([])

  useEffect(() => {
    return toast.subscribe(setToasts)
  }, [])

  return (
    <div className="fixed top-4 right-4 z-50 flex flex-col gap-2 w-full max-w-sm pointer-events-none">
      {toasts.map((t) => {
        const Icon = {
          success: CheckCircle,
          error: AlertCircle,
          warning: AlertTriangle,
          info: Info,
        }[t.type]

        const typeStyles = {
          success: 'border-success/30 bg-card text-foreground shadow-lg',
          error: 'border-destructive/30 bg-card text-foreground shadow-lg',
          warning: 'border-warning/30 bg-card text-foreground shadow-lg',
          info: 'border-border bg-card text-foreground shadow-lg',
        }[t.type]

        const iconStyles = {
          success: 'text-success',
          error: 'text-destructive',
          warning: 'text-warning',
          info: 'text-info',
        }[t.type]

        return (
          <div
            key={t.id}
            className={`flex items-start gap-3 p-4 rounded-lg border backdrop-blur-md transition-all duration-300 ease-in-out pointer-events-auto transform translate-y-0 opacity-100 hover:translate-x-[-4px] ${typeStyles}`}
            role="status"
          >
            <Icon className={`h-5 w-5 shrink-0 mt-0.5 ${iconStyles}`} />
            <div className="flex-1 text-sm font-medium pr-4">{t.message}</div>
            <button
              onClick={() => toast.dismiss(t.id)}
              className="text-muted-foreground hover:text-foreground transition-colors shrink-0 cursor-pointer"
              aria-label="关闭"
            >
              <X className="h-4 w-4" />
            </button>
          </div>
        )
      })}
    </div>
  )
}
