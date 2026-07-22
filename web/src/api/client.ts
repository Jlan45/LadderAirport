/** Panel API client — all calls use credentials: 'include' for session cookie. */

const API = '/api/v1'

export const AUTH_EXPIRED_EVENT = 'ladder-airport:auth-expired'

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
    this.name = 'ApiError'
  }
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const opts: RequestInit = {
    method,
    credentials: 'include',
    headers: { Accept: 'application/json' },
  }
  if (body !== undefined) {
    ;(opts.headers as Record<string, string>)['Content-Type'] = 'application/json'
    opts.body = JSON.stringify(body)
  }
  const res = await fetch(`${API}${path}`, opts)
  notifyAuthExpired(res.status, path)
  if (res.status === 204) {
    return undefined as T
  }
  const text = await res.text()
  let data: unknown = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = { error: text }
    }
  }
  if (!res.ok) {
    const msg =
      data && typeof data === 'object' && data !== null && 'error' in data
        ? String((data as { error: unknown }).error)
        : res.statusText || `HTTP ${res.status}`
    throw new ApiError(res.status, msg)
  }
  return data as T
}

function notifyAuthExpired(status: number, path: string) {
  if (status !== 401 || path === '/auth/login' || typeof window === 'undefined') return
  window.dispatchEvent(new CustomEvent(AUTH_EXPIRED_EVENT))
}

async function requestText(path: string): Promise<string> {
  const res = await fetch(`${API}${path}`, {
    credentials: 'include',
    headers: { Accept: 'text/plain, application/json' },
  })
  notifyAuthExpired(res.status, path)
  const text = await res.text()
  if (!res.ok) {
    let message = text || res.statusText || `HTTP ${res.status}`
    try {
      const data = JSON.parse(text) as { error?: unknown }
      if (data.error != null) message = String(data.error)
    } catch {
      // Keep the plain-text response.
    }
    throw new ApiError(res.status, message)
  }
  return text
}

// --- Types ---

/** Node-level NAT rewrite: agent listen port → external public port for subscriptions. */
export interface PortMapping {
  listen_port: number
  public_port: number
}

export interface Node {
  id: string
  name: string
  address: string
  grpc_port: number
  /** Client-facing host for subscriptions; empty falls back to address. */
  public_address?: string
  /**
   * NAT port map for subscription clients.
   * Agent still listens on inbound params.port; only subscription server_port is rewritten.
   */
  port_mappings?: PortMapping[]
  token?: string
  labels: string[]
  tls_skip_verify: boolean
  ca_cert_pem?: string
  /** Host NIC for sing-box direct bind_interface; empty = OS default route. */
  egress_interface?: string
  status: string
  last_seen_unix: number
  config_hash: string
  runtime_state?: string
  agent_version?: string
  singbox_version?: string
  connections?: number
  uplink_bytes?: number
  downlink_bytes?: number
  cpu_percent?: number
  memory_rss_bytes?: number
  metrics_at_unix?: number
  last_error?: string
  inbound_count?: number
  created_at_unix: number
  updated_at_unix: number
}

export interface NetworkInterface {
  name: string
  addresses: string[]
  up: boolean
  loopback: boolean
  mtu?: number
  hardware_addr?: string
}

export interface FleetOverview {
  total_nodes: number
  online_nodes: number
  offline_nodes: number
  running_nodes: number
  nodes: Node[]
  refreshed_at: number
}

export interface InboundConfig {
  id: string
  name: string
  protocol: string
  params: Record<string, unknown>
  enabled: boolean
  created_at_unix: number
  updated_at_unix: number
}

/** Inbound attached to a node, with optional subscription NAT overrides. */
export interface NodeInboundAttachment extends InboundConfig {
  /** Client-facing host for this inbound; empty falls back to node public_address/address. */
  public_address?: string
  /** Client-facing NAT port; 0/empty uses inbound listen port (and legacy node port_mappings). */
  public_port?: number
}

export interface NodeInboundBinding {
  inbound_id: string
  public_address?: string
  public_port?: number
}

export interface Field {
  name: string
  label: string
  type: string // string|int|bool|select|password
  required: boolean
  default?: unknown
  options?: string[]
  description?: string
}

export interface Template {
  id: string
  protocol: string
  name: string
  fields: Field[]
}

export interface TaskNodeResult {
  node_id: string
  ok: boolean
  message: string
}

export interface Task {
  id: string
  type: string
  status: string
  node_ids: string[]
  results: TaskNodeResult[]
  created_at_unix: number
  updated_at_unix: number
}

export interface Settings {
  default_agent_token: string
  grpc_timeout_sec: number
  max_concurrency: number
  listen_addr: string
  public_base_url?: string
}

export interface Subscription {
  id: string
  name: string
  format: 'clash' | 'singbox' | string
  token: string
  inbound_ids: string[]
  /** True includes every enabled local inbound; false allows an external-only subscription. */
  include_all_inbounds: boolean
  external_source_ids?: string[]
  enabled: boolean
  url?: string
  created_at_unix: number
  updated_at_unix: number
}

export interface ExternalSource {
  id: string
  name: string
  url: string
  headers?: Record<string, string>
  enabled: boolean
  refresh_interval_sec: number
  last_fetch_unix: number
  last_success_unix: number
  last_error?: string
  content_type?: string
  cached_proxy_count: number
  created_at_unix: number
  updated_at_unix: number
}

export interface ProbeResult {
  node: Node
  agent_version: string
  singbox_version: string
}

export interface Metrics {
  connections: number
  uplink_bytes: number
  downlink_bytes: number
  cpu_percent: number
  memory_rss_bytes: number
}

export interface CreateNodeInput {
  name: string
  address?: string
  grpc_port?: number
  public_address?: string
  token?: string
  labels?: string[]
  tls_skip_verify?: boolean
  ca_cert_pem?: string
}

export interface UpdateNodeInput {
  name?: string
  address?: string
  grpc_port?: number
  public_address?: string
  token?: string
  labels?: string[]
  tls_skip_verify?: boolean
  ca_cert_pem?: string
  egress_interface?: string
  port_mappings?: PortMapping[]
}

export interface BootstrapNodeInput {
  name: string
  address?: string
  grpc_port?: number
  public_address?: string
  token?: string
  labels?: string[]
  enable_tls?: boolean
  agent_version?: string
  tls_skip_verify?: boolean
  ca_cert_pem?: string
}

export interface NodeInstallInfo {
  node: Node
  token: string
  enable_tls: boolean
  install_command: string
  upgrade_command?: string
  uninstall_command?: string
  steps: string[]
  panel_base_url?: string
  enroll_enabled?: boolean
  recommended_agent_version?: string
  outdated?: boolean
}

export interface MetaInfo {
  panel_version?: string
  panel_commit?: string
  recommended_agent_version?: string
  agent_upgrade_command: string
  agent_uninstall_command: string
  source: string
  checked_at_unix?: number
}

export interface CreateInboundInput {
  name: string
  protocol: string
  params: Record<string, unknown>
  enabled?: boolean
}

export interface PutSettingsInput {
  default_agent_token?: string
  grpc_timeout_sec?: number
  max_concurrency?: number
  listen_addr?: string
  public_base_url?: string
  new_password?: string
}

export interface BatchRequest {
  node_ids?: string[]
  labels?: string[]
}

// --- Auth ---

export function login(password: string): Promise<{ ok: boolean }> {
  return request('POST', '/auth/login', { password })
}

export function logout(): Promise<void> {
  return request('POST', '/auth/logout')
}

// --- Fleet (multi-node overview / refresh) ---

export function fleetOverview(): Promise<FleetOverview> {
  return request('GET', '/fleet/overview')
}

export function fleetRefresh(): Promise<FleetOverview> {
  return request('POST', '/fleet/refresh')
}

/** Recommended agent version + upgrade/uninstall one-liners. */
export function getMeta(): Promise<MetaInfo> {
  return request('GET', '/meta')
}

// --- Nodes ---

export function listNodes(): Promise<Node[]> {
  return request('GET', '/nodes')
}

export function createNode(body: CreateNodeInput): Promise<Node> {
  return request('POST', '/nodes', body)
}

/** Create node + return one-click agent install command. */
export function bootstrapNode(body: BootstrapNodeInput): Promise<NodeInstallInfo> {
  return request('POST', '/nodes/bootstrap', body)
}

/** Regenerate install command for an existing node. */
export function getNodeInstallCommand(
  id: string,
  opts?: { tls?: boolean; version?: string },
): Promise<NodeInstallInfo> {
  const q = new URLSearchParams()
  if (opts?.tls === false) q.set('tls', '0')
  if (opts?.tls === true) q.set('tls', '1')
  if (opts?.version) q.set('version', opts.version)
  const qs = q.toString()
  return request('GET', `/nodes/${id}/install-command${qs ? `?${qs}` : ''}`)
}

export function updateNode(id: string, body: UpdateNodeInput): Promise<Node> {
  return request('PUT', `/nodes/${id}`, body)
}

export function deleteNode(id: string): Promise<void> {
  return request('DELETE', `/nodes/${id}`)
}

export function probeNode(id: string): Promise<ProbeResult> {
  return request('POST', `/nodes/${id}/probe`)
}

export interface UpgradeAgentResult {
  ok: boolean
  message: string
  version?: string
  staged_path?: string
  previous_version?: string
  node_id?: string
  hint?: string
}

/** Stage remote agent upgrade (binary download + helper restart). */
export function upgradeNode(
  id: string,
  body?: { version?: string; repo?: string; download_url?: string; sha256?: string },
): Promise<UpgradeAgentResult> {
  return request('POST', `/nodes/${id}/upgrade`, body ?? {})
}

export function listNodeInbounds(id: string): Promise<NodeInboundAttachment[]> {
  return request('GET', `/nodes/${id}/inbounds`)
}

export interface SetNodeInboundsResult {
  inbounds: InboundConfig[]
  attachments?: NodeInboundAttachment[]
  deployed: boolean
  deploy_message?: string
  apply_task?: Task
  start_task?: Task
}

/** Attach inbounds (legacy IDs only) and auto-apply+start on the agent. */
export function setNodeInbounds(
  id: string,
  inbound_ids: string[],
  opts?: { skip_deploy?: boolean },
): Promise<SetNodeInboundsResult> {
  return request('PUT', `/nodes/${id}/inbounds`, {
    inbound_ids,
    skip_deploy: opts?.skip_deploy ?? false,
  })
}

/** Attach inbounds with per-inbound public host/port overrides. */
export function setNodeInboundBindings(
  id: string,
  bindings: NodeInboundBinding[],
  opts?: { skip_deploy?: boolean },
): Promise<SetNodeInboundsResult> {
  return request('PUT', `/nodes/${id}/inbounds`, {
    bindings,
    skip_deploy: opts?.skip_deploy ?? false,
  })
}

export function previewNodeConfig(id: string): Promise<unknown> {
  return request('POST', `/nodes/${id}/config/preview`)
}

export function applyNode(id: string): Promise<Task> {
  return request('POST', `/nodes/${id}/apply`)
}

export function startNode(id: string): Promise<Task> {
  return request('POST', `/nodes/${id}/start`)
}

export function stopNode(id: string): Promise<Task> {
  return request('POST', `/nodes/${id}/stop`)
}

export function getNodeMetrics(id: string): Promise<Metrics> {
  return request('GET', `/nodes/${id}/metrics`)
}

export function listNodeInterfaces(id: string): Promise<{ interfaces: NetworkInterface[] }> {
  return request('GET', `/nodes/${id}/interfaces`)
}

/**
 * Stream node logs via fetch + ReadableStream so the session cookie is sent.
 * EventSource does not reliably include credentials in all browsers.
 * Yields parsed SSE `data:` payloads (JSON objects with level/message/ts).
 */
export async function streamNodeLogs(
  id: string,
  opts: {
    level?: string
    tail?: number
    signal?: AbortSignal
    onLine: (line: { level: string; message: string; ts: number }) => void
  },
): Promise<void> {
  const qs = new URLSearchParams()
  if (opts.level) qs.set('level', opts.level)
  if (opts.tail != null) qs.set('tail', String(opts.tail))
  const q = qs.toString()
  const url = `${API}/nodes/${id}/logs${q ? `?${q}` : ''}`
  const res = await fetch(url, {
    method: 'GET',
    credentials: 'include',
    signal: opts.signal,
    headers: { Accept: 'text/event-stream' },
  })
  notifyAuthExpired(res.status, `/nodes/${id}/logs`)
  if (!res.ok) {
    const text = await res.text()
    let msg = res.statusText
    try {
      const j = JSON.parse(text)
      if (j.error) msg = j.error
    } catch {
      if (text) msg = text
    }
    throw new ApiError(res.status, msg)
  }
  if (!res.body) {
    throw new ApiError(500, 'no response body for log stream')
  }
  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    const parts = buffer.split('\n')
    buffer = parts.pop() ?? ''
    for (const raw of parts) {
      const line = raw.replace(/\r$/, '')
      if (line.startsWith('data:')) {
        const payload = line.slice(5).trim()
        if (!payload) continue
        try {
          const obj = JSON.parse(payload) as {
            level: string
            message: string
            ts: number
          }
          opts.onLine(obj)
        } catch {
          // ignore non-JSON data lines
        }
      }
    }
  }
}

// --- Inbounds ---

export function listInbounds(): Promise<InboundConfig[]> {
  return request('GET', '/inbounds')
}

export function createInbound(body: CreateInboundInput): Promise<InboundConfig> {
  return request('POST', '/inbounds', body)
}

export function updateInbound(
  id: string,
  body: Partial<InboundConfig>,
): Promise<InboundConfig> {
  return request('PUT', `/inbounds/${id}`, body)
}

export function deleteInbound(id: string): Promise<void> {
  return request('DELETE', `/inbounds/${id}`)
}

// --- Templates ---

export function listTemplates(): Promise<Template[]> {
  return request('GET', '/templates')
}

// --- Batch ---

export function batchApply(body: BatchRequest): Promise<Task> {
  return request('POST', '/batch/apply', body)
}

export function batchStart(body: BatchRequest): Promise<Task> {
  return request('POST', '/batch/start', body)
}

export function batchStop(body: BatchRequest): Promise<Task> {
  return request('POST', '/batch/stop', body)
}

// --- Tasks ---

export function listTasks(): Promise<Task[]> {
  return request('GET', '/tasks')
}

export function getTask(id: string): Promise<Task> {
  return request('GET', `/tasks/${id}`)
}

// --- Settings ---

export function getSettings(): Promise<Settings> {
  return request('GET', '/settings')
}

export function putSettings(body: PutSettingsInput): Promise<Settings> {
  return request('PUT', '/settings', body)
}

// --- Subscriptions ---

export function listSubscriptions(): Promise<Subscription[]> {
  return request('GET', '/subscriptions')
}

export function createSubscription(body: {
  name: string
  format: string
  inbound_ids?: string[]
  include_all_inbounds?: boolean
  external_source_ids?: string[]
  enabled?: boolean
}): Promise<Subscription> {
  return request('POST', '/subscriptions', body)
}

export function updateSubscription(
  id: string,
  body: {
    name?: string
    format?: string
    inbound_ids?: string[]
    include_all_inbounds?: boolean
    external_source_ids?: string[]
    enabled?: boolean
    rotate_token?: boolean
  },
): Promise<Subscription> {
  return request('PUT', `/subscriptions/${id}`, body)
}

export function deleteSubscription(id: string): Promise<void> {
  return request('DELETE', `/subscriptions/${id}`)
}

export async function previewSubscription(id: string): Promise<string> {
  return requestText(`/subscriptions/${id}/preview`)
}

// --- External subscription sources ---

export function listExternalSources(): Promise<ExternalSource[]> {
  return request('GET', '/external-sources')
}

export function createExternalSource(body: {
  name: string
  url: string
  headers?: Record<string, string>
  enabled?: boolean
  refresh_interval_sec?: number
}): Promise<ExternalSource> {
  return request('POST', '/external-sources', body)
}

export function updateExternalSource(
  id: string,
  body: {
    name?: string
    url?: string
    headers?: Record<string, string>
    enabled?: boolean
    refresh_interval_sec?: number
  },
): Promise<ExternalSource> {
  return request('PUT', `/external-sources/${id}`, body)
}

export function deleteExternalSource(id: string): Promise<void> {
  return request('DELETE', `/external-sources/${id}`)
}

export function refreshExternalSource(id: string): Promise<ExternalSource> {
  return request('POST', `/external-sources/${id}/refresh`)
}

export function previewExternalSource(id: string): Promise<{
  count: number
  names: string[]
  warnings?: string[]
  source: Partial<ExternalSource>
}> {
  return request('GET', `/external-sources/${id}/preview`)
}
