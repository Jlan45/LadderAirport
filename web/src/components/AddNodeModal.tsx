import { useEffect, useState } from 'react'
import {
  Button,
  Checkbox,
  Dialog,
  Form,
  Input,
  InputNumber,
  MessagePlugin,
  Space,
} from 'tdesign-react'
import { bootstrapNode, type NodeInstallInfo } from '../api/client'

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
  }, [open])

  async function onSubmit() {
    if (!name.trim()) {
      MessagePlugin.warning('请填写节点名称')
      return
    }
    setBusy(true)
    setCopied(false)
    try {
      const info = await bootstrapNode({
        name: name.trim(),
        address: address.trim() || undefined,
        grpc_port: grpcPort,
        public_address: publicAddress.trim() || undefined,
        labels: labels
          .split(',')
          .map((s) => s.trim())
          .filter(Boolean),
        enable_tls: enableTLS,
        agent_version: agentVersion.trim() || 'latest',
      })
      setInstallInfo(info)
      onCreated()
      MessagePlugin.success('节点已创建，请复制安装命令')
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '创建失败')
    } finally {
      setBusy(false)
    }
  }

  async function copyCommand() {
    if (!installInfo?.install_command) return
    try {
      await navigator.clipboard.writeText(installInfo.install_command)
      setCopied(true)
      MessagePlugin.success('已复制安装命令')
      setTimeout(() => setCopied(false), 2000)
    } catch {
      MessagePlugin.error('复制失败，请手动选择命令文本')
    }
  }

  return (
    <Dialog
      visible={open}
      header={installInfo ? `安装命令 · ${installInfo.node.name}` : '添加节点'}
      width={640}
      footer={
        installInfo ? (
          <Space>
            <Button
              theme="primary"
              onClick={() => {
                onOpenDetail(installInfo.node.id)
                onClose()
              }}
            >
              打开节点详情
            </Button>
            <Button variant="outline" onClick={onClose}>
              完成
            </Button>
          </Space>
        ) : (
          <Space>
            <Button variant="outline" onClick={onClose}>
              取消
            </Button>
            <Button theme="primary" loading={busy} onClick={() => void onSubmit()}>
              添加并生成安装命令
            </Button>
          </Space>
        )
      }
      onClose={onClose}
      destroyOnClose
    >
      {!installInfo ? (
        <>
          <p className="la-page-desc" style={{ marginTop: 0 }}>
            创建节点并生成安装命令。目标机执行后会<strong>自动向 Panel 上报地址与 CA</strong>
            （需在「设置」填写 Public Base URL）。控制面地址可留空由 Agent 探测；若已预填则 enroll 不会覆盖。
          </p>
          <Form labelAlign="top">
            <Form.FormItem label="节点名称" requiredMark>
              <Input
                value={name}
                onChange={(v) => setName(String(v))}
                placeholder="例如: 香港 01"
                clearable
              />
            </Form.FormItem>
            <Form.FormItem label="控制面地址（可选，Panel 拨号）">
              <Input
                value={address}
                onChange={(v) => setAddress(String(v))}
                placeholder="可先留空；已填则 enroll 不覆盖"
                clearable
              />
            </Form.FormItem>
            <Form.FormItem label="控制面 gRPC 端口">
              <InputNumber
                theme="normal"
                style={{ width: '100%' }}
                value={grpcPort}
                onChange={(v) => setGrpcPort(Number(v) || 50051)}
              />
            </Form.FormItem>
            <Form.FormItem label="公网地址（可选，订阅用）">
              <Input
                value={publicAddress}
                onChange={(v) => setPublicAddress(String(v))}
                placeholder="客户端入口；空则回退控制面地址"
                clearable
              />
            </Form.FormItem>
            <Form.FormItem label="节点标签（英文逗号分隔）">
              <Input
                value={labels}
                onChange={(v) => setLabels(String(v))}
                placeholder="例如: edge,prod,hk"
                clearable
              />
            </Form.FormItem>
            <Form.FormItem label="Agent 版本">
              <Input
                value={agentVersion}
                onChange={(v) => setAgentVersion(String(v))}
                placeholder="latest 或 v0.3.1"
                clearable
              />
            </Form.FormItem>
            <Form.FormItem>
              <Checkbox checked={enableTLS} onChange={(c) => setEnableTLS(Boolean(c))}>
                安装时启用 TLS（推荐）
              </Checkbox>
            </Form.FormItem>
          </Form>
        </>
      ) : (
        <>
          <p className="la-page-desc" style={{ marginTop: 0 }}>
            Token 已写入节点；命令含 <code>LADDER_TOKEN</code>
            {installInfo.enable_tls ? ' + TLS' : '（明文）'}
            {installInfo.enroll_enabled
              ? `；装机后自动 enroll 到 ${installInfo.panel_base_url || 'Panel'}。`
              : '。未配置 Public Base URL 时无法自动上报，请先到「设置」填写。'}
          </p>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 8 }}>
            <strong>一键安装</strong>
            <Button size="small" variant="outline" onClick={() => void copyCommand()}>
              {copied ? '已复制' : '复制命令'}
            </Button>
          </div>
          <pre className="la-pre">{installInfo.install_command}</pre>
          <ol style={{ marginTop: 16, paddingLeft: 20 }}>
            {installInfo.steps.map((s) => (
              <li key={s} style={{ marginBottom: 6 }}>
                {s}
              </li>
            ))}
          </ol>
        </>
      )}
    </Dialog>
  )
}
