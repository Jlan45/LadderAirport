# Design: Managed DNS, DDNS and ACME Protocol Certificates

**Date:** 2026-07-27
**Status:** Implemented in v0.10.0
**Baseline:** `v0.9.3` (`842b524`)
**Target release:** `v0.10.0`

## 1. Goal

Add a Panel-managed DNS and public certificate control plane:

1. Connect DNS accounts from supported providers.
2. Assign a managed hostname to a node/inbound endpoint.
3. Reconcile A and AAAA records from manual or Agent-discovered public addresses.
4. Complete ACME DNS-01 challenges from Panel.
5. Keep protocol certificate private keys on Agent.
6. Install and renew certificates without replacing a working certificate until the new deployment succeeds.
7. Use the managed hostname and SNI in subscriptions and proxy-chain hop resolution.

The existing management PKI remains independent:

| Certificate plane | Purpose | Issuer | Private key location |
|---|---|---|---|
| Management PKI | Panel ↔ Agent mTLS | LadderAirport management CA | Panel/Agent according to existing PKI design |
| Protocol certificate | Client ↔ proxy protocol TLS | Public ACME CA | Agent only |

No protocol certificate is issued by the management CA, and no ACME key is reused for management mTLS.

## 2. Product scope

### 2.1 In scope for v0.10.0

- Providers:
  - AliDNS
  - Tencent Cloud DNSPod
  - Cloudflare DNS
  - Generic HTTP Callback
- A and AAAA managed records.
- IPv4 and IPv6 managed independently.
- Address sources:
  - Manual address
  - Node control address when it is a literal IP
  - Agent public-address discovery
- ACME DNS-01 with:
  - Configurable directory URL
  - Contact email
  - Optional External Account Binding
  - Production and staging profiles
- One managed certificate per node + managed hostname.
- Certificate reuse by multiple TLS inbounds on the same node.
- Supported TLS inbounds:
  - Trojan
  - Hysteria2
  - TUIC
  - AnyTLS
  - VLESS with `tls_mode=tls`
  - VMess with `tls_mode=tls`
- Standalone node/inbound attachments and proxy-chain-owned node/inbound pairs.
- Persistent reconciliation, retry, audit and restart recovery.
- Chinese operator errors and status text.

### 2.2 Explicitly out of scope

- HTTP-01 and TLS-ALPN-01.
- Running ACME independently inside sing-box.
- Wildcard certificate UI.
- Arbitrary shell commands for address discovery.
- Automatic Cloudflare proxying; managed records are DNS-only.
- Automatic CNAME delegation of `_acme-challenge`.
- Bulk import from DDNS-GO configuration.
- Storing protocol private keys in Panel or SQLite.
- Multi-Panel active/active scheduling.

These can be added after the single-Panel state machine is stable.

## 3. Architecture

```text
                         ┌────────────────────────────┐
                         │           Panel            │
                         │                            │
 Operator ──────────────►│ DNS accounts + bindings    │
                         │ DNS reconciler             │──────► DNS provider API
                         │ ACME order manager          │──────► ACME directory
                         │ Persistent jobs + audit     │
                         └──────────────┬─────────────┘
                                        │ management mTLS gRPC
                                        ▼
                         ┌────────────────────────────┐
                         │            Agent           │
                         │ Public IP discovery        │
                         │ Protocol key + CSR         │
                         │ Versioned certificate store│
                         │ Atomic sing-box apply      │
                         └────────────────────────────┘
```

### 3.1 Ownership

Panel owns:

- DNS provider credentials.
- Desired DNS records.
- ACME account key.
- ACME order/challenge state.
- Certificate public metadata and chain.
- Scheduling, retry and audit.
- Node/inbound → hostname/certificate bindings.

Agent owns:

- Protocol certificate private keys.
- CSR generation.
- Versioned certificate/key files.
- Validation that an issued certificate matches its local private key.
- Atomic activation through the existing sing-box apply lifecycle.

### 3.2 DDNS-GO ideas retained

- Provider adapters behind a common interface.
- Separate address acquisition from record mutation.
- Skip writes when desired and observed values match.
- Periodic authoritative reconciliation even when cached values have not changed.
- Clear retry state after a failed provider write.
- Generic Callback provider for unsupported vendors.

The DDNS-GO timer loop, plaintext provider configuration and A/AAAA-only interface are not reused.

## 4. Domain and endpoint model

A managed domain belongs to one node and may serve multiple TLS inbounds on that node.

Node/inbound TLS automation is stored separately from global `inbounds.params_json`, because a global inbound can be deployed to multiple nodes with different domains and certificates.

Effective client endpoint priority:

1. Explicit proxy-chain hop `dial_address`.
2. Managed domain assigned to the node/inbound pair.
3. Existing per-inbound `public_address`.
4. Node `public_address`.
5. Node control `address`.

When a managed domain is active:

- Subscription server address uses its FQDN.
- TLS SNI uses its FQDN.
- Proxy-chain next-hop resolution uses its FQDN unless explicitly overridden.
- TLS certificate paths are injected into the per-node config build.

Global inbound parameters remain protocol credentials and defaults. Managed DNS/certificate state never mutates another node's deployment.

## 5. Persistence

### 5.1 `dns_accounts`

| Column | Purpose |
|---|---|
| `id`, `name`, `provider` | Identity and adapter name |
| `credentials_ciphertext` | Versioned AES-256-GCM envelope |
| `settings_json` | Non-secret provider options |
| `enabled` | Scheduling switch |
| `last_test_unix`, `last_test_error` | Credential test state |
| timestamps | Audit ordering |

API responses expose only `has_credentials` and provider-specific masked hints.

### 5.2 `managed_domains`

| Column | Purpose |
|---|---|
| `id`, `node_id`, `dns_account_id` | Ownership |
| `zone`, `fqdn` | Normalized punycode names |
| `record_mode` | `a`, `aaaa`, `dual` |
| `address_source` | `manual`, `node_address`, `agent_public` |
| `manual_ipv4`, `manual_ipv6` | Manual desired values |
| `ttl` | Requested TTL |
| `enabled` | Desired management state |
| `state` | Reconciliation state |
| observed/desired values | Last comparison |
| provider record IDs | Safe update/delete |
| `created_by_panel_*` | External deletion ownership |
| retry/error timestamps | Scheduler recovery |

Unique constraint: `(dns_account_id, fqdn, record_type)` for managed A/AAAA ownership.

### 5.3 `node_inbound_tls_bindings`

Primary key: `(node_id, inbound_id)`.

Fields:

- `managed_domain_id`
- `certificate_id`
- `mode`: `legacy` or `managed`
- timestamps

Application validation requires the node/inbound pair to be present either in `node_inbounds` or as a `proxy_chain_hops` owner. Delete/update operations clean stale bindings transactionally.

### 5.4 `acme_accounts`

- Directory URL and contact email.
- Encrypted ACME account private key.
- Optional encrypted EAB HMAC key and non-secret key ID.
- Registration URI/status.
- Terms acceptance timestamp.

ACME account keys are Panel control-plane keys, not protocol certificate keys.

### 5.5 `protocol_certificates`

| Column | Purpose |
|---|---|
| `id`, `node_id`, `managed_domain_id` | Certificate ownership |
| `domains_json` | Requested SAN set |
| `status` | Lifecycle state |
| `agent_key_id` | Opaque Agent key handle |
| `candidate_cert_path`, `candidate_key_path` | Staged generation |
| `active_cert_path`, `active_key_path` | Last successful generation |
| `cert_pem` | Public certificate chain |
| serial/fingerprint/validity | Operator status |
| `revision` | Included in deployment hash |
| renewal/retry fields | Scheduler |

Only public certificate material is stored in Panel.

### 5.6 `automation_jobs` and `automation_audit_logs`

Jobs persist operation, target, state, attempt, next run, lease owner/expiry and a redacted error.

Job types:

- `dns.reconcile`
- `certificate.issue`
- `certificate.renew`
- `certificate.deploy`
- `certificate.cleanup`

Audit records include actor, action, target IDs, outcome and redacted detail.

## 6. Provider abstraction

```go
type Provider interface {
    Test(ctx context.Context) error
    ResolveZone(ctx context.Context, fqdn string) (Zone, error)
    Lookup(ctx context.Context, zone Zone, name string, typ RecordType) ([]Record, error)
    Upsert(ctx context.Context, zone Zone, record Record) (RecordRef, error)
    Delete(ctx context.Context, zone Zone, ref RecordRef) error
}
```

Adapters are registered by name rather than selected by a central switch:

```go
registry.Register("alidns", alidns.New)
registry.Register("dnspod", dnspod.New)
registry.Register("cloudflare", cloudflare.New)
registry.Register("callback", callback.New)
```

Provider requirements:

- Context timeout on every request.
- Structured provider error classification: authentication, permission, rate limit, transient, permanent.
- Provider TTL normalization.
- No secret values in errors or logs.
- Per-account concurrency limit.
- Exact TXT value deletion for ACME cleanup.
- A/AAAA records default to DNS-only.

Callback variables:

```text
#{action}  lookup | upsert | delete
#{zone}
#{name}
#{type}    A | AAAA | TXT
#{value}
#{ttl}
```

Callback configuration includes method, URL, headers, JSON body template, timeout, accepted status codes and optional response matcher. URL schemes are limited to HTTPS by default; private-network targets require an explicit unsafe setting.

## 7. Credential encryption

- AES-256-GCM with random nonce and associated data containing record ID, provider and envelope version.
- Master key source:
  1. `LADDER_CREDENTIALS_KEY` when set.
  2. Otherwise generate `/var/lib/ladder-airport/secrets/credentials.key` with mode `0600`.
- Missing or invalid master key fails the DNS/ACME subsystem closed; it never overwrites encrypted values.
- Credential updates use “secret omitted means unchanged” semantics.
- Backups must include both SQLite and the master-key file.
- All API bodies, logs, task errors and audit details pass through provider-aware redaction.

## 8. Address discovery and DDNS reconciliation

### 8.1 Agent public-address RPC

Agent exposes `GetPublicAddresses`.

- Uses fixed HTTPS endpoints configured by Panel policy, never a shell command.
- Validates returned values with `netip`.
- IPv4 and IPv6 queried independently.
- Requires consensus when multiple endpoints return results.
- Returns observed addresses and diagnostic source names, never arbitrary response bodies.

Agent advertises capability `public-address-v1`.

### 8.2 Reconciliation

For each enabled managed domain:

1. Resolve desired IPv4/IPv6.
2. Validate address family and reject private/reserved values unless explicitly allowed.
3. Query provider record state.
4. Skip writes when the exact desired RRSet is already present.
5. Upsert changed records.
6. Query provider again.
7. Verify via authoritative DNS servers.
8. Mark ready or schedule retry.

Every domain is force-reconciled periodically even when the address cache is unchanged. Per-domain leases prevent duplicate work after Panel restart.

Deleting/disabling a managed domain:

- Records created by Panel may be deleted after explicit operator confirmation.
- Adopted pre-existing records are retained by default.
- ACME TXT records are always removed by exact value in a `defer`/cleanup job.

## 9. ACME flow

DNS must be ready before issuance starts.

```text
pending
  → preparing_key
  → ordering
  → presenting_challenge
  → waiting_propagation
  → finalizing
  → staging_on_agent
  → applying_config
  → active
```

Failure states retain the last active certificate:

```text
retry_wait | failed | deployment_failed
```

Detailed flow:

1. Panel asks Agent to prepare a key and CSR for the exact DNS SAN set.
2. Agent creates a P-256 key in a new versioned generation and returns:
   - Opaque key ID
   - CSR PEM
   - Public-key fingerprint
3. Panel creates an ACME order from the CSR.
4. Panel writes `_acme-challenge.<fqdn>` TXT using the selected provider.
5. Panel polls authoritative nameservers over UDP with TCP fallback.
6. Panel accepts the challenge and waits for authorization.
7. Panel finalizes the order and validates returned chain, SANs, EKU and validity.
8. Panel sends the public certificate chain to Agent.
9. Agent validates chain ↔ local key match and stages immutable certificate/key paths.
10. Panel builds node config with candidate paths and a deployment hash including certificate revision.
11. Existing Agent `ApplyConfig` starts the new box.
12. Only after successful apply does Panel mark the candidate active.
13. Old generations are retained until a later cleanup job.

If step 10 or 11 fails, the old box and old active paths remain authoritative.

## 10. Agent certificate store

Suggested layout:

```text
/var/lib/ladder-airport/protocol-certs/
└── <certificate-id>/
    ├── <generation-id>/
    │   ├── privkey.pem      0600
    │   ├── fullchain.pem    0640
    │   └── metadata.json    0600
    └── ...
```

Agent RPCs:

- `GetPublicAddresses`
- `PrepareProtocolCertificate`
- `InstallProtocolCertificate`
- `GetProtocolCertificateStatus`
- `DeleteProtocolCertificateGeneration`

Agent advertises capability `protocol-cert-v1`.

All RPCs use existing management mTLS and bearer authentication. Key IDs and paths are validated against strict IDs; callers cannot request arbitrary filesystem paths.

## 11. Config build and protocol behavior

`nodeconfig.Builder` loads node/inbound TLS bindings and creates a per-node copy of inbound params.

For a managed active certificate it overlays:

```text
tls_cert_path = active_cert_path
tls_key_path  = active_key_path
server_name   = managed fqdn
```

It does not mutate the global inbound row.

The deployment hash is:

```text
SHA256(config JSON + sorted active certificate IDs/revisions)
```

This guarantees renewal rebuilds the in-memory sing-box TLS configuration even when visible JSON fields would otherwise remain stable.

Legacy self-signed PEM/path behavior continues until an operator switches that node/inbound pair to managed mode.

## 12. API and UI

### 12.1 API groups

- `GET /api/v1/dns/providers`
- CRUD `/api/v1/dns/accounts`
- `POST /api/v1/dns/accounts/{id}/test`
- CRUD `/api/v1/managed-domains`
- `POST /api/v1/managed-domains/{id}/reconcile`
- CRUD `/api/v1/acme/accounts`
- `POST /api/v1/certificates/{id}/issue`
- `POST /api/v1/certificates/{id}/renew`
- `GET /api/v1/certificates`
- `GET /api/v1/automation/jobs`
- `GET /api/v1/automation/audit-logs`
- `PUT /api/v1/nodes/{node}/inbounds/{inbound}/tls-binding`

Mutating endpoints enqueue idempotent jobs and return the persisted job/result state.

### 12.2 UI

New “DNS 与证书” page:

- DNS accounts tab:
  - Dynamic provider credential form
  - Test connection
  - Masked secret state
- Managed domains tab:
  - Node, FQDN, record mode, address source, TTL
  - Desired/observed A/AAAA
  - Last reconciliation and error
- Certificates tab:
  - Domain, node, serial, issuer, validity, renewal state
  - Issue/renew actions
- Jobs/audit tab:
  - Phase, attempts, next retry, redacted detail

Node detail inbound editor:

- Legacy TLS / Managed TLS selector.
- Managed domain selector.
- Certificate readiness and Agent capability warning.
- Subscription endpoint preview.

Config preview:

- Uses the same builder as deployment.
- Shows managed certificate paths and revision.
- Never contains protocol private-key PEM for managed mode.
- Returns a clear “DNS/certificate not ready” validation error before apply.

## 13. Scheduling and failure handling

- DNS periodic reconciliation: default 5 minutes plus jitter.
- Authoritative forced verification: at least every 30 minutes.
- Certificate renewal target:
  - Earlier of two-thirds through lifetime or 30 days before expiry.
- Retry sequence:
  - 1 minute, 5 minutes, 15 minutes, 1 hour, 6 hours.
- Increased urgency below 14 and 7 days remaining.
- Job leases are reclaimed after expiry on Panel restart.
- One active DNS job per managed domain.
- One active issuance/deployment job per certificate.
- ACME TXT cleanup is persisted separately so cancellation or restart cannot strand challenges.

## 14. Migration and compatibility

- Additive SQLite migration only.
- Existing nodes, inbounds, proxy chains and subscriptions keep current behavior.
- Existing self-signed TLS material is not auto-deleted.
- Managed mode is opt-in per node/inbound pair.
- Old Agents remain operable but cannot enable managed certificates; UI requires an upgrade when capabilities are absent.
- Deleting a DNS account is blocked while referenced.
- Deleting a node first disables automation and records cleanup intent; external DNS deletion requires explicit confirmation.
- No one-shot migration release is required.

## 15. Security requirements

- Protocol private keys never cross Agent → Panel.
- Provider and ACME secrets encrypted at rest.
- No secret values in preview, API responses, logs or audit.
- Callback template expansion cannot add arbitrary local files or execute commands.
- Callback DNS resolution and redirect handling enforce SSRF policy.
- ACME directory URL is HTTPS except explicitly configured local test mode.
- Certificate install validates:
  - Public key match
  - Exact SAN set
  - ServerAuth EKU
  - Validity window
  - Parseable full chain
- DNS names are normalized to lowercase ASCII/punycode and validated before provider calls.
- Provider record ownership prevents accidental deletion of records not created by Panel.

## 16. Acceptance criteria

1. Operator can add and test all four provider types without secrets being returned.
2. A/AAAA changes reconcile and recover after Panel restart or temporary provider failure.
3. Agent-generated private key never appears in Panel database, API or logs.
4. ACME DNS-01 issuance succeeds and removes only its own TXT value.
5. A working certificate remains active when renewal, install or config apply fails.
6. All supported TLS protocols start with managed paths on Agent.
7. Subscription and proxy-chain endpoints use managed FQDN/SNI.
8. Nodes with old Agents receive a clear upgrade-required result.
9. Existing v0.9.3 installations start with unchanged behavior after upgrade.
10. Unit, integration, frontend and release verification pass.
