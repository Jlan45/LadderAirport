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
          success: 'border-emerald-500/30 bg-emerald-950/90 text-emerald-300 shadow-emerald-950/20',
          error: 'border-red-500/30 bg-red-950/90 text-red-300 shadow-red-950/20',
          warning: 'border-amber-500/30 bg-amber-950/90 text-amber-300 shadow-amber-950/20',
          info: 'border-zinc-500/30 bg-zinc-900/90 text-zinc-300 shadow-zinc-950/20',
        }[t.type]

        return (
          <div
            key={t.id}
            className={`flex items-start gap-3 p-4 rounded-lg border shadow-lg backdrop-blur-md transition-all duration-300 ease-in-out pointer-events-auto transform translate-y-0 opacity-100 hover:translate-x-[-4px] ${typeStyles}`}
            role="status"
          >
            <Icon className="h-5 w-5 shrink-0 mt-0.5" />
            <div className="flex-1 text-sm font-medium pr-4">{t.message}</div>
            <button
              onClick={() => toast.dismiss(t.id)}
              className="text-zinc-400 hover:text-zinc-200 transition-colors shrink-0"
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
