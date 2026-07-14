import { useCallback, useEffect, useState } from 'react'
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
  createSubscription,
  deleteSubscription,
  listInbounds,
  listSubscriptions,
  previewSubscription,
  updateSubscription,
  type InboundConfig,
  type Subscription,
} from '../api/client'

export default function Subscriptions() {
  const [list, setList] = useState<Subscription[]>([])
  const [inbounds, setInbounds] = useState<InboundConfig[]>([])
  const [busy, setBusy] = useState(false)
  const [preview, setPreview] = useState('')

  const [name, setName] = useState('')
  const [format, setFormat] = useState<'clash' | 'singbox'>('clash')
  const [selectedInbounds, setSelectedInbounds] = useState<Set<string>>(new Set())
  const [allInbounds, setAllInbounds] = useState(true)

  const load = useCallback(async () => {
    try {
      const [subs, ins] = await Promise.all([listSubscriptions(), listInbounds()])
      setList(subs ?? [])
      setInbounds(ins ?? [])
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '加载失败')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  function toggleInbound(id: string) {
    setSelectedInbounds((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  async function onCreate() {
    if (!name.trim()) {
      MessagePlugin.warning('请填写订阅名称')
      return
    }
    setBusy(true)
    try {
      const body = {
        name: name.trim(),
        format,
        inbound_ids: allInbounds ? [] : Array.from(selectedInbounds),
        enabled: true,
      }
      const sub = await createSubscription(body)
      setName('')
      MessagePlugin.success(`已创建订阅：${sub.url || sub.token}`)
      await load()
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '创建失败')
    } finally {
      setBusy(false)
    }
  }

  async function onCopy(url?: string) {
    if (!url) return
    try {
      await navigator.clipboard.writeText(url)
      MessagePlugin.success('订阅链接已复制')
    } catch {
      MessagePlugin.info(url)
    }
  }

  async function onPreview(id: string) {
    try {
      const text = await previewSubscription(id)
      setPreview(text)
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '预览失败')
    }
  }

  function onRotate(id: string) {
    const dialog = DialogPlugin.confirm({
      header: '轮换 Token',
      body: '轮换 Token 后旧链接将失效，继续？',
      theme: 'warning',
      onConfirm: async () => {
        try {
          const sub = await updateSubscription(id, { rotate_token: true })
          MessagePlugin.success(`新链接：${sub.url}`)
          await load()
          dialog.destroy()
        } catch (err) {
          MessagePlugin.error(err instanceof Error ? err.message : '轮换失败')
        }
      },
    })
  }

  async function onToggle(sub: Subscription) {
    try {
      await updateSubscription(sub.id, { enabled: !sub.enabled })
      await load()
      MessagePlugin.success(sub.enabled ? '已禁用' : '已启用')
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '更新失败')
    }
  }

  function onDelete(sub: Subscription) {
    const dialog = DialogPlugin.confirm({
      header: '删除订阅',
      body: `删除订阅「${sub.name}」？`,
      theme: 'danger',
      confirmBtn: { content: '删除', theme: 'danger' },
      onConfirm: async () => {
        try {
          await deleteSubscription(sub.id)
          await load()
          MessagePlugin.success('已删除')
          dialog.destroy()
        } catch (err) {
          MessagePlugin.error(err instanceof Error ? err.message : '删除失败')
        }
      },
    })
  }

  const columns: PrimaryTableCol<Subscription>[] = [
    {
      colKey: 'name',
      title: '名称',
      cell: ({ row }) => <strong>{row.name}</strong>,
    },
    {
      colKey: 'format',
      title: '格式',
      width: 100,
      cell: ({ row }) => <code className="la-mono">{row.format}</code>,
    },
    {
      colKey: 'enabled',
      title: '状态',
      width: 90,
      cell: ({ row }) => (
        <Tag theme={row.enabled ? 'success' : 'default'} variant="light">
          {row.enabled ? '启用' : '禁用'}
        </Tag>
      ),
    },
    {
      colKey: 'url',
      title: '链接',
      cell: ({ row }) => <code className="la-mono">{row.url}</code>,
    },
    {
      colKey: 'ops',
      title: '操作',
      width: 320,
      cell: ({ row }) => (
        <Space size={4} breakLine>
          <Button size="small" variant="outline" onClick={() => void onCopy(row.url)}>
            复制
          </Button>
          <Button size="small" variant="outline" onClick={() => void onPreview(row.id)}>
            预览
          </Button>
          <Button size="small" variant="outline" onClick={() => void onToggle(row)}>
            {row.enabled ? '禁用' : '启用'}
          </Button>
          <Button size="small" variant="outline" onClick={() => onRotate(row.id)}>
            轮换 Token
          </Button>
          <Button size="small" theme="danger" variant="outline" onClick={() => onDelete(row)}>
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
          <h1 className="la-page-title">订阅</h1>
          <p className="la-page-desc">
            生成 Clash / sing-box 客户端订阅。基础 CN 分流：局域网与中国大陆直连，其余走代理。节点{' '}
            <code>Address</code> 会作为客户端连接的服务器地址。
          </p>
        </div>
      </div>

      <Card bordered className="la-section" title="创建订阅">
        <Form labelAlign="top">
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 12 }}>
            <Form.FormItem label="名称" requiredMark>
              <Input
                value={name}
                onChange={(v) => setName(String(v))}
                placeholder="例如: 主力 Clash"
                clearable
              />
            </Form.FormItem>
            <Form.FormItem label="格式">
              <Select
                value={format}
                onChange={(v) => setFormat(v as 'clash' | 'singbox')}
                options={[
                  { label: 'Clash / Mihomo (YAML)', value: 'clash' },
                  { label: 'sing-box (JSON)', value: 'singbox' },
                ]}
              />
            </Form.FormItem>
          </div>

          <Form.FormItem>
            <Checkbox checked={allInbounds} onChange={(c) => setAllInbounds(Boolean(c))}>
              包含所有已启用入站（按节点关联展开）
            </Checkbox>
          </Form.FormItem>

          {!allInbounds ? (
            <div className="la-check-list" style={{ marginBottom: 16 }}>
              {inbounds.length === 0 ? (
                <span className="la-page-desc">暂无入站配置</span>
              ) : (
                inbounds.map((inb) => (
                  <Checkbox
                    key={inb.id}
                    checked={selectedInbounds.has(inb.id)}
                    onChange={() => toggleInbound(inb.id)}
                  >
                    {inb.name} <code className="la-mono">{inb.protocol}</code>
                    {!inb.enabled ? ' (已禁用)' : ''}
                  </Checkbox>
                ))
              )}
            </div>
          ) : null}

          <Form.FormItem style={{ marginBottom: 0 }}>
            <Button theme="primary" loading={busy} onClick={() => void onCreate()}>
              创建订阅
            </Button>
          </Form.FormItem>
        </Form>
      </Card>

      <Card
        bordered
        className="la-section"
        title="订阅列表"
        actions={
          <Button variant="outline" onClick={() => void load()}>
            刷新
          </Button>
        }
      >
        <Table rowKey="id" data={list} columns={columns} empty="暂无订阅" hover size="small" />
      </Card>

      {preview ? (
        <Card
          bordered
          className="la-section"
          title="配置预览"
          actions={
            <Button size="small" variant="outline" onClick={() => setPreview('')}>
              关闭
            </Button>
          }
        >
          <pre className="la-pre">{preview}</pre>
        </Card>
      ) : null}
    </div>
  )
}
