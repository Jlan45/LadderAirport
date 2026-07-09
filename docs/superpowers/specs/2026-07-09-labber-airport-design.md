# LabberAirport Design Spec

**Date:** 2026-07-09  
**Status:** Approved for implementation planning  
**Codename:** LabberAirport — self-hosted proxy fleet control plane

## 1. Goal

Build an operations platform for a self-hosted proxy node fleet:

- **Panel (Go):** control plane with embedded full Web UI (React SPA), SQLite, config abstraction, batch control.
- **Agent:** secondary development (**二开**) on **sing-box source code**, process-local proxy core + gRPC control server.
- **Control channel:** Panel dials each Agent over **gRPC** (TLS + shared token).
- **Config model:** protocol templates → form params → convert to full sing-box JSON → apply to nodes.

## 2. Non-Goals (v1)

- Multi-tenant SaaS / subscription billing / user plans (not an “airport” product for end users).
- Outbound chain modeling, selector/urltest UX, or complex route rule builder.
- Agent-side business templates (Agent never interprets protocol form params).
- Panel HA / multi-instance (single binary + SQLite only).
- GitOps file-driven config as primary control path.

## 3. Chosen Approach

**Panel as config hub; Agent as sing-box-fork executor.**

| Layer | Responsibility |
|-------|----------------|
| Panel | Templates, CRUD, conversion, SQLite, batch tasks, Web embed, gRPC client |
| Agent (sing-box 二开) | gRPC server, box lifecycle, metrics/log hooks, run proxy traffic |
| Web | Dynamic forms, node/batch ops UI |

Rejected alternatives:

- Dual-end template schema (version drift risk).
- GitOps-primary control (weak fit for live start/stop/logs).

## 4. Architecture

### 4.1 Repository layout (monorepo)

```
LabberAirport/
├── proto/                      # gRPC: panel ↔ agent
├── pkg/                        # shared: token auth helpers, error codes
├── panel/
│   ├── cmd/panel/
│   ├── internal/               # api, store, converter, nodeclient, batch
│   └── web/dist/               # go:embed SPA build output
├── web/                        # React + TS + Vite source
├── agent/                      # sing-box source tree (submodule or vendored fork)
│   ├── (upstream sing-box tree)
│   ├── cmd/labber-agent/       # 二开 entry (name may vary)
│   └── internal/control/       # gRPC, auth, runtime wrapper, metrics, logs
└── docs/superpowers/specs/
```

Agent integration strategy: **prefer in-monorepo submodule/vendor of upstream sing-box** with local packages for control plane. Document upstream base version and merge/upgrade policy.

### 4.2 Runtime topology

```
Browser --HTTPS--> Panel (Go + embed SPA + SQLite)
                      |
                      | gRPC (TLS + Bearer token)
                      +-----> Agent-1 (sing-box 二开 + lib/runtime in-process)
                      +-----> Agent-2
                      '-----> Agent-N
```

- Nodes must be **reachable from Panel** (public IP, VPN, or L3 path).
- Browser **never** connects directly to Agents; logs/metrics are proxied via Panel.

### 4.3 Core data flow

1. Operator selects inbound protocol template in Web → fills form → saves `InboundConfig`.
2. Operator attaches configs to node(s) (or by label).
3. On Apply: Panel loads enabled inbounds for node → **converter** emits full sing-box JSON → `ConfigSnapshot` → gRPC `ApplyConfig`.
4. Agent validates/loads config into box runtime; on failure keeps previous instance.
5. Batch: Panel creates `Task`, fans out RPCs with bounded concurrency, records per-node results.

## 5. Data Model (SQLite)

### 5.1 Entities

| Entity | Purpose |
|--------|---------|
| `Node` | `id`, `name`, `address`, `grpc_port`, optional per-node `token`, `labels` (JSON), TLS client options, cached `status`, `last_seen`, last `config_hash` |
| `InboundConfig` | `id`, `name`, `protocol` (`shadowsocks` \| `trojan` \| `vless` \| `hysteria2`), `params` (JSON), `enabled`, timestamps |
| `NodeInbound` | M2M: which inbound configs apply to which nodes |
| `ConfigSnapshot` | Generated full JSON, hash, `node_id`, `created_at`, optional task id |
| `Task` | Batch job: type (`apply`/`start`/`stop`), targets, overall status, per-node results JSON |
| `PanelSettings` | Default agent token, gRPC timeout, max concurrency, admin credential hash, listen addr |

### 5.2 Outbound / route (fixed)

Converter always injects minimal:

- `outbounds`: direct (and any sing-box-required defaults such as block/dns if needed for validity).
- `route.final`: direct.

Operators do **not** model outbounds or routes in v1.

### 5.3 Protocol templates (built-in only)

Templates are **code/static schema**, not user-editable schema documents.

| Protocol | Template ID (example) | Primary form fields |
|----------|----------------------|---------------------|
| Shadowsocks | `inbound.shadowsocks.v1` | listen, port, method, password, network |
| Trojan | `inbound.trojan.v1` | listen, port, password, TLS cert/key or paths, optional fallback |
| VLESS | `inbound.vless.v1` | listen, port, uuid, optional flow; TLS mode **or Reality** (public_key, short_id, server_names, handshake server, etc.) |
| Hysteria2 | `inbound.hysteria2.v1` | listen, port, password/users, TLS, optional bandwidth fields |

- Reality is a **TLS mode on VLESS**, not a separate protocol row.
- Frontend: `GET /api/v1/templates` → JSON Schema-like field defs → dynamic form.
- Backend converter maps `params` + `template_id`/protocol version → sing-box inbound object(s).

### 5.4 Conversion rules

**Input:** all enabled `InboundConfig` rows linked to a node.  
**Output:** one valid sing-box config document.

Pre-flight checks (fail before enqueue/apply):

- No enabled inbounds (optional: allow empty config only for Stop-like clear — v1: reject Apply with clear error).
- Listen/port conflicts across inbounds on same node.
- Required fields per protocol; UUID format; port range.

On success: persist `ConfigSnapshot`, then RPC apply with `config_json` + `config_hash`.

## 6. gRPC Control Plane

### 6.1 Security

- Transport: **TLS** (Agent server cert; Panel verifies via CA or configurable insecure skip for lab only).
- Auth: metadata `authorization: Bearer <token>`.
- Per-node token overrides panel default when set.
- Failed auth → immediate RPC error; node marked unauthorized in cache.

### 6.2 Service `AgentControl`

| RPC | Type | Purpose |
|-----|------|---------|
| `Ping` | Unary | Liveness; agent version; upstream sing-box base version |
| `ApplyConfig` | Unary | Full JSON replace/reload |
| `Start` | Unary | Start box if stopped |
| `Stop` | Unary | Stop box; process keeps serving gRPC |
| `GetStatus` | Unary | running/stopped, config hash, started_at, last_error |
| `GetMetrics` | Unary | connections, uplink/downlink bytes, process CPU/RSS |
| `StreamLogs` | Server stream | Ring buffer tail + live lines; optional level filter |

`ApplyConfigRequest` fields:

- `config_json` — full sing-box configuration
- `config_hash` — panel-side hash for reconciliation
- `replace` — full replace of running config (v1 always true)

`ApplyConfig` failure must **not** tear down a healthy previous instance when reload/replace fails mid-way (best-effort keep-old).

### 6.3 Batch semantics (Panel-only)

- No batch RPC on Agent.
- Panel `max_concurrency` (default 10).
- Task status: `pending` → `running` → `success` | `partial` | `failed`.
- One node failure does not cancel siblings.

### 6.4 Agent (sing-box 二开) internals

```
gRPC Server
  → TLS + Token interceptor
  → Runtime Manager (wrap box.Box / 二开 Runtime)
  → Metrics (hooks / clash API / process stats — concrete hook chosen in plan)
  → Log tee → ring buffer → StreamLogs hub
  → Optional on-disk cache of last good JSON (crash recovery aid; Panel remains SoT)
```

二开 constraints:

- Control packages live inside the sing-box source tree entrypoint.
- Do not embed Web/SQLite into Agent.
- Do not execute arbitrary shell from Panel.
- Expose versions via `Ping` for compatibility checks.

## 7. Panel HTTP API & Web

### 7.1 Embedding

- React + TypeScript + Vite.
- Production assets under `panel/web/dist`, embedded with `go:embed`.
- SPA fallback to `index.html` for non-API routes.
- Dev: Vite proxy to Panel API.

### 7.2 Auth (Panel operators)

- Separate from agent token.
- v1: single admin password (hashed in SQLite settings) → session JWT or secure cookie.
- All `/api/v1/*` (except login) require auth.

### 7.3 API surface (representative)

| Area | Endpoints |
|------|-----------|
| Auth | `POST /api/v1/auth/login` |
| Templates | `GET /api/v1/templates` |
| Inbounds | CRUD `/api/v1/inbounds` |
| Nodes | CRUD `/api/v1/nodes`; probe; cached status/metrics refresh |
| Attachment | `/api/v1/nodes/{id}/inbounds` |
| Apply | `POST /api/v1/nodes/{id}/apply`; `POST .../config/preview` |
| Batch | `POST /api/v1/batch/apply|start|stop` (by ids or labels) |
| Tasks | `GET /api/v1/tasks`, `GET /api/v1/tasks/{id}` |
| Logs | `GET /api/v1/nodes/{id}/logs` (SSE or WebSocket; Panel bridges gRPC stream) |

### 7.4 Web pages (MVP)

1. Node list — status, labels, quick apply/start/stop  
2. Inbound configs — template picker + dynamic form  
3. Node detail — attachments, JSON preview, metrics, log stream  
4. Batch — multi-select / by label → task results  
5. Settings — default token, timeouts, concurrency, admin password  

## 8. Error Handling

| Layer | Behavior |
|-------|----------|
| Form / template | Client + server schema validation |
| Converter | Structured errors (port conflict, missing Reality fields, etc.) |
| gRPC | Timeout, unreachable, unauthorized, apply failure → per-node task result |
| Batch | `partial` when mixed outcomes |
| UI | Toasts + task detail; retry single node where applicable |

## 9. Testing Strategy

| Layer | Coverage |
|-------|----------|
| Converter unit tests | Four protocols → JSON golden files; conflict/invalid cases |
| Store unit tests | SQLite CRUD, M2M, task state machine |
| gRPC tests | Auth interceptor; Apply/Stop against mock Runtime |
| HTTP API tests | httptest + temp SQLite |
| Agent | Mock Runtime in unit tests; full 二开 binary build documented; optional CI job |
| Web | Optional Vitest for schema form mapping |

Default CI: panel packages + proto + converter/store/API tests. Full sing-box 二开 compile may be optional/manual if toolchain is heavy.

## 10. Acceptance Criteria

1. Single Panel binary serves embedded Web; admin can log in.  
2. Create inbounds via four templates; attach to nodes; preview JSON.  
3. Apply to a running 二开 Agent successfully; status shows running + matching hash.  
4. Batch apply/start/stop by labels with visible per-node results.  
5. Metrics visible; log stream works through Panel proxy.  
6. Wrong agent token cannot control the node.  
7. Failed Apply leaves previous working config when replace fails after old instance existed.

## 11. Implementation Phasing (for planning skill)

Suggested build order (not optional scope cut — full F is in scope; order reduces risk):

1. `proto` + shared auth helpers  
2. Agent skeleton in sing-box tree: gRPC + mock/no-op runtime → real box apply  
3. Panel store + converter + HTTP API (no UI)  
4. nodeclient + batch tasks  
5. Web SPA + embed  
6. Metrics/logs polish + e2e docs  

## 12. Open Implementation Details (resolved during plan, not product ambiguity)

- Exact sing-box upstream tag and module path for 二开.  
- Precise metrics extraction API inside sing-box.  
- SSE vs WebSocket for log proxy (prefer SSE if simpler).  
- Certificate bootstrap UX (file paths on Agent; Panel TLS verify flags).

These do not change product scope; implementers pick concrete libraries and document them in the plan.
