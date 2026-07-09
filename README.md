# LabberAirport

Self-hosted proxy fleet control plane. Monorepo layout:

| Path | Description |
|------|-------------|
| `panel/` | Control-plane server (API, store, agent coordination) |
| `agent/` | Node agent (`labber-agent`) that runs on fleet hosts |
| `web/` | Frontend UI (built into `panel/web/dist` for embedding) |
| `pkg/` | Shared Go libraries (`auth`, `hashutil`, …) |
| `proto/` | gRPC/protobuf definitions and generated code |

## Modules

Go workspace (`go.work`) covers:

- `github.com/labberairport/pkg`
- `github.com/labberairport/panel`
- `github.com/labberairport/agent`

## Build

```bash
make web      # build frontend into panel/web/dist
make panel   # build panel binary to bin/panel
make agent   # build agent binary to bin/labber-agent
make proto   # generate Go from proto/agent/v1
make test    # run Go tests in pkg, panel, agent
```
