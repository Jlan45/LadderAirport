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

interface NodePairingQRModalProps {
  open: boolean
  onClose: () => void
  nodeName: string
  panelUrl: string
  nodeId: string
  token: string
}

export function NodePairingQRModal({
  open,
  onClose,
  nodeName,
  panelUrl,
  nodeId,
  token,
}: NodePairingQRModalProps) {
  const [dataUrl, setDataUrl] = useState('')
  const [copied, setCopied] = useState(false)

  const payload = JSON.stringify(
    {
      panel_url: panelUrl,
      node_id: nodeId,
      token: token,
    },
    null,
    2
  )

  useEffect(() => {
    if (!open || !panelUrl || !nodeId || !token) return
    let active = true
    const qrPayload = JSON.stringify({
      panel_url: panelUrl,
      node_id: nodeId,
      token: token,
    })

    void QRCode.toDataURL(qrPayload, {
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
  }, [open, panelUrl, nodeId, token])

  async function handleCopy() {
    try {
      await copyText(payload)
      setCopied(true)
      toast.success('已复制配对配置')
      setTimeout(() => setCopied(false), 2000)
    } catch {
      toast.error('复制失败')
    }
  }

  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="sm:max-w-md p-6 space-y-5 shadow-xl">
        <DialogHeader className="space-y-1 text-center sm:text-left">
          <DialogTitle className="text-lg font-bold flex items-center gap-2">
            <QrCode className="h-5 w-5 text-primary" />
            节点扫码配对 · {nodeName}
          </DialogTitle>
          <p className="text-xs text-muted-foreground">
            使用移动端客户端（如 LadderAirport Android）扫描下方二维码即可自动填入配置并完成注册。
          </p>
        </DialogHeader>

        <div className="flex flex-col items-center justify-center space-y-4 py-2">
          <div className="p-3 bg-white rounded-xl shadow-md border border-border flex items-center justify-center">
            {dataUrl ? (
              <img src={dataUrl} alt="Pairing QR Code" className="w-56 h-56 object-contain" />
            ) : (
              <div className="w-56 h-56 flex items-center justify-center text-xs text-muted-foreground">
                正在生成配对二维码…
              </div>
            )}
          </div>

          <div className="w-full space-y-2 text-xs">
            <div className="flex justify-between py-1 border-b border-border/50">
              <span className="text-muted-foreground">Panel 地址</span>
              <span className="font-mono text-foreground">{panelUrl}</span>
            </div>
            <div className="flex justify-between py-1 border-b border-border/50">
              <span className="text-muted-foreground">节点 ID</span>
              <span className="font-mono text-foreground">{nodeId}</span>
            </div>
            <div className="flex justify-between py-1">
              <span className="text-muted-foreground">注册令牌</span>
              <span className="font-mono text-foreground truncate max-w-[200px]">{token}</span>
            </div>

            <Button
              variant="outline"
              size="sm"
              onClick={() => void handleCopy()}
              className="w-full gap-2 mt-2"
            >
              {copied ? <Check className="h-4 w-4 text-emerald-500" /> : <Copy className="h-4 w-4" />}
              {copied ? '已复制配对 JSON' : '复制配对 JSON'}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
