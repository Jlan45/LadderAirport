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
  createExternalSource,
  createSubscription,
  deleteExternalSource,
  deleteSubscription,
  listExternalSources,
  listInbounds,
  listSubscriptions,
  previewSubscription,
  refreshExternalSource,
  updateExternalSource,
  updateSubscription,
  type ExternalSource,
  type InboundConfig,
  type Subscription,
} from '../api/client'
import { formatTime } from '../lib/nodeDisplay'

export default function Subscriptions() {
  const [list, setList] = useState<Subscription[]>([])
  const [inbounds, setInbounds] = useState<InboundConfig[]>([])
  const [sources, setSources] = useState<ExternalSource[]>([])
  const [busy, setBusy] = useState(false)
  const [preview, setPreview] = useState('')

  const [name, setName] = useState('')
  const [format, setFormat] = useState<'clash' | 'singbox'>('clash')
  const [selectedInbounds, setSelectedInbounds] = useState<Set<string>>(new Set())
  const [allInbounds, setAllInbounds] = useState(true)
  const [selectedSources, setSelectedSources] = useState<Set<string>>(new Set())

  const [srcName, setSrcName] = useState('')
  const [srcURL, setSrcURL] = useState('')
  const [srcInterval, setSrcInterval] = useState('86400')

  const load = useCallback(async () => {
    try {
      const [subs, ins, srcs] = await Promise.all([
        listSubscriptions(),
        listInbounds(),
        listExternalSources(),
      ])
      setList(subs ?? [])
      setInbounds(ins ?? [])
      setSources(srcs ?? [])
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

  function toggleSource(id: string) {
    setSelectedSources((prev) => {
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
        external_source_ids: Array.from(selectedSources),
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

  async function onCreateSource() {
    if (!srcName.trim() || !srcURL.trim()) {
      MessagePlugin.warning('请填写外部源名称与 URL')
      return
    }
    setBusy(true)
    try {
      const interval = Number(srcInterval) || 0
      const src = await createExternalSource({
        name: srcName.trim(),
        url: srcURL.trim(),
        enabled: true,
        refresh_interval_sec: interval,
      })
      setSrcName('')
      setSrcURL('')
      MessagePlugin.success(
        `已添加外部源「${src.name}」${src.cached_proxy_count ? ` · ${src.cached_proxy_count} 节点` : ''}`,
      )
      await load()
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '添加外部源失败')
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

  async function onRefreshSource(id: string) {
    try {
      const src = await refreshExternalSource(id)
      MessagePlugin.success(
        `已刷新「${src.name}」· ${src.cached_proxy_count ?? 0} 节点` +
          (src.content_type ? ` · ${src.content_type}` : ''),
      )
      await load()
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '刷新失败')
      await load()
    }
  }

  async function onToggleSource(src: ExternalSource) {
    try {
      await updateExternalSource(src.id, { enabled: !src.enabled })
      await load()
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '更新失败')
    }
  }

  function onDeleteSource(src: ExternalSource) {
    const dialog = DialogPlugin.confirm({
      header: '删除外部源',
      body: `删除外部源「${src.name}」？已挂到订阅上的关联会一并移除。`,
      theme: 'danger',
      confirmBtn: { content: '删除', theme: 'danger' },
      onConfirm: async () => {
        try {
          await deleteExternalSource(src.id)
          setSelectedSources((prev) => {
            const next = new Set(prev)
            next.delete(src.id)
            return next
          })
          await load()
          MessagePlugin.success('已删除外部源')
          dialog.destroy()
        } catch (err) {
          MessagePlugin.error(err instanceof Error ? err.message : '删除失败')
        }
      },
    })
  }

  const sourceName = (id: string) => sources.find((s) => s.id === id)?.name || id.slice(0, 8)

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
      colKey: 'external_source_ids',
      title: '外部源',
      width: 160,
      cell: ({ row }) => {
        const ids = row.external_source_ids ?? []
        if (ids.length === 0) return <span className="la-page-desc">—</span>
        return (
          <Space size={4} breakLine>
            {ids.map((id) => (
              <Tag key={id} size="small" variant="outline">
                {sourceName(id)}
              </Tag>
            ))}
          </Space>
        )
      },
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

  const sourceColumns: PrimaryTableCol<ExternalSource>[] = [
    {
      colKey: 'name',
      title: '名称',
      cell: ({ row }) => <strong>{row.name}</strong>,
    },
    {
      colKey: 'url',
      title: 'URL',
      cell: ({ row }) => (
        <code className="la-mono" style={{ wordBreak: 'break-all' }}>
          {row.url}
        </code>
      ),
    },
    {
      colKey: 'cached_proxy_count',
      title: '节点',
      width: 80,
      cell: ({ row }) => row.cached_proxy_count ?? 0,
    },
    {
      colKey: 'content_type',
      title: '格式',
      width: 110,
      cell: ({ row }) => row.content_type || '—',
    },
    {
      colKey: 'last_success_unix',
      title: '上次成功',
      width: 150,
      cell: ({ row }) => formatTime(row.last_success_unix),
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
      colKey: 'last_error',
      title: '错误',
      cell: ({ row }) =>
        row.last_error ? (
          <span className="la-node-error" style={{ fontSize: 12 }}>
            {row.last_error}
          </span>
        ) : (
          '—'
        ),
    },
    {
      colKey: 'ops',
      title: '操作',
      width: 220,
      cell: ({ row }) => (
        <Space size={4} breakLine>
          <Button size="small" variant="outline" onClick={() => void onRefreshSource(row.id)}>
            刷新
          </Button>
          <Button size="small" variant="outline" onClick={() => void onToggleSource(row)}>
            {row.enabled ? '禁用' : '启用'}
          </Button>
          <Button size="small" theme="danger" variant="outline" onClick={() => onDeleteSource(row)}>
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
            生成本地节点的 Clash / sing-box 订阅，并可聚合外部机场源。CN 分流：局域网与中国大陆直连，其余走
            PROXY。
          </p>
        </div>
      </div>

      <Card bordered className="la-section" title="外部源">
        <p className="la-page-desc" style={{ marginTop: 0 }}>
          填写其他机场的订阅 URL（Clash YAML / 节点链接列表 / sing-box JSON）。拉取后会缓存，可挂到下方订阅中合并输出。
        </p>
        <Form labelAlign="top">
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 12 }}>
            <Form.FormItem label="名称" requiredMark>
              <Input value={srcName} onChange={(v) => setSrcName(String(v))} placeholder="例如: 机场A" clearable />
            </Form.FormItem>
            <Form.FormItem label="订阅 URL" requiredMark>
              <Input value={srcURL} onChange={(v) => setSrcURL(String(v))} placeholder="https://..." clearable />
            </Form.FormItem>
            <Form.FormItem label="刷新间隔（秒，0=默认24h）">
              <Input value={srcInterval} onChange={(v) => setSrcInterval(String(v))} placeholder="86400" />
            </Form.FormItem>
          </div>
          <Form.FormItem style={{ marginBottom: 12 }}>
            <Button theme="primary" loading={busy} onClick={() => void onCreateSource()}>
              添加外部源
            </Button>
          </Form.FormItem>
        </Form>
        <Table rowKey="id" data={sources} columns={sourceColumns} empty="暂无外部源" hover size="small" />
      </Card>

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
              包含所有已启用本地入站（按节点关联展开）
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

          <Form.FormItem label="合并外部源">
            {sources.length === 0 ? (
              <span className="la-page-desc">暂无外部源，可先在上方添加</span>
            ) : (
              <div className="la-check-list">
                {sources.map((src) => (
                  <Checkbox
                    key={src.id}
                    checked={selectedSources.has(src.id)}
                    disabled={!src.enabled}
                    onChange={() => toggleSource(src.id)}
                  >
                    {src.name}
                    {!src.enabled ? ' (已禁用)' : ''}
                    {src.cached_proxy_count ? ` · ${src.cached_proxy_count} 节点` : ''}
                  </Checkbox>
                ))}
              </div>
            )}
          </Form.FormItem>

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
