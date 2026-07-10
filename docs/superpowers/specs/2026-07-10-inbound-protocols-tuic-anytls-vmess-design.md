# Design: Inbound Protocols TUIC / AnyTLS / VMess

**Date:** 2026-07-10  
**Status:** Approved for implementation (extends v1 control plane)  
**Parent:** [2026-07-09-ladder-airport-design.md](./2026-07-09-ladder-airport-design.md)

## 1. Goal

Complete product support for three additional inbound protocols so operators can create, apply, and subscribe to them end-to-end:

| Protocol | sing-box type | Notes |
|----------|---------------|--------|
| **TUIC** | `tuic` | QUIC; needs Agent `with_quic` |
| **AnyTLS** | `anytls` | Since sing-box **1.12.0** |
| **VMess** | `vmess` | Works on default registries |

Panel still owns templates → params → full JSON; Agent only runs JSON.

## 2. Scope

### In scope

1. Built-in templates (`GET /api/v1/templates`).
2. Secret auto-fill (`inboundfill`).
3. Converter → valid sing-box inbound objects + unit golden tests.
4. Subscription export: Clash (Mihomo) + sing-box client configs.
5. Reject unknown protocol IDs on create/update (template must exist).
6. Agent runtime readiness:
   - Pin sing-box **≥ 1.12** so AnyTLS type exists (target **v1.12.22**).
   - Default release/CI/Makefile build tags include **`with_quic`** and **`with_utls`** so TUIC/Hy2 and uTLS client paths work out of the box.
7. Docs: root README, agent README, this design, parent design protocol table update.

### Out of scope (YAGNI)

- Multi-user / multi-password per inbound.
- VMess/VLESS transports (WS/gRPC/HTTPUpgrade).
- ShadowTLS, Naive, TUIC advanced QUIC tuning.
- Custom padding schemes for AnyTLS.
- Upgrading past 1.12.x to 1.13.x in this change (can follow later).

## 3. Data model

`InboundConfig.protocol` allowed values (built-in only):

```
shadowsocks | trojan | vless | hysteria2 | tuic | anytls | vmess
```

No schema migration: `protocol` remains free TEXT; validation is application-level via `templates.Get`.

### Params (operator-visible + auto-filled secrets)

**TUIC**

| Field | Required | Auto | Notes |
|-------|----------|------|--------|
| listen | no | default `0.0.0.0` | |
| port | yes | | |
| uuid | yes | generate | |
| password | yes | generate | |
| congestion_control | no | `cubic` | `cubic` \| `new_reno` \| `bbr` |
| server_name | no | | client SNI |
| tls_cert_pem / tls_key_pem or paths | yes | self-signed PEM | |

**AnyTLS**

| Field | Required | Auto | Notes |
|-------|----------|------|--------|
| listen / port | as usual | | |
| password | yes | generate | |
| server_name | no | | |
| TLS material | yes | self-signed PEM | |

**VMess**

| Field | Required | Auto | Notes |
|-------|----------|------|--------|
| listen / port | as usual | | |
| uuid | yes | generate | |
| alter_id | no | `0` | AEAD when 0 |
| tls_mode | no | `none` | `none` \| `tls` |
| server_name | no | | when tls |
| TLS material | if tls | self-signed PEM | |

## 4. Converter mapping (server inbound)

Minimal sing-box shapes (single default user):

- **tuic:** `users: [{name, uuid, password}]`, `congestion_control`, required `tls`.
- **anytls:** `users: [{name, password}]`, required `tls`.
- **vmess:** `users: [{name, uuid, alterId}]`, optional `tls` when `tls_mode=tls`.

Port conflict and required-field checks reuse existing converter rules.

## 5. Subscription (client)

| Protocol | Clash (Mihomo) | sing-box outbound |
|----------|----------------|-------------------|
| tuic | `type: tuic`, uuid, password, congestion-controller, udp-relay-mode native, skip-cert-verify, alpn h3 | `type: tuic` + tls insecure + alpn h3 |
| anytls | `type: anytls`, password, client-fingerprint chrome, skip-cert-verify | `type: anytls` + tls + utls chrome |
| vmess | `type: vmess`, uuid, alterId, cipher auto, optional tls | `type: vmess`, security auto, alter_id, optional tls |

Self-signed nodes intentionally use insecure / skip-cert-verify (same as Trojan/Hy2 today).

## 6. Agent pin & build

| Item | Before | After |
|------|--------|--------|
| Submodule / replace | v1.11.15 | **v1.12.22** |
| Default tags | none | `with_quic,with_utls` |
| Optional full tags | documented | still optional: gvisor, wireguard, clash_api |
| `SingboxVersion()` fallback | 1.11.15 | 1.12.22 |
| ldflags Version | CI optional | set in Makefile/CI to 1.12.22 when building |

**Breaking risk:** API surface used by `BoxRuntime` (`box.Context`, registries, `option.Options`) must compile against 1.12.22; fix glue only in `agent/internal`, never patch submodule sources unless unavoidable.

## 7. API validation

On create/update inbound:

- If `templates.Get(protocol)` is false → **400** with clear error.
- Then `inboundfill.Fill` as today.

## 8. Testing

| Layer | Coverage |
|-------|----------|
| templates | List length 7; Get each new protocol |
| inboundfill | TUIC/AnyTLS/VMess secret generation |
| converter | Golden JSON for three protocols; invalid UUID / missing TLS |
| subscription | Clash + sing-box contain new types |
| agent | Existing box tests with tags; parse/apply smoke still passes |
| CI | Build agent with default tags |

## 9. Success criteria

1. Operator can create TUIC / AnyTLS / VMess from Web (dynamic form).
2. Apply produces JSON that a **with_quic** Agent on **sing-box 1.12.22** accepts for TUIC and AnyTLS, and default+tags accepts VMess.
3. Subscription links include the three proxy types.
4. Unit tests green under CI (`GOWORK` with submodule present).
5. Docs state pin, tags, and protocol list accurately.

## 10. Risks

| Risk | Mitigation |
|------|------------|
| 1.11 → 1.12 agent compile break | Fix adapter; keep tests |
| Submodule not checked out locally | Document `git submodule update --init`; CI already recursive |
| Mihomo without anytls | Document Mihomo requirement for Clash anytls |
| Binary size with quic | Accept for correct default product behavior |
