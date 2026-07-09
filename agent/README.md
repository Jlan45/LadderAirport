# LabberAirport Agent

In-process **sing-box 二开** agent: gRPC `AgentControl` + `BoxRuntime` lifecycle adapter.

## Upstream pin

| Item | Value |
|------|--------|
| Upstream | [SagerNet/sing-box](https://github.com/SagerNet/sing-box) |
| Pinned tag | **v1.11.15** |
| Submodule path | `agent/sing-box` |
| Go module | `github.com/sagernet/sing-box` |

`agent/go.mod` uses:

```go
require github.com/sagernet/sing-box v1.11.15
replace github.com/sagernet/sing-box => ./sing-box
```

**Prefer submodule + `replace`.** A plain `go get` module dependency is acceptable only when the submodule clone is too heavy for the environment; the submodule pointer is the source of truth for 二开.

### Submodule checkout

```bash
git submodule update --init --recursive
cd agent/sing-box && git checkout v1.11.15
```

If `git submodule add` fails (network), shallow clone then wire replace:

```bash
git clone --depth 1 --branch v1.11.15 https://github.com/SagerNet/sing-box.git agent/sing-box
```

## Runtime

Agent always uses **in-process sing-box** (`control.BoxRuntime`). There is no mock core or `-runtime` flag.

Log line on start: `runtime=box agent_version=... singbox_version=...`.

## Build

```bash
# from repo root
make agent
# or
cd agent && go build -o ../bin/labber-agent ./cmd/labber-agent
```

Optional full feature tags (match upstream Makefile; not required for lab):

```bash
cd agent
go build -tags "with_gvisor,with_quic,with_wireguard,with_utls,with_clash_api" \
  -ldflags "-X 'github.com/sagernet/sing-box/constant.Version=1.11.15'" \
  -o ../bin/labber-agent ./cmd/labber-agent
```

Default builds use stub registries for QUIC/WireGuard/Clash API (`!with_*` tags). Protocol support for shadowsocks/vmess/trojan/vless/socks/http/mixed/direct works without extra tags.

### Tests

```bash
cd agent && go test ./...
# faster parse-only smoke (skips real box Start):
cd agent && go test ./internal/control/ -short
```

## BoxRuntime behaviour

`Apply` algorithm (keep-old-on-failure):

1. Parse JSON into `option.Options` with inbound/outbound/endpoint registries.
2. Create a **new** box instance (old still running).
3. `Start` new; on error close new, set `LastError`, return error (**old kept**).
4. On success close old, swap, update hash / `startedAt`, write `dataDir/current.json` when set.

`Start` re-applies the last good config if stopped. `Stop` closes the instance.

### Metrics

| Field | Source |
|-------|--------|
| `Connections` | Active routed connections via in-process `ConnectionTracker` |
| `UplinkBytes` / `DownlinkBytes` | Cumulative bytes (client→node / node→client); survives hot-reload |
| `MemoryRSSBytes` | `runtime.MemStats.Sys` (approx process memory, not exact RSS) |
| `CPUPercent` | Linux: `/proc/self/stat` sample between polls; first sample is 0 |

## Upgrade policy

1. Bump submodule to a new **stable** tag (`v1.11.x` or `v1.12.x` LTS-ish).
2. Rebuild agent; fix compile breaks only inside `internal/control/runtime_box.go` (and README pin table).
3. Run `go test ./...` and a manual Apply of a minimal direct config.
4. Update this README pin + `go.mod` require comment in the **same commit**.
5. Prefer minor bumps within the same major line; re-read `box.New` / `box.Context` signatures on major jumps (registries grew in later lines).

Do not edit upstream files under `agent/sing-box/` unless deliberately forking; keep 二开 glue in `agent/internal` and `agent/cmd`.

## Run (lab)

```bash
./bin/labber-agent -listen 127.0.0.1:50051 -token test -data-dir /tmp/labber-agent
```

TLS (optional):

```bash
openssl req -x509 -newkey rsa:2048 -keyout /tmp/agent.key -out /tmp/agent.crt -days 1 -nodes -subj /CN=localhost
./bin/labber-agent -listen 127.0.0.1:50051 -token test \
  -tls-cert /tmp/agent.crt -tls-key /tmp/agent.key -data-dir /tmp/labber-agent
```
