import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from './dialog'
import { Button } from './button'

export interface ConfirmModalProps {
  open: boolean
  title?: string
  description?: string
  confirmText?: string
  cancelText?: string
  variant?: 'destructive' | 'default'
  confirmVariant?: 'destructive' | 'default'
  loading?: boolean
  onConfirm: () => void
  onCancel: () => void
}

export function ConfirmModal({
  open,
  title = '确认执行此操作？',
  description,
  confirmText = '确定',
  cancelText = '取消',
  variant,
  confirmVariant,
  loading = false,
  onConfirm,
  onCancel,
}: ConfirmModalProps) {
  const finalVariant = confirmVariant || variant || 'destructive'
  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v && !loading) onCancel() }}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description ? <DialogDescription className="mt-1">{description}</DialogDescription> : null}
        </DialogHeader>

        <DialogFooter className="mt-6">
          <Button type="button" variant="outline" disabled={loading} onClick={onCancel}>
            {cancelText}
          </Button>
          <Button type="button" variant={finalVariant} loading={loading} onClick={onConfirm}>
            {confirmText}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
