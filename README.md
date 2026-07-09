# LabberAirport

Self-hosted **proxy fleet control plane**: a Go Panel with embedded React SPA and SQLite, plus a node Agent (sing-box 二开) controlled over gRPC (TLS + shared bearer token).

## Architecture

```
Browser --HTTP--> Panel (Go + embed SPA + SQLite)
                     |
                     | gRPC (optional TLS + Bearer token)
                     +-----> Agent-1 (labber-agent + sing-box)
                     +-----> Agent-2
                     '-----> Agent-N
```

| Path | Role |
|------|------|
| `panel/` | Control plane: HTTP API, auth, store, converter, batch apply, node gRPC client |
| `agent/` | Node agent (`labber-agent`): AgentControl gRPC server + box lifecycle |
| `agent/sing-box/` | Upstream sing-box **git submodule** (pinned); in-process box runtime |
| `web/` | React + TypeScript + Vite SPA (built into `panel/web/dist` for `go:embed`) |
| `pkg/` | Shared Go libs (`auth` token interceptors, `hashutil`) |
| `proto/` | gRPC/protobuf (`AgentControl`) and generated Go |
| `scripts/` | Lab helpers (`gen-dev-certs.sh`, `e2e-smoke.sh`) |
| `deploy/dev/` | Optional self-signed TLS material for lab |

Panel owns protocol templates → form params → full sing-box JSON conversion. Agents only receive JSON via `ApplyConfig` and never interpret Panel form schemas.

Design / plan docs:

- [Design spec](docs/superpowers/specs/2026-07-09-labber-airport-design.md)
- [Implementation plan](docs/superpowers/plans/2026-07-09-labber-airport.md)

## Modules

Go workspace (`go.work`) covers:

- `github.com/labberairport/pkg`
- `github.com/labberairport/panel`
- `github.com/labberairport/agent`
- `github.com/labberairport/proto` (generated client/server stubs)

## Build

```bash
make web      # build frontend into panel/web/dist
make panel   # build panel binary to bin/panel (depends on web)
make agent   # build agent binary to bin/labber-agent
make proto   # generate Go from proto/agent/v1
make test    # run Go tests in pkg, panel, agent
```

Requires Go 1.22+, Node/npm for `make web` / `make panel`, and `protoc` + plugins for `make proto`.

### Submodule (`agent/sing-box`)

The agent box runtime depends on the [sing-box](https://github.com/SagerNet/sing-box) submodule at `agent/sing-box`.

```bash
git submodule update --init --recursive
```

See [agent/README.md](agent/README.md) for pin policy and upgrade notes. Glue code stays under `agent/cmd` and `agent/internal`; avoid editing upstream tree unless deliberately forking.

## Run (lab)

### Agent

Always runs in-process **sing-box** (no mock core).

```bash
./bin/labber-agent -listen 127.0.0.1:50051 -token test -data-dir /tmp/labber-agent
```

Optional TLS (after generating lab certs):

```bash
./scripts/gen-dev-certs.sh
./bin/labber-agent -listen 127.0.0.1:50051 -token test -data-dir /tmp/labber-agent \
  -tls-cert deploy/dev/agent.crt -tls-key deploy/dev/agent.key
```

When the agent uses TLS, the Panel node should carry the CA PEM (`ca_cert_pem` from `deploy/dev/ca.crt`). Lab-only shortcut: `tls_skip_verify=true` (never in production).

### Panel

```bash
./bin/panel -listen 127.0.0.1:8080 -db ./data/panel.db
```

Flags:

| Flag | Default | Notes |
|------|---------|--------|
| `-listen` | settings / `:8080` | HTTP listen address |
| `-db` | `./data/panel.db` | SQLite path |
| `-session-secret` | random ephemeral | Set a stable secret so sessions survive restart |
| `-bootstrap` | `true` | On start, apply configs and start sing-box on all registered nodes |
| `-bootstrap-timeout` | `3m` | Max time for initial startup bootstrap |
| `-bootstrap-retry` | `true` | Periodically retry apply+start for nodes not yet online/running |
| `-bootstrap-retry-interval` | `30s` | Interval between retry rounds |

Open `http://127.0.0.1:8080` in a browser.

### Default admin password

On first start with an empty admin hash, Panel sets the password to **`admin`** and logs a warning. Change it under Settings immediately.

### Automated smoke

```bash
./scripts/e2e-smoke.sh
```

Builds missing binaries if needed, starts agent (sing-box) + panel (temp DB), logs in, creates a node + Shadowsocks inbound, attaches, applies, and asserts task `success`.

### Dev TLS certs

```bash
./scripts/gen-dev-certs.sh
```

Writes `deploy/dev/{ca,agent}.{crt,key}` via `openssl` (self-signed CA + leaf). Lab only.

## Security notes

- **Change defaults:** admin password `admin`, agent tokens (`-token` / node token / default agent token), and `-session-secret`.
- **Tokens:** shared bearer on every gRPC call; wrong token must fail probe/apply.
- **TLS:** prefer real certs + CA verification on Panel (`ca_cert_pem`). Do **not** use `tls_skip_verify` outside local lab.
- **Network:** Agents must be reachable from the Panel (not from browsers). Bind agent listen addresses carefully.
- **Lab certs** under `deploy/dev/` are self-signed throwaways — regenerate or replace for any non-dev use.

## License / upstream

Agent runtime builds on sing-box (see `agent/sing-box` for upstream license). LabberAirport control-plane code is separate monorepo content around that submodule.
