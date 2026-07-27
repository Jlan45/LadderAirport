import { FormEvent, useCallback, useEffect, useMemo, useState } from 'react'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Loader2, Play, Plus, RefreshCw, TestTube2 } from 'lucide-react'
import {
  createACMEAccount,
  createDNSAccount,
  createManagedDomain,
  createProtocolCertificate,
  issueProtocolCertificate,
  listACMEAccounts,
  listAutomationJobs,
  listDNSAccounts,
  listDNSProviders,
  listManagedDomains,
  listNodes,
  listProtocolCertificates,
  reconcileManagedDomain,
  registerACMEAccount,
  testDNSAccount,
  type ACMEAccount,
  type AutomationJob,
  type DNSAccount,
  type DNSProviderMetadata,
  type ManagedDomain,
  type Node,
  type ProtocolCertificate,
} from '../api/client'
import { formatTime } from '../lib/nodeDisplay'
import { toast } from '../lib/toast'

const selectClass = 'h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm text-zinc-100'

export default function DNSCertificates() {
  const [providers, setProviders] = useState<DNSProviderMetadata[]>([])
  const [dnsAccounts, setDNSAccounts] = useState<DNSAccount[]>([])
  const [domains, setDomains] = useState<ManagedDomain[]>([])
  const [acmeAccounts, setACMEAccounts] = useState<ACMEAccount[]>([])
  const [certificates, setCertificates] = useState<ProtocolCertificate[]>([])
  const [jobs, setJobs] = useState<AutomationJob[]>([])
  const [nodes, setNodes] = useState<Node[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const [p, d, m, a, c, j, n] = await Promise.all([
        listDNSProviders(), listDNSAccounts(), listManagedDomains(),
        listACMEAccounts(), listProtocolCertificates(), listAutomationJobs(), listNodes(),
      ])
      setProviders(p ?? [])
      setDNSAccounts(d ?? [])
      setDomains(m ?? [])
      setACMEAccounts(a ?? [])
      setCertificates(c ?? [])
      setJobs(j ?? [])
      setNodes(n ?? [])
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载 DNS / ACME 数据失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { void load() }, [load])

  async function action(key: string, work: () => Promise<unknown>, success: string) {
    setBusy(key)
    try {
      await work()
      toast.success(success)
      await load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : '操作失败')
    } finally {
      setBusy('')
    }
  }

  if (loading && providers.length === 0) {
    return <div className="flex min-h-[320px] items-center justify-center"><Loader2 className="h-6 w-6 animate-spin text-zinc-500" /></div>
  }

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">DNS 与协议证书</h1>
          <p className="mt-1 text-sm text-zinc-500">自动维护节点域名，并通过 DNS-01 为代理协议签发公网 TLS 证书。</p>
        </div>
        <Button variant="outline" size="sm" onClick={() => void load()} loading={loading}>
          {!loading && <RefreshCw className="mr-2 h-4 w-4" />}刷新
        </Button>
      </div>
      {error && <Alert variant="destructive"><AlertTitle>加载失败</AlertTitle><AlertDescription>{error}</AlertDescription></Alert>}
      <Alert>
        <AlertTitle>私钥边界</AlertTitle>
        <AlertDescription>DNS 与 ACME 账号凭据在 Panel 加密保存；协议证书私钥仅在对应 Agent 上生成和保存，不会回传 Panel。</AlertDescription>
      </Alert>

      <Tabs defaultValue="dns">
        <TabsList className="grid w-full grid-cols-4">
          <TabsTrigger value="dns">DNS 账号</TabsTrigger>
          <TabsTrigger value="domains">托管域名</TabsTrigger>
          <TabsTrigger value="certificates">ACME / 证书</TabsTrigger>
          <TabsTrigger value="jobs">任务</TabsTrigger>
        </TabsList>
        <TabsContent value="dns" className="space-y-4">
          <DNSAccountForm providers={providers} onCreate={(body) => action('dns-create', () => createDNSAccount(body), 'DNS 账号已创建')} busy={busy === 'dns-create'} />
          <Card>
            <CardHeader><CardTitle>DNS 账号</CardTitle><CardDescription>敏感字段只可覆盖写入，API 不会返回明文。</CardDescription></CardHeader>
            <CardContent><Table><TableHeader><TableRow><TableHead>名称</TableHead><TableHead>供应商</TableHead><TableHead>状态</TableHead><TableHead>最近测试</TableHead><TableHead className="text-right">操作</TableHead></TableRow></TableHeader>
              <TableBody>{dnsAccounts.map((account) => <TableRow key={account.id}>
                <TableCell className="font-medium">{account.name}</TableCell><TableCell>{account.provider}</TableCell>
                <TableCell><StatusBadge value={account.enabled ? (account.last_test_error ? 'error' : 'enabled') : 'disabled'} /></TableCell>
                <TableCell>{account.last_test_unix ? formatTime(account.last_test_unix) : '未测试'}{account.last_test_error && <div className="max-w-[360px] truncate text-xs text-red-400">{account.last_test_error}</div>}</TableCell>
                <TableCell className="text-right"><Button variant="ghost" size="sm" loading={busy === `dns-test-${account.id}`} onClick={() => void action(`dns-test-${account.id}`, () => testDNSAccount(account.id), 'DNS 连接测试通过')}><TestTube2 className="mr-2 h-4 w-4" />测试</Button></TableCell>
              </TableRow>)}</TableBody></Table></CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="domains" className="space-y-4">
          <DomainForm nodes={nodes} accounts={dnsAccounts} onCreate={(body) => action('domain-create', () => createManagedDomain(body), '托管域名已创建并进入同步队列')} busy={busy === 'domain-create'} />
          <Card><CardHeader><CardTitle>托管域名</CardTitle><CardDescription>观测值会定期与 DNS 供应商重新对账。</CardDescription></CardHeader>
            <CardContent><Table><TableHeader><TableRow><TableHead>域名</TableHead><TableHead>地址</TableHead><TableHead>状态</TableHead><TableHead>下次同步</TableHead><TableHead className="text-right">操作</TableHead></TableRow></TableHeader>
              <TableBody>{domains.map((domain) => <TableRow key={domain.id}><TableCell><div className="font-medium">{domain.fqdn}</div><div className="text-xs text-zinc-500">{domain.zone} · {domain.record_mode.toUpperCase()}</div></TableCell>
                <TableCell className="font-mono text-xs">{[domain.desired_ipv4, domain.desired_ipv6].filter(Boolean).join(' / ') || '等待探测'}</TableCell>
                <TableCell><StatusBadge value={domain.state} />{domain.last_error && <div className="mt-1 max-w-[300px] truncate text-xs text-red-400">{domain.last_error}</div>}</TableCell>
                <TableCell>{domain.next_reconcile_unix ? formatTime(domain.next_reconcile_unix) : '立即'}</TableCell>
                <TableCell className="text-right"><Button variant="ghost" size="sm" loading={busy === `reconcile-${domain.id}`} onClick={() => void action(`reconcile-${domain.id}`, () => reconcileManagedDomain(domain.id), '已提交 DNS 同步')}><RefreshCw className="mr-2 h-4 w-4" />同步</Button></TableCell>
              </TableRow>)}</TableBody></Table></CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="certificates" className="space-y-4">
          <div className="grid gap-4 xl:grid-cols-2">
            <ACMEAccountForm onCreate={(body) => action('acme-create', () => createACMEAccount(body), 'ACME 账号已保存，请继续注册')} busy={busy === 'acme-create'} />
            <CertificateForm domains={domains} accounts={acmeAccounts} onCreate={(body) => action('cert-create', () => createProtocolCertificate(body), '证书已进入签发队列')} busy={busy === 'cert-create'} />
          </div>
          <Card><CardHeader><CardTitle>ACME 账号</CardTitle></CardHeader><CardContent>
            <Table><TableHeader><TableRow><TableHead>名称</TableHead><TableHead>目录</TableHead><TableHead>状态</TableHead><TableHead className="text-right">操作</TableHead></TableRow></TableHeader><TableBody>
              {acmeAccounts.map((account) => <TableRow key={account.id}><TableCell className="font-medium">{account.name}</TableCell><TableCell className="max-w-[420px] truncate font-mono text-xs">{account.directory_url}</TableCell><TableCell><StatusBadge value={account.status} /></TableCell><TableCell className="text-right">{account.status !== 'active' && <Button variant="ghost" size="sm" loading={busy === `register-${account.id}`} onClick={() => void action(`register-${account.id}`, () => registerACMEAccount(account.id), 'ACME 账号注册成功')}><Play className="mr-2 h-4 w-4" />注册</Button>}</TableCell></TableRow>)}
            </TableBody></Table>
          </CardContent></Card>
          <Card><CardHeader><CardTitle>协议证书</CardTitle><CardDescription>签发完成后可在节点入站中切换到托管 TLS。</CardDescription></CardHeader><CardContent>
            <Table><TableHeader><TableRow><TableHead>域名</TableHead><TableHead>状态</TableHead><TableHead>有效期</TableHead><TableHead>版本</TableHead><TableHead className="text-right">操作</TableHead></TableRow></TableHeader><TableBody>
              {certificates.map((cert) => <TableRow key={cert.id}><TableCell className="font-medium">{cert.domains.join(', ')}</TableCell><TableCell><StatusBadge value={cert.status} />{cert.last_error && <div className="max-w-[320px] truncate text-xs text-red-400">{cert.last_error}</div>}</TableCell><TableCell>{cert.not_after_unix ? formatTime(cert.not_after_unix) : '尚未签发'}</TableCell><TableCell>r{cert.revision}</TableCell><TableCell className="text-right"><Button variant="ghost" size="sm" loading={busy === `issue-${cert.id}`} onClick={() => void action(`issue-${cert.id}`, () => issueProtocolCertificate(cert.id), '已提交签发/续期')}><RefreshCw className="mr-2 h-4 w-4" />签发 / 续期</Button></TableCell></TableRow>)}
            </TableBody></Table>
          </CardContent></Card>
        </TabsContent>

        <TabsContent value="jobs">
          <Card><CardHeader><CardTitle>持久化自动化任务</CardTitle><CardDescription>Panel 重启后会自动接管过期租约并继续执行。</CardDescription></CardHeader><CardContent>
            <Table><TableHeader><TableRow><TableHead>类型</TableHead><TableHead>目标</TableHead><TableHead>状态</TableHead><TableHead>尝试</TableHead><TableHead>更新时间</TableHead></TableRow></TableHeader><TableBody>
              {jobs.map((job) => <TableRow key={job.id}><TableCell>{job.type}</TableCell><TableCell className="max-w-[280px] truncate font-mono text-xs">{job.target_id}</TableCell><TableCell><StatusBadge value={job.state} />{job.last_error && <div className="max-w-[360px] truncate text-xs text-red-400">{job.last_error}</div>}</TableCell><TableCell>{job.attempt}</TableCell><TableCell>{formatTime(job.updated_at_unix)}</TableCell></TableRow>)}
            </TableBody></Table>
          </CardContent></Card>
        </TabsContent>
      </Tabs>
    </div>
  )
}

function DNSAccountForm({ providers, onCreate, busy }: { providers: DNSProviderMetadata[]; onCreate: (body: Parameters<typeof createDNSAccount>[0]) => void; busy: boolean }) {
  const [name, setName] = useState('')
  const [provider, setProvider] = useState('')
  const [credentials, setCredentials] = useState<Record<string, string>>({})
  const [settings, setSettings] = useState('{"test_zone":"example.com"}')
  useEffect(() => { if (!provider && providers[0]) setProvider(providers[0].name) }, [provider, providers])
  const metadata = providers.find((item) => item.name === provider)
  function submit(event: FormEvent) {
    event.preventDefault()
    try { onCreate({ name, provider, credentials, settings: JSON.parse(settings) as Record<string, unknown> }) } catch { toast.error('供应商设置必须是有效 JSON') }
  }
  return <Card><CardHeader><CardTitle>添加 DNS 账号</CardTitle></CardHeader><CardContent><form className="grid gap-3 md:grid-cols-2 xl:grid-cols-4" onSubmit={submit}>
    <Field label="名称"><Input value={name} onChange={(e) => setName(e.target.value)} required /></Field>
    <Field label="供应商"><select className={selectClass} value={provider} onChange={(e) => { setProvider(e.target.value); setCredentials({}) }}>{providers.map((item) => <option key={item.name} value={item.name}>{item.label}</option>)}</select></Field>
    {metadata?.credential_fields.map((field) => <Field key={field.name} label={field.label}><Input type={field.secret ? 'password' : 'text'} value={credentials[field.name] ?? ''} required={field.required} onChange={(e) => setCredentials((current) => ({ ...current, [field.name]: e.target.value }))} /></Field>)}
    <Field label="供应商设置 JSON"><Input value={settings} onChange={(e) => setSettings(e.target.value)} /></Field>
    <div className="flex items-end"><Button type="submit" loading={busy}><Plus className="mr-2 h-4 w-4" />添加</Button></div>
  </form></CardContent></Card>
}

function DomainForm({ nodes, accounts, onCreate, busy }: { nodes: Node[]; accounts: DNSAccount[]; onCreate: (body: Parameters<typeof createManagedDomain>[0]) => void; busy: boolean }) {
  const [nodeID, setNodeID] = useState('')
  const [accountID, setAccountID] = useState('')
  const [zone, setZone] = useState('')
  const [fqdn, setFQDN] = useState('')
  const [ipv4, setIPv4] = useState('')
  useEffect(() => { if (!nodeID && nodes[0]) setNodeID(nodes[0].id) }, [nodeID, nodes])
  useEffect(() => { if (!accountID && accounts[0]) setAccountID(accounts[0].id) }, [accountID, accounts])
  return <Card><CardHeader><CardTitle>添加托管域名</CardTitle></CardHeader><CardContent><form className="grid gap-3 md:grid-cols-3 xl:grid-cols-6" onSubmit={(e) => { e.preventDefault(); onCreate({ node_id: nodeID, dns_account_id: accountID, zone, fqdn, record_mode: 'a', address_source: ipv4 ? 'manual' : 'agent_public', manual_ipv4: ipv4, ttl: 300 }) }}>
    <Field label="节点"><select className={selectClass} value={nodeID} onChange={(e) => setNodeID(e.target.value)}>{nodes.map((node) => <option key={node.id} value={node.id}>{node.name}</option>)}</select></Field>
    <Field label="DNS 账号"><select className={selectClass} value={accountID} onChange={(e) => setAccountID(e.target.value)}>{accounts.map((account) => <option key={account.id} value={account.id}>{account.name}</option>)}</select></Field>
    <Field label="DNS 区域"><Input value={zone} onChange={(e) => setZone(e.target.value)} placeholder="example.com" required /></Field>
    <Field label="完整域名"><Input value={fqdn} onChange={(e) => setFQDN(e.target.value)} placeholder="edge.example.com" required /></Field>
    <Field label="手工 IPv4（留空由 Agent 探测）"><Input value={ipv4} onChange={(e) => setIPv4(e.target.value)} /></Field>
    <div className="flex items-end"><Button type="submit" loading={busy}><Plus className="mr-2 h-4 w-4" />添加</Button></div>
  </form></CardContent></Card>
}

function ACMEAccountForm({ onCreate, busy }: { onCreate: (body: Parameters<typeof createACMEAccount>[0]) => void; busy: boolean }) {
  const [name, setName] = useState("Let's Encrypt Staging")
  const [url, setURL] = useState('https://acme-staging-v02.api.letsencrypt.org/directory')
  const [email, setEmail] = useState('')
  return <Card><CardHeader><CardTitle>添加 ACME 账号</CardTitle><CardDescription>建议先使用 Staging 验证流程，避免生产限额。</CardDescription></CardHeader><CardContent><form className="space-y-3" onSubmit={(e) => { e.preventDefault(); onCreate({ name, directory_url: url, email, accept_terms: true }) }}>
    <Field label="名称"><Input value={name} onChange={(e) => setName(e.target.value)} required /></Field><Field label="Directory URL"><Input value={url} onChange={(e) => setURL(e.target.value)} required /></Field><Field label="联系邮箱"><Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} /></Field><Button type="submit" loading={busy}><Plus className="mr-2 h-4 w-4" />保存账号</Button>
  </form></CardContent></Card>
}

function CertificateForm({ domains, accounts, onCreate, busy }: { domains: ManagedDomain[]; accounts: ACMEAccount[]; onCreate: (body: Parameters<typeof createProtocolCertificate>[0]) => void; busy: boolean }) {
  const readyDomains = useMemo(() => domains.filter((item) => item.state === 'ready'), [domains])
  const activeAccounts = useMemo(() => accounts.filter((item) => item.status === 'active'), [accounts])
  const [domainID, setDomainID] = useState('')
  const [accountID, setAccountID] = useState('')
  useEffect(() => { if (!domainID && readyDomains[0]) setDomainID(readyDomains[0].id) }, [domainID, readyDomains])
  useEffect(() => { if (!accountID && activeAccounts[0]) setAccountID(activeAccounts[0].id) }, [accountID, activeAccounts])
  const domain = readyDomains.find((item) => item.id === domainID)
  return <Card><CardHeader><CardTitle>申请协议证书</CardTitle></CardHeader><CardContent><form className="space-y-3" onSubmit={(e) => { e.preventDefault(); if (domain) onCreate({ node_id: domain.node_id, managed_domain_id: domain.id, acme_account_id: accountID }) }}>
    <Field label="已就绪域名"><select className={selectClass} value={domainID} onChange={(e) => setDomainID(e.target.value)}>{readyDomains.map((item) => <option key={item.id} value={item.id}>{item.fqdn}</option>)}</select></Field><Field label="已注册 ACME 账号"><select className={selectClass} value={accountID} onChange={(e) => setAccountID(e.target.value)}>{activeAccounts.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></Field><Button type="submit" disabled={!domain || !accountID} loading={busy}><Plus className="mr-2 h-4 w-4" />申请证书</Button>
  </form></CardContent></Card>
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return <div className="space-y-1.5"><Label>{label}</Label>{children}</div>
}

function StatusBadge({ value }: { value: string }) {
  const success = ['active', 'ready', 'success', 'enabled'].includes(value)
  const danger = ['error', 'failed', 'disabled'].includes(value)
  const variant = success ? 'success' : danger ? 'destructive' : ['pending', 'running', 'authorizing', 'installing'].includes(value) ? 'info' : 'warning'
  const labels: Record<string, string> = { active: '有效', ready: '就绪', success: '成功', enabled: '启用', error: '错误', failed: '失败', disabled: '禁用', pending: '等待', running: '执行中', retry_wait: '等待重试', authorizing: '验证中', installing: '安装中' }
  return <Badge variant={variant}>{labels[value] ?? value}</Badge>
}
