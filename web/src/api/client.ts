/** Panel API client — all calls use credentials: 'include' for session cookie. */

const API = '/api/v1'

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
    headers: {},
  }
  if (body !== undefined) {
    ;(opts.headers as Record<string, string>)['Content-Type'] = 'application/json'
    opts.body = JSON.stringify(body)
  }
  const res = await fetch(`${API}${path}`, opts)
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

// --- Types ---

export interface Node {
  id: string
  name: string
  address: string
  grpc_port: number
  token?: string
  labels: string[]
  tls_skip_verify: boolean
  ca_cert_pem?: string
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
  address: string
  grpc_port?: number
  token?: string
  labels?: string[]
  tls_skip_verify?: boolean
  ca_cert_pem?: string
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

// --- Fleet (multi-node overview / refresh) ---

export function fleetOverview(): Promise<FleetOverview> {
  return request('GET', '/fleet/overview')
}

export function fleetRefresh(): Promise<FleetOverview> {
  return request('POST', '/fleet/refresh')
}

// --- Nodes ---

export function listNodes(): Promise<Node[]> {
  return request('GET', '/nodes')
}

export function createNode(body: CreateNodeInput): Promise<Node> {
  return request('POST', '/nodes', body)
}

export function updateNode(id: string, body: Partial<Node>): Promise<Node> {
  return request('PUT', `/nodes/${id}`, body)
}

export function deleteNode(id: string): Promise<void> {
  return request('DELETE', `/nodes/${id}`)
}

export function probeNode(id: string): Promise<ProbeResult> {
  return request('POST', `/nodes/${id}/probe`)
}

export function listNodeInbounds(id: string): Promise<InboundConfig[]> {
  return request('GET', `/nodes/${id}/inbounds`)
}

export function setNodeInbounds(
  id: string,
  inbound_ids: string[],
): Promise<InboundConfig[]> {
  return request('PUT', `/nodes/${id}/inbounds`, { inbound_ids })
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
