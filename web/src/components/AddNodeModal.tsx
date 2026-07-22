import { useEffect, useState } from 'react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from './ui/dialog'
import { Button } from './ui/button'
import { Alert, AlertDescription } from './ui/alert'
import { Checkbox } from './ui/checkbox'
import { Input } from './ui/input'
import { Label } from './ui/label'
import { Copy, Check, Info } from 'lucide-react'
import { bootstrapNode, type NodeInstallInfo } from '../api/client'
import { copyText } from '../lib/clipboard'
import { toast } from '../lib/toast'

type Props = {
  open: boolean
  onClose: () => void
  onCreated: () => void
  onOpenDetail: (nodeId: string) => void
}

export default function AddNodeModal({ open, onClose, onCreated, onOpenDetail }: Props) {
  const [name, setName] = useState('')
  const [address, setAddress] = useState('')
  const [grpcPort, setGrpcPort] = useState(50051)
  const [publicAddress, setPublicAddress] = useState('')
  const [labels, setLabels] = useState('')
  const [enableTLS, setEnableTLS] = useState(true)
  const [agentVersion, setAgentVersion] = useState('latest')
  const [busy, setBusy] = useState(false)
  const [installInfo, setInstallInfo] = useState<NodeInstallInfo | null>(null)
  const [copied, setCopied] = useState(false)
  const [formError, setFormError] = useState('')

  useEffect(() => {
    if (!open) return
    setName('')
    setAddress('')
    setGrpcPort(50051)
    setPublicAddress('')
    setLabels('')
    setEnableTLS(true)
    setAgentVersion('latest')
    setBusy(false)
    setInstallInfo(null)
    setCopied(false)
    setFormError('')
  }, [open])

  async function onSubmit() {
    if (busy) return
    if (!name.trim()) {
      setFormError('请填写节点名称')
      return
    }
    const controlHost = normalizeHost(address)
    const clientHost = normalizeHost(publicAddress)
    if (address.trim() && !controlHost) {
      setFormError('控制面地址只填写主机名或 IP，不要包含协议、端口或路径')
      return
    }
    if (publicAddress.trim() && !clientHost) {
      setFormError('公网地址只填写主机名或 IP，不要包含协议、端口或路径')
      return
    }
    if (!Number.isInteger(grpcPort) || grpcPort < 1 || grpcPort > 65535) {
      setFormError('gRPC 端口必须在 1 到 65535 之间')
      return
    }
    setFormError('')
    setBusy(true)
    setCopied(false)
    try {
      const info = await bootstrapNode({
        name: name.trim(),
        address: controlHost || undefined,
        grpc_port: grpcPort,
        public_address: clientHost || undefined,
        labels: Array.from(
          new Set(
            labels
              .split(/[,，]/)
              .map((value) => value.trim())
              .filter(Boolean),
          ),
        ),
        enable_tls: enableTLS,
        agent_version: agentVersion.trim() || 'latest',
      })
      setInstallInfo(info)
      onCreated()
      toast.success('节点已创建，请复制安装命令')
    } catch (err) {
      toast.error(err instanceof Error ? err.message : '创建失败')
    } finally {
      setBusy(false)
    }
  }

  async function copyCommand() {
    if (!installInfo?.install_command) return
    try {
      await copyText(installInfo.install_command)
      setCopied(true)
      toast.success('已复制安装命令')
      setTimeout(() => setCopied(false), 2000)
    } catch {
      toast.error('复制失败，请手动选择命令文本')
    }
  }

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v && !busy) onClose() }}>
      <DialogContent className="max-w-2xl bg-zinc-950 border-zinc-800 text-zinc-100 max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-xl font-bold flex items-center gap-2">
            {installInfo ? `安装命令 · ${installInfo.node.name}` : '添加节点'}
          </DialogTitle>
        </DialogHeader>

        {!installInfo ? (
          <div className="space-y-4 my-2">
            <div className="flex gap-2.5 p-3 rounded-lg bg-zinc-900/50 border border-zinc-800 text-xs text-zinc-400 leading-relaxed">
              <Info className="h-4 w-4 shrink-0 text-zinc-500 mt-0.5" />
              <div>
                创建节点并生成安装命令。目标机执行后会<strong>自动向 Panel 上报地址与 CA</strong>
                （需在「设置」填写 Public Base URL）。控制面地址可留空由 Agent 探测；若已预填则 enroll 不会覆盖。
              </div>
            </div>

            {formError ? (
              <Alert variant="destructive">
                <AlertDescription>{formError}</AlertDescription>
              </Alert>
            ) : null}

            <div className="space-y-4">
              <div className="flex flex-col space-y-1.5">
                <Label htmlFor="add-node-name" className="text-zinc-300">
                  节点名称 <span className="text-red-500">*</span>
                </Label>
                <Input
                  id="add-node-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="例如: 香港 01"
                  maxLength={80}
                  className="bg-zinc-900 border-zinc-800 focus-visible:ring-zinc-700"
                />
              </div>

              <div className="flex flex-col space-y-1.5">
                <Label htmlFor="add-node-address" className="text-zinc-300">控制面地址（可选，Panel 拨号）</Label>
                <Input
                  id="add-node-address"
                  value={address}
                  onChange={(e) => setAddress(e.target.value)}
                  placeholder="可先留空；已填则 enroll 不覆盖"
                  className="bg-zinc-900 border-zinc-800 focus-visible:ring-zinc-700"
                />
              </div>

              <div className="flex flex-col space-y-1.5">
                <Label htmlFor="add-node-grpc-port" className="text-zinc-300">控制面 gRPC 端口</Label>
                <Input
                  id="add-node-grpc-port"
                  type="number"
                  value={grpcPort}
                  min={1}
                  max={65535}
                  onChange={(e) => setGrpcPort(Number(e.target.value) || 0)}
                  className="bg-zinc-900 border-zinc-800 focus-visible:ring-zinc-700"
                />
              </div>

              <div className="flex flex-col space-y-1.5">
                <Label htmlFor="add-node-public-address" className="text-zinc-300">公网地址（可选，订阅用）</Label>
                <Input
                  id="add-node-public-address"
                  value={publicAddress}
                  onChange={(e) => setPublicAddress(e.target.value)}
                  placeholder="客户端入口；空则回退控制面地址"
                  className="bg-zinc-900 border-zinc-800 focus-visible:ring-zinc-700"
                />
              </div>

              <div className="flex flex-col space-y-1.5">
                <Label htmlFor="add-node-labels" className="text-zinc-300">节点标签（英文逗号分隔）</Label>
                <Input
                  id="add-node-labels"
                  value={labels}
                  onChange={(e) => setLabels(e.target.value)}
                  placeholder="例如: edge,prod,hk"
                  className="bg-zinc-900 border-zinc-800 focus-visible:ring-zinc-700"
                />
              </div>

              <div className="flex flex-col space-y-1.5">
                <Label htmlFor="add-node-agent-version" className="text-zinc-300">Agent 版本</Label>
                <Input
                  id="add-node-agent-version"
                  value={agentVersion}
                  onChange={(e) => setAgentVersion(e.target.value)}
                  placeholder="latest 或 v0.3.1"
                  className="bg-zinc-900 border-zinc-800 focus-visible:ring-zinc-700"
                />
              </div>

              <div className="flex items-center space-x-2 pt-2">
                <Checkbox
                  id="add-node-tls"
                  checked={enableTLS}
                  onCheckedChange={(c) => setEnableTLS(Boolean(c))}
                />
                <Label htmlFor="add-node-tls" className="text-zinc-300 cursor-pointer">
                  安装时启用 TLS（推荐）
                </Label>
              </div>
            </div>
          </div>
        ) : (
          <div className="space-y-4 my-2">
            <div className="text-sm text-zinc-400 leading-relaxed">
              Token 已写入节点；命令含 <code className="px-1.5 py-0.5 rounded bg-zinc-900 text-zinc-300 font-mono text-xs">LADDER_TOKEN</code>
              {installInfo.enable_tls ? ' + TLS' : '（明文）'}
              {installInfo.enroll_enabled
                ? `；装机后自动 enroll 到 ${installInfo.panel_base_url || 'Panel'}。`
                : '。未配置 Public Base URL 时无法自动上报，请先到「设置」填写。'}
            </div>

            <div className="flex items-center justify-between">
              <span className="text-sm font-semibold text-zinc-200">一键安装</span>
              <Button size="sm" variant="outline" onClick={() => void copyCommand()} className="gap-1.5 text-zinc-300 border-zinc-800 hover:bg-zinc-900">
                {copied ? <Check className="h-3.5 w-3.5 text-emerald-500" /> : <Copy className="h-3.5 w-3.5" />}
                {copied ? '已复制' : '复制命令'}
              </Button>
            </div>

            <pre className="p-4 rounded-lg bg-zinc-900 border border-zinc-800 overflow-x-auto text-xs font-mono text-zinc-300 leading-relaxed max-h-[160px]">
              {installInfo.install_command}
            </pre>

            <div className="space-y-2">
              <h4 className="text-xs font-semibold text-zinc-400 uppercase tracking-wider">后续步骤</h4>
              <ol className="list-decimal pl-5 space-y-1.5 text-sm text-zinc-400">
                {installInfo.steps.map((s, idx) => (
                  <li key={idx} className="leading-relaxed">
                    {s}
                  </li>
                ))}
              </ol>
            </div>
          </div>
        )}

        <DialogFooter className="mt-4 gap-2">
          {installInfo ? (
            <>
              <Button
                variant="default"
                onClick={() => {
                  onOpenDetail(installInfo.node.id)
                  onClose()
                }}
              >
                打开节点详情
              </Button>
              <Button variant="outline" onClick={onClose} className="border-zinc-800 hover:bg-zinc-900">
                完成
              </Button>
            </>
          ) : (
            <>
              <Button
                variant="outline"
                disabled={busy}
                onClick={onClose}
                className="border-zinc-800 hover:bg-zinc-900"
              >
                取消
              </Button>
              <Button loading={busy} onClick={() => void onSubmit()}>
                添加并生成安装命令
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function normalizeHost(value: string): string {
  let host = value.trim()
  if (!host) return ''
  if (host.startsWith('[') && host.endsWith(']')) host = host.slice(1, -1)
  if (!host || /[\s/?#@]|:\/\//.test(host)) return ''
  if (host.includes(':')) {
    const parts = host.split('%')
    if (parts.length > 2 || (parts[1] !== undefined && !/^[a-z0-9_.-]+$/i.test(parts[1]))) {
      return ''
    }
    try {
      new URL(`http://[${parts[0]}]/`)
    } catch {
      return ''
    }
    return host
  }
  if (host.length > 253 || !/^[a-z0-9](?:[a-z0-9._-]*[a-z0-9])?$/i.test(host)) return ''
  return host
}
