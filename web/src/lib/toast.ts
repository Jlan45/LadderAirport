type ToastType = 'success' | 'error' | 'warning' | 'info'

export interface ToastMessage {
  id: string
  type: ToastType
  message: string
  duration?: number
}

type Listener = (toasts: ToastMessage[]) => void
let toasts: ToastMessage[] = []
const listeners = new Set<Listener>()

function emit() {
  listeners.forEach((l) => l([...toasts]))
}

export const toast = {
  show(type: ToastType, message: string, duration = 3000) {
    const id = Math.random().toString(36).substring(2, 9)
    const toastItem: ToastMessage = { id, type, message, duration }
    toasts.push(toastItem)
    emit()
    if (duration > 0) {
      setTimeout(() => {
        toasts = toasts.filter((t) => t.id !== id)
        emit()
      }, duration)
    }
    return id
  },
  success(message: string, duration = 3000) {
    return this.show('success', message, duration)
  },
  error(message: string, duration = 4000) {
    return this.show('error', message, duration)
  },
  warning(message: string, duration = 3500) {
    return this.show('warning', message, duration)
  },
  info(message: string, duration = 3000) {
    return this.show('info', message, duration)
  },
  dismiss(id: string) {
    toasts = toasts.filter((t) => t.id !== id)
    emit()
  },
  subscribe(listener: Listener) {
    listeners.add(listener)
    listener([...toasts])
    return () => {
      listeners.delete(listener)
    }
  },
}
