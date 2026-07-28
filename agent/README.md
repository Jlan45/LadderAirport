# LadderAirport Agent

In-process **sing-box + optional FRPS** agent: gRPC `AgentControl` with independent `BoxRuntime` and `FRPServerRuntime` lifecycle adapters.

## Upstream pin

| Item | Value |
|------|--------|
| Upstream | [SagerNet/sing-box](https://github.com/SagerNet/sing-box) |
| Pinned tag | **v1.12.22** |
| Submodule path | `agent/sing-box` |
| Go module | `github.com/sagernet/sing-box` |

FRPS follows the same source pinning model:

| Item | Value |
|---|---|
| Upstream | [fatedier/frp](https://github.com/fatedier/frp) |
| Pinned tag | `v0.69.0` |
| Submodule path | `agent/frp` |
| Go module | `github.com/fatedier/frp` |
| Default build tags | `with_quic,with_utls` |

`agent/go.mod` uses:

```go
require github.com/sagernet/sing-box v1.12.22
replace github.com/sagernet/sing-box => ./sing-box
```

**Prefer submodule + `replace`.** A plain `go get` module dependency is acceptable only when the submodule clone is too heavy for the environment; the submodule pointer is the source of truth for 二开.

### Submodule checkout

```bash
git submodule update --init --recursive
cd agent/sing-box && git checkout v1.12.22
```

If `git submodule add` fails (network), shallow clone then wire replace:

```bash
git clone --depth 1 --branch v1.12.22 https://github.com/SagerNet/sing-box.git agent/sing-box
```

## Runtime

Agent always uses **in-process sing-box** (`control.BoxRuntime`). FRPS is also linked into the Agent and remains stopped until Panel applies an enabled FRPS configuration.

The runtimes are independent: applying or stopping FRPS does not restart sing-box. The latest FRPS configuration is stored as `frps/frps-current.json` under the Agent data directory with mode `0600`, restored at Agent startup, and controlled through the `frps-v1` gRPC capability.

Log line on start: `runtime=box agent_version=... singbox_version=...`.

## Build

From repo root (recommended — applies default tags + version ldflags):

```bash
make agent   # → bin/ladder-agent
```

Or:

```bash
cd agent
go build -tags "with_quic,with_utls" \
  -ldflags "-X 'github.com/sagernet/sing-box/constant.Version=1.12.22'" \
  -o ../bin/ladder-agent ./cmd/ladder-agent
```

Optional full feature tags (match upstream Makefile extras):

```bash
cd agent
go build -tags "with_gvisor,with_quic,with_wireguard,with_utls,with_clash_api" \
  -ldflags "-X 'github.com/sagernet/sing-box/constant.Version=1.12.22'" \
  -o ../bin/ladder-agent ./cmd/ladder-agent
```

### Protocol build notes

| Protocols | Tags / pin |
|-----------|------------|
| Shadowsocks, Trojan, VLESS, VMess | default registries |
| TUIC, Hysteria2 | **`with_quic`** (default in `make agent` / CI) |
| AnyTLS | sing-box **≥ 1.12** (pin v1.12.22) |
| Reality client fingerprint paths | **`with_utls`** (default) |

## Tests

```bash
make test
# or
cd agent && go test -tags "with_quic,with_utls" ./... -timeout 120s
# faster parse-only smoke (skips real box Start):
cd agent && go test -tags "with_quic,with_utls" ./internal/control/ -short
```

## BoxRuntime behaviour

**Single-instance lifecycle** (never two sing-box cores listening at once):

`Apply` algorithm (stop-then-start; brief disconnect OK):

1. If already **running** with the **same** `config_json` + `config_hash` → **no-op**.
2. Parse JSON into `option.Options` (fail here keeps the old instance if any).
3. **Stop/close** the current instance completely (ports released).
4. Create and `Start` the new box.
5. On start failure: best-effort **restore** previous config; set `LastError`.
6. On success: update hash / `startedAt`, write `dataDir/current.json` when set.

All Apply/Start/Stop share one mutex — concurrent Panel RPCs queue, never dual-start.

`Start` is a no-op if already running; otherwise re-applies the last good config. `Stop` closes the instance (config kept for later Start).

### Metrics

| Field | Source |
|-------|--------|
| `Connections` | Active routed connections via in-process `ConnectionTracker` |
| `UplinkBytes` / `DownlinkBytes` | Cumulative bytes (client→node / node→client); survives hot-reload |
| `MemoryRSSBytes` | `runtime.MemStats.Sys` (approx process memory, not exact RSS) |
| `CPUPercent` | Linux: `/proc/self/stat` sample between polls; first sample is 0 |

## Upgrade policy

1. Bump submodule to a new **stable** tag (`v1.12.x` or `v1.13.x`).
2. Update `agent/go.mod` require version comment, `SingboxVersion` fallback, Makefile/`AGENT_LDFLAGS`, CI env.
3. Fix glue under `agent/internal` only if APIs break (do not edit upstream tree under `agent/sing-box/` unless deliberately forking).
4. Run `make test` and a short Apply/Start smoke.

Do not edit upstream files under `agent/sing-box/` unless deliberately forking; keep 二开 glue in `agent/internal` and `agent/cmd`.
