import { useCallback, useEffect, useMemo, useState } from 'react'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { StatusBadge } from '@/components/ui/status-badge'
import { ConfirmModal } from '@/components/ui/confirm-modal'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { AlertTriangle, KeyRound, Loader2, RefreshCw, ShieldCheck, ShieldX } from 'lucide-react'
import {
  getPKIStatus,
  listNodes,
  listPKICertificates,
  revokePKICertificate,
  type Node,
  type PKICertificate,
  type PKIStatus,
} from '../api/client'
import { formatTime } from '../lib/nodeDisplay'
import { toast } from '../lib/toast'

export default function PKI() {
  const [status, setStatus] = useState<PKIStatus | null>(null)
  const [certificates, setCertificates] = useState<PKICertificate[]>([])
  const [nodes, setNodes] = useState<Node[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [revoking, setRevoking] = useState('')

  const nodeNames = useMemo(
    () => new Map(nodes.map((node) => [node.id, node.name])),
    [nodes],
  )

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const [nextStatus, nextCertificates, nextNodes] = await Promise.all([
        getPKIStatus(),
        listPKICertificates(),
        listNodes(),
      ])
      setStatus(nextStatus)
      setCertificates(nextCertificates ?? [])
      setNodes(nextNodes ?? [])
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载管理 CA 状态失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const [revokeTarget, setRevokeTarget] = useState<PKICertificate | null>(null)

  function onRequestRevoke(cert: PKICertificate) {
    setRevokeTarget(cert)
  }

  async function confirmRevoke() {
    if (!revokeTarget) return
    const cert = revokeTarget
    setRevokeTarget(null)
    setRevoking(cert.serial)
    try {
      await revokePKICertificate(cert.serial, 'revoked by administrator')
      toast.success('证书已吊销')
      await load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : '吊销失败')
    } finally {
      setRevoking('')
    }
  }

  if (loading && status === null) {
    return (
      <div className="flex min-h-[320px] items-center justify-center text-muted-foreground">
        <Loader2 className="h-6 w-6 animate-spin" />
      </div>
    )
  }

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">管理面证书中心</h1>
          <p className="mt-1 text-sm text-muted-foreground">统一签发 Panel ↔ Agent mTLS 证书；不用于代理入站或公网域名。</p>
        </div>
        <Button variant="outline" size="sm" onClick={() => void load()} loading={loading}>
          {!loading && <RefreshCw className="mr-2 h-4 w-4" />}
          刷新
        </Button>
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertTitle>加载失败</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {status?.root_key_online && (
        <Alert>
          <AlertTriangle className="h-4 w-4" />
          <AlertTitle>根 CA 私钥仍在 Panel 服务器</AlertTitle>
          <AlertDescription>
            完成首次初始化并安全备份后，请将 pki/offline/root-ca.key 转移到离线介质。Panel 日常签发只需要中间 CA 私钥。
          </AlertDescription>
        </Alert>
      )}

      <div className="grid gap-4 md:grid-cols-3">
        <StatusCard
          icon={<ShieldCheck className="h-5 w-5 text-success" />}
          title="有效证书"
          value={String(status?.active_certificates ?? 0)}
          detail={`7 天内到期 ${status?.expiring_certificates ?? 0}`}
        />
        <StatusCard
          icon={<KeyRound className="h-5 w-5 text-info" />}
          title="中间 CA 到期"
          value={formatTime(status?.intermediate_not_after_unix ?? 0)}
          detail={status?.intermediate_subject ?? '—'}
        />
        <StatusCard
          icon={<ShieldCheck className="h-5 w-5 text-primary" />}
          title="根 CA 到期"
          value={formatTime(status?.root_not_after_unix ?? 0)}
          detail={status?.root_subject ?? '—'}
        />
      </div>

      <Card>
        <CardHeader>
          <CardTitle>证书清单</CardTitle>
          <CardDescription>新证书签发后，旧证书自动标记为已替换；吊销会立即解除节点身份绑定。</CardDescription>
        </CardHeader>
        <CardContent>
          {certificates.length === 0 ? (
            <div className="py-10 text-center text-sm text-muted-foreground">尚未签发 Agent 管理证书。</div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>节点 / 身份</TableHead>
                  <TableHead>序列号</TableHead>
                  <TableHead>有效期</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {certificates.map((cert) => (
                  <TableRow key={cert.serial}>
                    <TableCell>
                      <div className="font-medium">{nodeNames.get(cert.node_id ?? '') || cert.node_id || cert.subject}</div>
                      <div className="mt-1 max-w-[360px] truncate font-mono text-xs text-muted-foreground">{cert.uri_san}</div>
                    </TableCell>
                    <TableCell className="font-mono text-xs">{shortSerial(cert.serial)}</TableCell>
                    <TableCell>
                      <div>{formatTime(cert.not_after_unix)}</div>
                      <div className="mt-1 text-xs text-muted-foreground">签发 {formatTime(cert.created_at_unix)}</div>
                    </TableCell>
                    <TableCell><StatusBadge value={cert.status} /></TableCell>
                    <TableCell className="text-right">
                      {cert.status === 'active' && (
                        <Button
                          variant="ghost"
                          size="sm"
                          className="text-destructive hover:text-destructive/80"
                          loading={revoking === cert.serial}
                          onClick={() => onRequestRevoke(cert)}
                        >
                          {revoking !== cert.serial && <ShieldX className="mr-2 h-4 w-4" />}
                          吊销
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {/* Revoke Certificate Confirm Modal */}
      <ConfirmModal
        open={!!revokeTarget}
        title="吊销管理证书"
        description={`确认吊销 ${revokeTarget ? (nodeNames.get(revokeTarget.node_id ?? '') || revokeTarget.node_id || revokeTarget.serial) : ''} 的管理证书？吊销后 Panel 将停止信任该证书。`}
        confirmText="确认吊销"
        confirmVariant="destructive"
        onConfirm={() => void confirmRevoke()}
        onCancel={() => setRevokeTarget(null)}
      />
    </div>
  )
}

function StatusCard({ icon, title, value, detail }: { icon: React.ReactNode; title: string; value: string; detail: string }) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex items-center justify-between">
          <CardDescription>{title}</CardDescription>
          {icon}
        </div>
      </CardHeader>
      <CardContent>
        <div className="text-xl font-semibold">{value}</div>
        <div className="mt-2 truncate text-xs text-muted-foreground" title={detail}>{detail}</div>
      </CardContent>
    </Card>
  )
}

function shortSerial(serial: string) {
  return serial.length > 16 ? `${serial.slice(0, 8)}…${serial.slice(-8)}` : serial
}
