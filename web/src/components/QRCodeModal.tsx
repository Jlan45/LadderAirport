import { useEffect, useState } from 'react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Copy, Check, QrCode } from 'lucide-react'
import QRCode from 'qrcode'
import { copyText } from '../lib/clipboard'
import { toast } from '../lib/toast'

interface QRCodeModalProps {
  open: boolean
  onClose: () => void
  title: string
  url: string
}

export function QRCodeModal({ open, onClose, title, url }: QRCodeModalProps) {
  const [dataUrl, setDataUrl] = useState('')
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (!open || !url) return
    let active = true
    void QRCode.toDataURL(url, {
      width: 280,
      margin: 2,
      color: {
        dark: '#000000',
        light: '#ffffff',
      },
    }).then((res) => {
      if (active) setDataUrl(res)
    })
    return () => {
      active = false
    }
  }, [open, url])

  async function handleCopy() {
    try {
      await copyText(url)
      setCopied(true)
      toast.success('已复制订阅链接')
      setTimeout(() => setCopied(false), 2000)
    } catch {
      toast.error('复制失败')
    }
  }

  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="sm:max-w-md bg-zinc-950 border-zinc-900 text-zinc-100 p-6 space-y-5">
        <DialogHeader className="space-y-1 text-center sm:text-left">
          <DialogTitle className="text-lg font-bold flex items-center gap-2">
            <QrCode className="h-5 w-5 text-cyan-400" />
            订阅二维码 · {title}
          </DialogTitle>
          <p className="text-xs text-zinc-400">
            使用手机客户端（Clash / sing-box / Shadowrocket / PassWall 等）直接扫描下方二维码添加订阅。
          </p>
        </DialogHeader>

        <div className="flex flex-col items-center justify-center space-y-4 py-2">
          <div className="p-3 bg-white rounded-xl shadow-lg border border-zinc-200 flex items-center justify-center">
            {dataUrl ? (
              <img src={dataUrl} alt="Subscription QR Code" className="w-56 h-56 object-contain" />
            ) : (
              <div className="w-56 h-56 flex items-center justify-center text-xs text-zinc-400">
                正在生成二维码…
              </div>
            )}
          </div>

          <div className="w-full space-y-2">
            <div className="p-2.5 rounded bg-zinc-900 border border-zinc-800 text-[11px] font-mono text-zinc-300 break-all select-all text-center">
              {url}
            </div>
            <Button
              onClick={() => void handleCopy()}
              className="w-full gap-2 bg-zinc-900 border border-zinc-800 hover:bg-zinc-800 text-zinc-200"
            >
              {copied ? <Check className="h-4 w-4 text-emerald-500" /> : <Copy className="h-4 w-4" />}
              {copied ? '已复制订阅链接' : '复制完整订阅链接'}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
