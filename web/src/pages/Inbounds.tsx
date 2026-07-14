import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Button,
  Card,
  Checkbox,
  DialogPlugin,
  Form,
  Input,
  MessagePlugin,
  Select,
  Space,
  Table,
  Tag,
  type PrimaryTableCol,
} from 'tdesign-react'
import {
  createInbound,
  deleteInbound,
  listInbounds,
  listTemplates,
  type InboundConfig,
  type Template,
} from '../api/client'
import DynamicForm, { defaultsFromFields } from '../components/DynamicForm'

export default function Inbounds() {
  const [inbounds, setInbounds] = useState<InboundConfig[]>([])
  const [templates, setTemplates] = useState<Template[]>([])
  const [busy, setBusy] = useState(false)

  const [name, setName] = useState('')
  const [protocol, setProtocol] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [params, setParams] = useState<Record<string, unknown>>({})
  const [lastCreated, setLastCreated] = useState<InboundConfig | null>(null)
  const [expandedId, setExpandedId] = useState<string | null>(null)

  const selectedTemplate = useMemo(
    () => templates.find((t) => t.protocol === protocol),
    [templates, protocol],
  )

  const load = useCallback(async () => {
    try {
      const [ins, tmpls] = await Promise.all([listInbounds(), listTemplates()])
      setInbounds(ins ?? [])
      const tlist = tmpls ?? []
      setTemplates(tlist)
      if (!protocol && tlist.length > 0) {
        setProtocol(tlist[0].protocol)
        setParams(defaultsFromFields(tlist[0].fields))
      }
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '加载失败')
    }
  }, [protocol])

  useEffect(() => {
    void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- initial load only
  }, [])

  function onProtocolChange(p: string) {
    setProtocol(p)
    const t = templates.find((x) => x.protocol === p)
    if (t) setParams(defaultsFromFields(t.fields))
  }

  async function onCreate() {
    if (!protocol) {
      MessagePlugin.warning('请选择传输协议')
      return
    }
    if (!name.trim()) {
      MessagePlugin.warning('请填写配置名称')
      return
    }
    setBusy(true)
    try {
      const clean: Record<string, unknown> = {}
      for (const [k, v] of Object.entries(params)) {
        if (v === '') continue
        clean[k] = v
      }
      const created = await createInbound({
        name: name.trim(),
        protocol,
        params: clean,
        enabled,
      })
      setName('')
      setLastCreated(created)
      MessagePlugin.success('入站配置创建成功；密码 / UUID / 证书密钥已自动生成')
      if (selectedTemplate) {
        setParams(defaultsFromFields(selectedTemplate.fields))
      }
      await load()
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '创建失败')
    } finally {
      setBusy(false)
    }
  }

  function onDelete(id: string, n: string) {
    const dialog = DialogPlugin.confirm({
      header: '删除入站',
      body: `确定要删除入站配置「${n}」吗？`,
      theme: 'danger',
      confirmBtn: { content: '删除', theme: 'danger' },
      onConfirm: async () => {
        try {
          await deleteInbound(id)
          await load()
          MessagePlugin.success('已删除')
          dialog.destroy()
        } catch (err) {
          MessagePlugin.error(err instanceof Error ? err.message : '删除失败')
        }
      },
    })
  }

  const columns: PrimaryTableCol<InboundConfig>[] = [
    {
      colKey: 'name',
      title: '配置名称',
      cell: ({ row }) => <strong>{row.name}</strong>,
    },
    {
      colKey: 'protocol',
      title: '传输协议',
      width: 120,
      cell: ({ row }) => <code className="la-mono">{row.protocol}</code>,
    },
    {
      colKey: 'enabled',
      title: '是否启用',
      width: 100,
      cell: ({ row }) => (
        <Tag theme={row.enabled ? 'success' : 'default'} variant="light">
          {row.enabled ? '已启用' : '已禁用'}
        </Tag>
      ),
    },
    {
      colKey: 'params',
      title: '核心参数预览',
      cell: ({ row }) => (
        <div>
          <code className="la-mono">{summarizeParams(row.params)}</code>
          {expandedId === row.id ? (
            <pre className="la-pre" style={{ marginTop: 8 }}>
              {formatSecrets(row.params)}
            </pre>
          ) : null}
        </div>
      ),
    },
    {
      colKey: 'ops',
      title: '操作',
      width: 200,
      cell: ({ row }) => (
        <Space>
          <Button
            size="small"
            variant="outline"
            onClick={() => setExpandedId((id) => (id === row.id ? null : row.id))}
          >
            {expandedId === row.id ? '隐藏凭据' : '查看凭据'}
          </Button>
          <Button size="small" theme="danger" variant="outline" onClick={() => onDelete(row.id, row.name)}>
            删除
          </Button>
        </Space>
      ),
    },
  ]

  return (
    <div>
      <div className="la-page-header">
        <div>
          <h1 className="la-page-title">入站配置管理</h1>
          <p className="la-page-desc">创建协议模板并生成凭据，再关联到节点下发</p>
        </div>
      </div>

      <Card bordered className="la-section" title="创建入站配置">
        <p className="la-page-desc" style={{ marginTop: 0 }}>
          只需填写名称、协议和端口等基础项。密码、UUID、TLS 证书、Reality 密钥会在服务端自动生成。
        </p>
        <Form labelAlign="top">
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 12 }}>
            <Form.FormItem label="配置名称" requiredMark>
              <Input
                value={name}
                onChange={(v) => setName(String(v))}
                placeholder="例如: ss-edge-1"
                clearable
              />
            </Form.FormItem>
            <Form.FormItem label="传输协议" requiredMark>
              <Select
                value={protocol}
                onChange={(v) => onProtocolChange(String(v))}
                options={templates.map((t) => ({
                  label: `${t.name} (${t.protocol})`,
                  value: t.protocol,
                }))}
              />
            </Form.FormItem>
            <Form.FormItem label=" ">
              <Checkbox checked={enabled} onChange={(c) => setEnabled(Boolean(c))}>
                立即启用此配置
              </Checkbox>
            </Form.FormItem>
          </div>

          {selectedTemplate ? (
            <>
              <h3 style={{ margin: '8px 0 4px', fontSize: 14 }}>{selectedTemplate.name} 参数配置</h3>
              <DynamicForm fields={selectedTemplate.fields} value={params} onChange={setParams} />
            </>
          ) : null}

          <Form.FormItem style={{ marginBottom: 0 }}>
            <Button theme="primary" loading={busy} disabled={!protocol} onClick={() => void onCreate()}>
              创建入站配置
            </Button>
          </Form.FormItem>
        </Form>

        {lastCreated ? (
          <Card bordered size="small" style={{ marginTop: 16 }} title={`已生成凭据 — ${lastCreated.name}`}>
            <pre className="la-pre">{formatSecrets(lastCreated.params)}</pre>
          </Card>
        ) : null}
      </Card>

      <Card
        bordered
        className="la-section"
        title="已有入站"
        actions={
          <Button variant="outline" onClick={() => void load()}>
            刷新列表
          </Button>
        }
      >
        <Table
          rowKey="id"
          data={inbounds}
          columns={columns}
          empty="暂无入站配置数据"
          hover
          size="small"
        />
      </Card>
    </div>
  )
}

const SECRET_KEYS = new Set([
  'password',
  'private_key',
  'public_key',
  'uuid',
  'short_id',
  'tls_cert_pem',
  'tls_key_pem',
  'tls_cert_path',
  'tls_key_path',
])

function summarizeParams(params: Record<string, unknown> | null | undefined): string {
  if (!params) return '—'
  const parts: string[] = []
  for (const [k, v] of Object.entries(params)) {
    if (SECRET_KEYS.has(k) || k.endsWith('_pem')) {
      parts.push(`${k}=***`)
    } else {
      parts.push(`${k}=${String(v)}`)
    }
  }
  const s = parts.join(' ')
  return s.length > 80 ? s.slice(0, 77) + '…' : s || '—'
}

function formatSecrets(params: Record<string, unknown> | null | undefined): string {
  if (!params) return '—'
  const lines: string[] = []
  const order = [
    'listen',
    'port',
    'method',
    'password',
    'uuid',
    'tls_mode',
    'private_key',
    'short_id',
    'server_name',
    'handshake_server',
    'handshake_server_port',
    'flow',
    'network',
    'up_mbps',
    'down_mbps',
  ]
  const seen = new Set<string>()
  for (const k of order) {
    if (params[k] === undefined || params[k] === null || params[k] === '') continue
    seen.add(k)
    if (k === 'tls_cert_pem' || k === 'tls_key_pem') continue
    lines.push(`${k}: ${String(params[k])}`)
  }
  if (params.tls_cert_pem) {
    lines.push('tls_cert_pem: <auto self-signed PEM, applied inline>')
  }
  if (params.tls_key_pem) {
    lines.push('tls_key_pem: <auto private key PEM, applied inline>')
  }
  for (const [k, v] of Object.entries(params)) {
    if (seen.has(k) || k.endsWith('_pem')) continue
    lines.push(`${k}: ${String(v)}`)
  }
  return lines.join('\n') || '—'
}
