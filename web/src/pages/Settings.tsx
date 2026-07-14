import { useCallback, useEffect, useState } from 'react'
import {
  Button,
  Card,
  Col,
  Form,
  Input,
  InputNumber,
  MessagePlugin,
  Row,
  Space,
} from 'tdesign-react'
import { getSettings, putSettings } from '../api/client'

export default function Settings() {
  const [token, setToken] = useState('')
  const [timeoutSec, setTimeoutSec] = useState(10)
  const [concurrency, setConcurrency] = useState(8)
  const [listenAddr, setListenAddr] = useState('')
  const [publicBase, setPublicBase] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const s = await getSettings()
      setToken(s.default_agent_token || '')
      setTimeoutSec(s.grpc_timeout_sec)
      setConcurrency(s.max_concurrency)
      setListenAddr(s.listen_addr || '')
      setPublicBase(s.public_base_url || '')
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '加载系统设置失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  async function onSave() {
    if (newPassword && newPassword !== confirmPassword) {
      MessagePlugin.warning('两次输入的密码不一致')
      return
    }
    setBusy(true)
    try {
      const body: Parameters<typeof putSettings>[0] = {
        default_agent_token: token,
        grpc_timeout_sec: timeoutSec,
        max_concurrency: concurrency,
        listen_addr: listenAddr,
        public_base_url: publicBase,
      }
      if (newPassword) {
        body.new_password = newPassword
      }
      const s = await putSettings(body)
      setToken(s.default_agent_token || '')
      setTimeoutSec(s.grpc_timeout_sec)
      setConcurrency(s.max_concurrency)
      setListenAddr(s.listen_addr || '')
      setPublicBase(s.public_base_url || '')
      setNewPassword('')
      setConfirmPassword('')
      MessagePlugin.success('系统设置保存成功')
    } catch (err) {
      MessagePlugin.error(err instanceof Error ? err.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="la-settings">
      <div className="la-page-header">
        <div>
          <h1 className="la-page-title">系统设置</h1>
          <p className="la-page-desc">管理节点连接默认值、Panel 对外地址与管理员账号</p>
        </div>
        <Space>
          <Button variant="outline" loading={loading} onClick={() => void load()}>
            重新加载
          </Button>
          <Button theme="primary" loading={busy} onClick={() => void onSave()}>
            保存全部
          </Button>
        </Space>
      </div>

      <div className="la-settings-grid">
        <Card bordered className="la-section" title="连接与任务" subtitle="节点 gRPC 管控默认参数">
          <Form labelAlign="top" disabled={loading}>
            <Form.FormItem
              label="默认 Agent 访问令牌"
              help="新建节点未单独填写 Token 时使用；也用于生成安装命令"
            >
              <Input
                type="password"
                value={token}
                onChange={(v) => setToken(String(v))}
                autocomplete="off"
                placeholder="长随机串，请勿使用弱口令"
                clearable
              />
            </Form.FormItem>
            <Row gutter={[16, 0]}>
              <Col xs={12} sm={12} md={6}>
                <Form.FormItem label="gRPC 超时（秒）" help="单次探测 / 下发 / 启停的等待上限">
                  <InputNumber
                    theme="column"
                    style={{ width: '100%' }}
                    min={1}
                    max={600}
                    value={timeoutSec}
                    onChange={(v) => setTimeoutSec(Number(v) || 1)}
                    suffix="秒"
                  />
                </Form.FormItem>
              </Col>
              <Col xs={12} sm={12} md={6}>
                <Form.FormItem label="最大并发任务数" help="批量下发 / 启动时的并行上限">
                  <InputNumber
                    theme="column"
                    style={{ width: '100%' }}
                    min={1}
                    max={256}
                    value={concurrency}
                    onChange={(v) => setConcurrency(Number(v) || 1)}
                  />
                </Form.FormItem>
              </Col>
            </Row>
          </Form>
        </Card>

        <Card bordered className="la-section" title="服务与订阅" subtitle="Panel 自身监听与对外公开地址">
          <Form labelAlign="top" disabled={loading}>
            <Form.FormItem
              label="Public Base URL"
              help="用于完整订阅链接，以及「添加节点」安装命令里的 LADDER_PANEL 自动 enroll"
            >
              <Input
                value={publicBase}
                onChange={(v) => setPublicBase(String(v))}
                placeholder="https://panel.example.com"
                clearable
              />
            </Form.FormItem>
            <Form.FormItem
              label="面板监听地址"
              help="仅记录在设置中，修改后通常需要重启 Panel 进程才生效"
            >
              <Input
                value={listenAddr}
                onChange={(v) => setListenAddr(String(v))}
                placeholder=":8080 或 127.0.0.1:8080"
                clearable
              />
            </Form.FormItem>
          </Form>
        </Card>

        <Card bordered className="la-section" title="安全" subtitle="控制台管理员密码">
          <Form labelAlign="top" disabled={loading}>
            <Row gutter={[16, 0]}>
              <Col xs={12} sm={12} md={6}>
                <Form.FormItem label="新密码" help="留空表示不修改">
                  <Input
                    type="password"
                    value={newPassword}
                    onChange={(v) => setNewPassword(String(v))}
                    autocomplete="new-password"
                    placeholder="新管理员密码"
                    clearable
                  />
                </Form.FormItem>
              </Col>
              <Col xs={12} sm={12} md={6}>
                <Form.FormItem label="确认新密码">
                  <Input
                    type="password"
                    value={confirmPassword}
                    onChange={(v) => setConfirmPassword(String(v))}
                    autocomplete="new-password"
                    placeholder="再次输入以确认"
                    clearable
                    status={
                      confirmPassword && newPassword && confirmPassword !== newPassword
                        ? 'error'
                        : undefined
                    }
                    tips={
                      confirmPassword && newPassword && confirmPassword !== newPassword
                        ? '两次输入不一致'
                        : undefined
                    }
                  />
                </Form.FormItem>
              </Col>
            </Row>
          </Form>
        </Card>
      </div>

      <div className="la-settings-footer">
        <Space>
          <Button variant="outline" loading={loading} onClick={() => void load()}>
            放弃未保存并重载
          </Button>
          <Button theme="primary" loading={busy} onClick={() => void onSave()}>
            保存设置
          </Button>
        </Space>
      </div>
    </div>
  )
}
