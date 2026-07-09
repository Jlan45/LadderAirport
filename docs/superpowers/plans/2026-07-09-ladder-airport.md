# LadderAirport Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a self-hosted proxy fleet control plane: Go Panel with embedded React SPA and SQLite, plus a sing-box source fork Agent controlled over gRPC (TLS + shared token), with inbound protocol templates converted to full sing-box JSON for batch apply.

**Architecture:** Panel is the single source of truth (templates, converter, node inventory, batch tasks). Agents are dialed by Panel; each Agent is a 二开 sing-box process embedding an `AgentControl` gRPC server and an in-process box runtime. Web is React/Vite built into `panel/web/dist` and served via `go:embed`.

**Tech Stack:** Go 1.22+, gRPC + protobuf, modernc.org/sqlite (or mattn/go-sqlite3), chi or stdlib `net/http` mux, React 18 + TypeScript + Vite, sing-box upstream (submodule, pinned tag), TLS + Bearer token.

**Spec:** `docs/superpowers/specs/2026-07-09-ladder-airport-design.md`

**Prerequisites (environment):** Install Go 1.22+, `protoc`, `protoc-gen-go`, `protoc-gen-go-grpc`, Node 20+ (present), git. On Debian/Kali-like hosts:

```bash
# Example — adjust as needed
sudo apt-get update && sudo apt-get install -y golang-go protobuf-compiler
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
export PATH="$(go env GOPATH)/bin:$PATH"
```

---

## File structure (target)

```
LadderAirport/
├── go.work
├── Makefile
├── README.md
├── proto/
│   ├── agent/v1/agent.proto
│   └── gen/go/agent/v1/          # generated
├── pkg/
│   ├── auth/token.go
│   ├── auth/token_test.go
│   └── hashutil/hash.go
├── panel/
│   ├── go.mod
│   ├── cmd/panel/main.go
│   ├── internal/
│   │   ├── config/config.go
│   │   ├── store/store.go
│   │   ├── store/store_test.go
│   │   ├── store/models.go
│   │   ├── templates/templates.go
│   │   ├── templates/templates_test.go
│   │   ├── converter/converter.go
│   │   ├── converter/converter_test.go
│   │   ├── converter/testdata/   # golden JSON
│   │   ├── nodeclient/client.go
│   │   ├── nodeclient/client_test.go
│   │   ├── batch/runner.go
│   │   ├── batch/runner_test.go
│   │   ├── api/server.go
│   │   ├── api/auth.go
│   │   ├── api/handlers_*.go
│   │   ├── api/server_test.go
│   │   └── embed/web.go
│   └── web/dist/                 # build output (placeholder index until web builds)
├── web/
│   ├── package.json
│   ├── vite.config.ts
│   ├── index.html
│   └── src/
│       ├── main.tsx
│       ├── api/client.ts
│       ├── App.tsx
│       ├── pages/Nodes.tsx
│       ├── pages/Inbounds.tsx
│       ├── pages/NodeDetail.tsx
│       ├── pages/Batch.tsx
│       ├── pages/Settings.tsx
│       ├── pages/Login.tsx
│       └── components/DynamicForm.tsx
├── agent/
│   ├── go.mod
│   ├── cmd/ladder-agent/main.go
│   ├── internal/control/
│   │   ├── server.go
│   │   ├── server_test.go
│   │   ├── auth.go
│   │   ├── runtime.go            # interface
│   │   ├── runtime_mock.go
│   │   ├── runtime_box.go        # sing-box 二开 adapter
│   │   ├── metrics.go
│   │   └── logbuf.go
│   └── sing-box/                 # git submodule: SagerNet/sing-box @ pinned tag
└── docs/superpowers/...
```

**Module layout:**

- `go.work` members: `./panel`, `./agent`, `./pkg` (if separate), and use `replace` for generated proto package under `proto/gen/go` or embed gen into `pkg/proto`.
- Practical choice: rootless multi-module:
  - `github.com/ladderairport/panel`
  - `github.com/ladderairport/agent`
  - `github.com/ladderairport/pkg`
  - Generated code: `github.com/ladderairport/proto/gen/go`

---

### Task 1: Repository scaffold and go.work

**Files:**
- Create: `go.work`, `Makefile`, `README.md`, `pkg/go.mod`, `panel/go.mod`, `agent/go.mod`, `.gitignore`

- [ ] **Step 1: Create directories and .gitignore**

```bash
mkdir -p proto/agent/v1 proto/gen/go pkg/auth pkg/hashutil \
  panel/cmd/panel panel/internal/{config,store,templates,converter,nodeclient,batch,api,embed} panel/web/dist \
  web/src agent/cmd/ladder-agent agent/internal/control \
  docs/superpowers/plans
```

`.gitignore`:

```
bin/
dist/
node_modules/
panel/web/dist/assets/
*.db
*.db-journal
.idea/
.vscode/
*.pem
*.key
.env
web/dist/
```

Keep `panel/web/dist/.gitkeep` so embed path exists before first frontend build.

- [ ] **Step 2: Initialize Go modules and go.work**

```bash
cd pkg && go mod init github.com/ladderairport/pkg && cd ..
cd panel && go mod init github.com/ladderairport/panel && cd ..
cd agent && go mod init github.com/ladderairport/agent && cd ..
go work init ./pkg ./panel ./agent
```

- [ ] **Step 3: Minimal Makefile**

```makefile
.PHONY: proto panel agent web test

proto:
	protoc -I proto \
	  --go_out=proto/gen/go --go_opt=paths=source_relative \
	  --go-grpc_out=proto/gen/go --go-grpc_opt=paths=source_relative \
	  proto/agent/v1/agent.proto

web:
	cd web && npm ci && npm run build
	rm -rf panel/web/dist/*
	cp -r web/dist/* panel/web/dist/

panel: web
	cd panel && go build -o ../bin/panel ./cmd/panel

agent:
	cd agent && go build -o ../bin/ladder-agent ./cmd/ladder-agent

test:
	cd pkg && go test ./...
	cd panel && go test ./...
	cd agent && go test ./...
```

- [ ] **Step 4: Commit**

```bash
git add go.work pkg panel agent Makefile .gitignore README.md
git commit -m "chore: scaffold monorepo modules and Makefile"
```

---

### Task 2: gRPC proto and code generation

**Files:**
- Create: `proto/agent/v1/agent.proto`
- Create: `proto/gen/go/agent/v1/*` (generated)
- Create: `proto/go.mod` (module `github.com/ladderairport/proto`)

- [ ] **Step 1: Write `proto/agent/v1/agent.proto`**

```protobuf
syntax = "proto3";

package agent.v1;

option go_package = "github.com/ladderairport/proto/gen/go/agent/v1;agentv1";

service AgentControl {
  rpc Ping(PingRequest) returns (PingResponse);
  rpc ApplyConfig(ApplyConfigRequest) returns (ApplyConfigResponse);
  rpc Start(StartRequest) returns (StartResponse);
  rpc Stop(StopRequest) returns (StopResponse);
  rpc GetStatus(GetStatusRequest) returns (GetStatusResponse);
  rpc GetMetrics(GetMetricsRequest) returns (GetMetricsResponse);
  rpc StreamLogs(StreamLogsRequest) returns (stream LogLine);
}

message PingRequest {}
message PingResponse {
  string agent_version = 1;
  string singbox_version = 2;
}

message ApplyConfigRequest {
  string config_json = 1;
  string config_hash = 2;
  bool replace = 3;
}
message ApplyConfigResponse {
  bool ok = 1;
  string message = 2;
  string applied_hash = 3;
}

message StartRequest {}
message StartResponse {
  bool ok = 1;
  string message = 2;
}
message StopRequest {}
message StopResponse {
  bool ok = 1;
  string message = 2;
}

message GetStatusRequest {}
message GetStatusResponse {
  string state = 1; // running | stopped | error
  string config_hash = 2;
  int64 started_at_unix = 3;
  string last_error = 4;
}

message GetMetricsRequest {}
message GetMetricsResponse {
  int64 connections = 1;
  int64 uplink_bytes = 2;
  int64 downlink_bytes = 3;
  double cpu_percent = 4;
  int64 memory_rss_bytes = 5;
}

message StreamLogsRequest {
  string level = 1; // optional filter: debug|info|warn|error
  int32 tail = 2;   // lines from ring buffer before live
}
message LogLine {
  int64 ts_unix_ms = 1;
  string level = 2;
  string message = 3;
}
```

- [ ] **Step 2: Init proto module and generate**

```bash
mkdir -p proto/gen/go
cd proto && go mod init github.com/ladderairport/proto && cd ..
# ensure plugins on PATH
make proto
```

Expected: files under `proto/gen/go/agent/v1/`.

- [ ] **Step 3: Add proto to go.work**

```bash
go work use ./proto
```

- [ ] **Step 4: Commit**

```bash
git add proto go.work
git commit -m "feat(proto): define AgentControl gRPC service"
```

---

### Task 3: Shared token auth helpers

**Files:**
- Create: `pkg/auth/token.go`
- Create: `pkg/auth/token_test.go`
- Create: `pkg/hashutil/hash.go`

- [ ] **Step 1: Write failing tests for token metadata**

`pkg/auth/token_test.go`:

```go
package auth_test

import (
	"context"
	"testing"

	"github.com/ladderairport/pkg/auth"
	"google.golang.org/grpc/metadata"
)

func TestAppendToken(t *testing.T) {
	ctx := auth.AppendBearerToken(context.Background(), "secret")
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatal("missing metadata")
	}
	vals := md.Get("authorization")
	if len(vals) != 1 || vals[0] != "Bearer secret" {
		t.Fatalf("got %v", vals)
	}
}

func TestValidateBearer(t *testing.T) {
	md := metadata.Pairs("authorization", "Bearer secret")
	ctx := metadata.NewIncomingContext(context.Background(), md)
	if err := auth.ValidateIncomingBearer(ctx, "secret"); err != nil {
		t.Fatal(err)
	}
	if err := auth.ValidateIncomingBearer(ctx, "wrong"); err == nil {
		t.Fatal("expected error")
	}
}
```

- [ ] **Step 2: Run tests — expect FAIL**

```bash
cd pkg && go get google.golang.org/grpc@latest && go test ./auth/ -v
```

Expected: FAIL (package/functions missing).

- [ ] **Step 3: Implement**

`pkg/auth/token.go`:

```go
package auth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const MDAuthorization = "authorization"

func AppendBearerToken(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, MDAuthorization, "Bearer "+token)
}

func ValidateIncomingBearer(ctx context.Context, expected string) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing metadata")
	}
	vals := md.Get(MDAuthorization)
	if len(vals) == 0 {
		return status.Error(codes.Unauthenticated, "missing authorization")
	}
	raw := vals[0]
	const prefix = "Bearer "
	if !strings.HasPrefix(raw, prefix) {
		return status.Error(codes.Unauthenticated, "invalid authorization scheme")
	}
	got := strings.TrimPrefix(raw, prefix)
	if subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
		return status.Error(codes.Unauthenticated, "invalid token")
	}
	return nil
}

func UnaryServerInterceptor(expectedToken string) func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := ValidateIncomingBearer(ctx, expectedToken); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}
```

Fix imports: add `"google.golang.org/grpc"` for `grpc.UnaryServerInfo` / `UnaryHandler`.

`pkg/hashutil/hash.go`:

```go
package hashutil

import (
	"crypto/sha256"
	"encoding/hex"
)

func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
```

- [ ] **Step 4: Run tests — expect PASS**

```bash
cd pkg && go test ./...
```

- [ ] **Step 5: Commit**

```bash
git add pkg
git commit -m "feat(pkg): bearer token helpers for gRPC metadata"
```

---

### Task 4: Agent Runtime interface + mock + gRPC server

**Files:**
- Create: `agent/internal/control/runtime.go`
- Create: `agent/internal/control/runtime_mock.go`
- Create: `agent/internal/control/logbuf.go`
- Create: `agent/internal/control/server.go`
- Create: `agent/internal/control/server_test.go`
- Create: `agent/cmd/ladder-agent/main.go`

- [ ] **Step 1: Define Runtime interface**

`agent/internal/control/runtime.go`:

```go
package control

import "context"

type State string

const (
	StateStopped State = "stopped"
	StateRunning State = "running"
	StateError   State = "error"
)

type Status struct {
	State         State
	ConfigHash    string
	StartedAtUnix int64
	LastError     string
}

type Metrics struct {
	Connections     int64
	UplinkBytes     int64
	DownlinkBytes   int64
	CPUPercent      float64
	MemoryRSSBytes  int64
}

type Runtime interface {
	Apply(ctx context.Context, configJSON string, hash string) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Status(ctx context.Context) Status
	Metrics(ctx context.Context) Metrics
}
```

- [ ] **Step 2: Write failing gRPC server tests**

`agent/internal/control/server_test.go` — start in-memory bufconn server with mock runtime, call Ping/ApplyConfig with and without token.

```go
package control_test

import (
	"context"
	"net"
	"testing"

	"github.com/ladderairport/agent/internal/control"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
	"github.com/ladderairport/pkg/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

func startTestServer(t *testing.T, token string, rt control.Runtime) (agentv1.AgentControlClient, func()) {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	s := grpc.NewServer(grpc.UnaryInterceptor(auth.UnaryServerInterceptor(token)))
	agentv1.RegisterAgentControlServer(s, control.NewServer(rt, "0.1.0-test", "sing-box-test"))
	go s.Serve(lis)
	conn, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	return agentv1.NewAgentControlClient(conn), func() { conn.Close(); s.Stop() }
}

func TestApplyConfigRequiresToken(t *testing.T) {
	rt := control.NewMockRuntime()
	client, cleanup := startTestServer(t, "secret", rt)
	defer cleanup()
	_, err := client.ApplyConfig(context.Background(), &agentv1.ApplyConfigRequest{
		ConfigJson: `{"inbounds":[],"outbounds":[{"type":"direct","tag":"direct"}]}`,
		ConfigHash: "abc",
		Replace:    true,
	})
	if err == nil {
		t.Fatal("expected unauthenticated")
	}
}

func TestApplyConfigOK(t *testing.T) {
	rt := control.NewMockRuntime()
	client, cleanup := startTestServer(t, "secret", rt)
	defer cleanup()
	ctx := auth.AppendBearerToken(context.Background(), "secret")
	resp, err := client.ApplyConfig(ctx, &agentv1.ApplyConfigRequest{
		ConfigJson: `{"log":{"level":"info"},"inbounds":[],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`,
		ConfigHash: "abc",
		Replace:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Ok || resp.AppliedHash != "abc" {
		t.Fatalf("%+v", resp)
	}
	st := rt.Status(context.Background())
	if st.State != control.StateRunning || st.ConfigHash != "abc" {
		t.Fatalf("%+v", st)
	}
}
```

Note: stream interceptor also needed for `StreamLogs` — add `grpc.StreamInterceptor` wrapping the same token check (implement `auth.StreamServerInterceptor` in pkg if missing).

- [ ] **Step 3: Implement MockRuntime, log ring buffer, Server**

`runtime_mock.go`: thread-safe mock storing last JSON/hash, state transitions.

`logbuf.go`: fixed-size ring (e.g. 1000 lines), `Append`, `Tail(n)`, `Subscribe() (<-chan LogLine, cancel)`.

`server.go`: implements `agentv1.UnimplementedAgentControlServer`, delegates to Runtime; `StreamLogs` drains Tail then Subscribe until ctx done.

- [ ] **Step 4: Wire Stream interceptor in pkg/auth**

Add `StreamServerInterceptor` analogous to unary (same ValidateIncomingBearer).

- [ ] **Step 5: `cmd/ladder-agent/main.go` flags**

Flags: `-listen` (default `:50051`), `-token`, `-tls-cert`, `-tls-key`, `-data-dir` (cache last config). Start with `NewMockRuntime` until Task 5; print clear log line which runtime is active.

- [ ] **Step 6: Dependencies and tests**

```bash
cd agent
go get github.com/ladderairport/pkg@v0.0.0
go get github.com/ladderairport/proto@v0.0.0
# with go.work, local replace is automatic
go get google.golang.org/grpc@latest
go test ./internal/control/ -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add agent pkg
git commit -m "feat(agent): gRPC AgentControl server with mock runtime"
```

---

### Task 5: sing-box submodule and BoxRuntime (二开 adapter)

**Files:**
- Create: `agent/sing-box` submodule
- Create: `agent/internal/control/runtime_box.go`
- Create: `agent/cmd/ladder-agent/main.go` (switch runtime)
- Modify: `agent/go.mod` replace / require sing-box
- Create: `agent/README.md` (build notes)

- [ ] **Step 1: Pin sing-box submodule**

```bash
git submodule add https://github.com/SagerNet/sing-box.git agent/sing-box
cd agent/sing-box && git checkout v1.11.15  # pin a known stable tag; adjust if missing
cd ../..
```

Document the exact tag in `agent/README.md`.

- [ ] **Step 2: Implement `BoxRuntime`**

Design (keep failures from destroying old instance):

```go
type BoxRuntime struct {
	mu         sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
	instance   /* *box.Box or 二开 wrapper */
	configHash string
	startedAt  int64
	lastErr    string
	dataDir    string
}
```

`Apply` algorithm:

1. Parse JSON with sing-box option loader (`option.Options` / `json.Unmarshal` compatible with upstream).
2. Build new box instance **without** closing old yet.
3. `Start` new instance; on error, close new, set `lastErr`, return error (old keeps running).
4. On success, close old, swap pointer, update hash/startedAt, write `dataDir/current.json`.

Exact import paths depend on submodule module path (`github.com/sagernet/sing-box`). Prefer:

```go
import (
  "github.com/sagernet/sing-box"
  "github.com/sagernet/sing-box/option"
)
```

Use `box.New(box.Options{Context, Options})` pattern matching the checked-out tag’s public API. If API differs, adapt inside `runtime_box.go` only.

`Metrics`: v1 may return process RSS via `runtime.MemStats` and connection counts `0` if no stable hook; prefer reading experimental clash API stats if inbound experimental controller is easy. Document gap if counts are zero initially — still return process memory/CPU.

- [ ] **Step 3: go.mod replace**

```go
require github.com/sagernet/sing-box v1.11.15
replace github.com/sagernet/sing-box => ./sing-box
```

- [ ] **Step 4: Manual smoke (not unit-test heavy)**

```bash
cd agent && go build -o ../bin/ladder-agent ./cmd/ladder-agent
# generate self-signed cert for lab
openssl req -x509 -newkey rsa:2048 -keyout /tmp/agent.key -out /tmp/agent.crt -days 1 -nodes -subj /CN=localhost
./bin/ladder-agent -listen 127.0.0.1:50051 -token test -tls-cert /tmp/agent.crt -tls-key /tmp/agent.key
```

Use a tiny grpcurl or panel client later to ApplyConfig with a minimal direct-only config.

- [ ] **Step 5: Unit test BoxRuntime with invalid JSON**

```go
func TestBoxRuntimeInvalidJSONKeepsStopped(t *testing.T) {
	rt := control.NewBoxRuntime(t.TempDir())
	err := rt.Apply(context.Background(), `{not-json`, "h")
	if err == nil {
		t.Fatal("expected error")
	}
	if rt.Status(context.Background()).State == control.StateRunning {
		t.Fatal("should not be running")
	}
}
```

- [ ] **Step 6: Commit**

```bash
git add agent .gitmodules
git commit -m "feat(agent): BoxRuntime on vendored sing-box submodule"
```

---

### Task 6: Panel SQLite store

**Files:**
- Create: `panel/internal/store/models.go`
- Create: `panel/internal/store/store.go`
- Create: `panel/internal/store/store_test.go`

- [ ] **Step 1: Models**

```go
package store

type Node struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Address       string   `json:"address"`
	GRPCPort      int      `json:"grpc_port"`
	Token         string   `json:"token,omitempty"` // empty => use default
	Labels        []string `json:"labels"`
	TLSSkipVerify bool     `json:"tls_skip_verify"`
	CACertPEM     string   `json:"ca_cert_pem,omitempty"`
	Status        string   `json:"status"`
	LastSeenUnix  int64    `json:"last_seen_unix"`
	ConfigHash    string   `json:"config_hash"`
	CreatedAtUnix int64    `json:"created_at_unix"`
	UpdatedAtUnix int64    `json:"updated_at_unix"`
}

type InboundConfig struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Protocol      string         `json:"protocol"` // shadowsocks|trojan|vless|hysteria2
	Params        map[string]any `json:"params"`
	Enabled       bool           `json:"enabled"`
	CreatedAtUnix int64          `json:"created_at_unix"`
	UpdatedAtUnix int64          `json:"updated_at_unix"`
}

type Task struct {
	ID            string           `json:"id"`
	Type          string           `json:"type"` // apply|start|stop
	Status        string           `json:"status"` // pending|running|success|partial|failed
	NodeIDs       []string         `json:"node_ids"`
	Results       []TaskNodeResult `json:"results"`
	CreatedAtUnix int64            `json:"created_at_unix"`
	UpdatedAtUnix int64            `json:"updated_at_unix"`
}

type TaskNodeResult struct {
	NodeID  string `json:"node_id"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

type Settings struct {
	AdminPasswordHash string `json:"-"`
	DefaultAgentToken string `json:"default_agent_token"`
	GRPCTimeoutSec    int    `json:"grpc_timeout_sec"`
	MaxConcurrency    int    `json:"max_concurrency"`
	ListenAddr        string `json:"listen_addr"`
}
```

- [ ] **Step 2: Failing store tests**

Cover: CreateNode, ListNodes, CreateInbound, AttachInbound, ListInboundsForNode, CreateTask/UpdateTask, Get/SetSettings. Use `t.TempDir()/test.db`.

- [ ] **Step 3: Implement store with modernc.org/sqlite**

```bash
cd panel && go get modernc.org/sqlite
```

Schema migration in `Open(path)`: `CREATE TABLE IF NOT EXISTS` for `nodes`, `inbounds`, `node_inbounds`, `config_snapshots`, `tasks`, `settings`. Serialize labels/params/results as JSON text.

IDs: ULID or `uuid.NewString()`.

- [ ] **Step 4: Pass tests and commit**

```bash
cd panel && go test ./internal/store/ -v
git add panel
git commit -m "feat(panel): SQLite store for nodes, inbounds, tasks"
```

---

### Task 7: Protocol templates + converter

**Files:**
- Create: `panel/internal/templates/templates.go`
- Create: `panel/internal/templates/templates_test.go`
- Create: `panel/internal/converter/converter.go`
- Create: `panel/internal/converter/converter_test.go`
- Create: `panel/internal/converter/testdata/*.json`

- [ ] **Step 1: Template schema API types**

```go
package templates

type Field struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Type        string `json:"type"` // string|int|bool|select|password
	Required    bool   `json:"required"`
	Default     any    `json:"default,omitempty"`
	Options     []string `json:"options,omitempty"` // for select
	Description string `json:"description,omitempty"`
}

type Template struct {
	ID       string  `json:"id"`
	Protocol string  `json:"protocol"`
	Name     string  `json:"name"`
	Fields   []Field `json:"fields"`
}

func List() []Template { /* four built-ins */ }
func Get(protocol string) (Template, bool)
```

Define fields per design §5.3 (SS method select with common AEAD methods; VLESS `tls_mode`: `none|tls|reality`; Reality fields visible when mode=reality — frontend can show all fields and backend validates).

- [ ] **Step 2: Converter failing tests (golden)**

For each protocol, build `[]InboundConfig` with fixed params → `Convert(inbounds) ([]byte, error)` → compare to golden file (canonical JSON, sorted keys optional — use `encoding/json` marshal of `map` with stable construction).

Also test: port conflict returns error; empty list returns error; invalid UUID for VLESS returns error.

- [ ] **Step 3: Implement converter**

Output shape:

```json
{
  "log": {"level": "info"},
  "inbounds": [ /* mapped */ ],
  "outbounds": [{"type": "direct", "tag": "direct"}],
  "route": {"final": "direct"}
}
```

Mapping notes (align with sing-box config docs for pinned version):

| Protocol | sing-box inbound type |
|----------|----------------------|
| shadowsocks | `shadowsocks` |
| trojan | `trojan` |
| vless | `vless` + `tls` / `reality` sub-object |
| hysteria2 | `hysteria2` |

Assign unique `tag` per inbound: `in-<id-prefix>` or `in-<name-sanitized>`.

- [ ] **Step 4: Pass tests and commit**

```bash
cd panel && go test ./internal/templates/ ./internal/converter/ -v
git add panel
git commit -m "feat(panel): inbound templates and sing-box converter"
```

---

### Task 8: Panel node gRPC client + batch runner

**Files:**
- Create: `panel/internal/nodeclient/client.go`
- Create: `panel/internal/nodeclient/client_test.go`
- Create: `panel/internal/batch/runner.go`
- Create: `panel/internal/batch/runner_test.go`

- [ ] **Step 1: NodeClient**

```go
type DialConfig struct {
	Address       string // host:port
	Token         string
	Timeout       time.Duration
	TLSSkipVerify bool
	CACertPEM     []byte
}

type Client struct{ /* conn + agentv1.AgentControlClient */ }

func Dial(ctx context.Context, cfg DialConfig) (*Client, error)
func (c *Client) Close() error
func (c *Client) Ping / ApplyConfig / Start / Stop / GetStatus / GetMetrics / StreamLogs ...
```

TLS: `credentials.NewClientTLSFromCertificate` or `tls.Config{InsecureSkipVerify}` when skip set.

- [ ] **Step 2: Test against bufconn agent server from Task 4 pattern** (shared test helper or duplicate minimal server in nodeclient_test).

- [ ] **Step 3: Batch Runner**

```go
type Runner struct {
	Store         *store.Store
	DefaultToken  func() string
	Timeout       time.Duration
	MaxConcurrency int
	Dial          func(ctx context.Context, n store.Node, token string) (*nodeclient.Client, error)
}

func (r *Runner) RunTask(ctx context.Context, taskID string) error
```

For each node: resolve token → dial → switch on task type → record `TaskNodeResult` → update store. Concurrency via semaphore. Final status: all ok → success; all fail → failed; else partial.

- [ ] **Step 4: Unit test Runner with fake Dial returning mock clients / errors**

- [ ] **Step 5: Commit**

```bash
git add panel
git commit -m "feat(panel): gRPC node client and batch task runner"
```

---

### Task 9: Panel HTTP API + admin auth

**Files:**
- Create: `panel/internal/config/config.go`
- Create: `panel/internal/api/server.go`
- Create: `panel/internal/api/auth.go`
- Create: `panel/internal/api/handlers_nodes.go`
- Create: `panel/internal/api/handlers_inbounds.go`
- Create: `panel/internal/api/handlers_batch.go`
- Create: `panel/internal/api/handlers_settings.go`
- Create: `panel/internal/api/server_test.go`
- Create: `panel/cmd/panel/main.go`

- [ ] **Step 1: Auth**

- Password hash: `golang.org/x/crypto/bcrypt`
- Login: `POST /api/v1/auth/login` `{ "password": "..." }` → set HTTP-only cookie `session` with signed JWT (`github.com/golang-jwt/jwt/v5`) or HMAC token.
- Middleware: reject unauthenticated API calls with 401.
- Default password on empty DB: `admin` (log warning once); force change in settings.

- [ ] **Step 2: Register routes**

```
POST   /api/v1/auth/login
GET    /api/v1/templates
GET    /api/v1/inbounds
POST   /api/v1/inbounds
PUT    /api/v1/inbounds/{id}
DELETE /api/v1/inbounds/{id}
GET    /api/v1/nodes
POST   /api/v1/nodes
PUT    /api/v1/nodes/{id}
DELETE /api/v1/nodes/{id}
POST   /api/v1/nodes/{id}/probe
GET    /api/v1/nodes/{id}/inbounds
PUT    /api/v1/nodes/{id}/inbounds      // body: {"inbound_ids":[...]}
POST   /api/v1/nodes/{id}/apply
POST   /api/v1/nodes/{id}/config/preview
POST   /api/v1/nodes/{id}/start
POST   /api/v1/nodes/{id}/stop
GET    /api/v1/nodes/{id}/metrics
GET    /api/v1/nodes/{id}/logs          // SSE
POST   /api/v1/batch/apply
POST   /api/v1/batch/start
POST   /api/v1/batch/stop
GET    /api/v1/tasks
GET    /api/v1/tasks/{id}
GET    /api/v1/settings
PUT    /api/v1/settings
```

Batch body: `{ "node_ids": [], "labels": [] }` — union of ids and nodes matching any label.

Apply single node: convert → snapshot → dial ApplyConfig (or enqueue 1-node task for consistency — prefer same Runner path).

Logs SSE: dial StreamLogs, write `data: {"level","message","ts"}\n\n`.

- [ ] **Step 3: httptest tests**

Login → create node → create inbound → attach → preview config → batch apply against fake runner or skipped dial with injectable Runner.

- [ ] **Step 4: main.go**

Load flags: `-db`, `-listen`, `-session-secret`. Open store, ensure settings, start HTTP server.

- [ ] **Step 5: Commit**

```bash
git add panel
git commit -m "feat(panel): HTTP API with admin auth and fleet operations"
```

---

### Task 10: React SPA (dynamic forms + pages)

**Files:**
- Create: entire `web/` app as listed in file structure
- Modify: `web/vite.config.ts` proxy `/api` → `http://127.0.0.1:8080`

- [ ] **Step 1: Scaffold Vite React-TS**

```bash
cd web && npm create vite@latest . -- --template react-ts
npm install
npm install react-router-dom
```

- [ ] **Step 2: API client**

`web/src/api/client.ts`: `fetch` wrappers with `credentials: 'include'`; helpers for all endpoints.

- [ ] **Step 3: DynamicForm**

Props: `fields: Field[]`, `value`, `onChange`. Render input by `type`.

- [ ] **Step 4: Pages**

- **Login** — password form  
- **Nodes** — table, labels, probe, links to detail  
- **Inbounds** — list + create modal (protocol select → load template fields → submit)  
- **NodeDetail** — attach inbounds, preview JSON (`<pre>`), apply/start/stop, metrics, log viewer (EventSource on `/api/v1/nodes/{id}/logs`)  
- **Batch** — multi-select nodes / label filter, actions, poll task  
- **Settings** — default token, concurrency, timeout, change password  

- [ ] **Step 5: Build into panel embed path**

```bash
# vite.config.ts build.outDir = '../panel/web/dist' emptyOutDir true
cd web && npm run build
```

- [ ] **Step 6: Commit**

```bash
git add web panel/web/dist
git commit -m "feat(web): React ops console with template forms"
```

---

### Task 11: Embed SPA in Panel binary

**Files:**
- Create: `panel/internal/embed/web.go`
- Modify: `panel/internal/api/server.go` (static + SPA fallback)
- Create: `panel/web/dist/index.html` (from build)

- [ ] **Step 1: embed**

```go
package embedweb

import "embed"

//go:embed all:../web/dist
var Dist embed.FS
```

Path: use `//go:embed all:web/dist` from `panel` package root file `panel/embed.go` if relative embed is cleaner:

`panel/embed.go`:

```go
package panel

import "embed"

//go:embed all:web/dist
var WebDist embed.FS
```

Actually embed must be in package owning the path — put `panel/cmd` or `panel/internal/embed` with correct path. Recommended:

```go
// panel/internal/embed/web.go
package embedweb

import "embed"

//go:embed all:../../web/dist
var Dist embed.FS
```

Go embed cannot use `..` — **must** place file at `panel/web_embed.go`:

```go
package main // NO — use package webdist in panel/webdist/dist.go

// file: panel/webdist/dist.go
package webdist

import "embed"

//go:embed all:dist
var Dist embed.FS
```

With assets in `panel/webdist/dist/*` — **Makefile already copies to `panel/web/dist`**. Use:

```go
// file: panel/webui/embed.go
package webui
import "embed"
//go:embed all:dist
var Dist embed.FS
// assets live in panel/webui/dist
```

Update Makefile `cp` target to `panel/webui/dist`. Align with design path `panel/web/dist` by putting embed go file at `panel/web/embed.go` package `web` and assets in `panel/web/dist` — **embed cannot nest from parent**. Correct pattern:

```
panel/
  web/
    embed.go   // package webui; //go:embed all:dist
    dist/
      index.html
      assets/...
```

- [ ] **Step 2: File server + SPA fallback**

For request not starting with `/api/`, serve file from embed FS; if not found, serve `index.html`.

- [ ] **Step 3: Build single binary**

```bash
make web && cd panel && go build -o ../bin/panel ./cmd/panel
./bin/panel -listen :8080 -db ./data/panel.db
```

Open browser → login → UI loads.

- [ ] **Step 4: Commit**

```bash
git add panel Makefile
git commit -m "feat(panel): embed React SPA in panel binary"
```

---

### Task 12: TLS lab defaults, e2e script, README

**Files:**
- Create: `scripts/gen-dev-certs.sh`
- Create: `scripts/e2e-smoke.sh`
- Modify: `README.md`

- [ ] **Step 1: Cert script** — generate `deploy/dev/agent.crt/key` and optional CA.

- [ ] **Step 2: e2e-smoke.sh**

1. Start agent with token `test` on `127.0.0.1:50051`  
2. Start panel  
3. `curl` login, create node `127.0.0.1:50051`, create SS inbound, attach, apply  
4. Assert task success / status running  

- [ ] **Step 3: README** — architecture diagram (text), build instructions, default passwords, security notes (change tokens, do not use skip-verify in prod).

- [ ] **Step 4: Commit**

```bash
git add scripts README.md
git commit -m "docs: README and e2e smoke scripts"
```

---

### Task 13: Hardening pass (acceptance checklist)

- [ ] **Step 1: Map acceptance criteria from spec §10 to manual checks; fix gaps**

| Criterion | Verification |
|-----------|----------------|
| Single binary Web | `make panel` → browser login |
| Four templates | UI + `GET /templates` |
| Apply to agent | e2e-smoke |
| Batch by labels | create 2 nodes same label, batch apply |
| Metrics/logs | node detail page |
| Wrong token fails | probe/apply with bad token |
| Failed apply keeps old | Apply good config, then Apply invalid JSON, status still running with old hash |

- [ ] **Step 2: Ensure `make test` green**

- [ ] **Step 3: Final commit if fixes**

```bash
git commit -m "fix: acceptance checklist hardening"
```

---

## Spec coverage self-review

| Spec item | Task(s) |
|-----------|---------|
| Panel Go + SQLite | 6, 9 |
| Embedded React | 10, 11 |
| Agent sing-box 二开 | 5 |
| gRPC Panel→Agent | 2, 4, 8 |
| TLS + shared token | 3, 4, 5, 8 |
| Lifecycle start/stop/status | 4, 8, 9 |
| Config apply + snapshot | 7, 8, 9 |
| Metrics + logs stream | 4, 9, 10 |
| Batch by ids/labels | 8, 9, 10 |
| Inbound templates SS/Trojan/VLESS/Hy2 | 7, 10 |
| Converter → full JSON | 7 |
| Admin auth separate from agent token | 9 |
| Non-goals (no multi-tenant, no outbound UX) | honored — not scheduled |

## Placeholder / consistency scan

- Runtime interface names: `Apply`, `Start`, `Stop`, `Status`, `Metrics` — used consistently.
- Proto package: `agent.v1` / `agentv1`.
- Embed path locked to `panel/web/` with `embed.go` + `dist/` (Makefile must match Task 11).
- sing-box tag `v1.11.15` is a planning pin; if tag missing at implement time, choose latest stable 1.11.x and update README + go.mod in the same commit.

## Execution handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-09-ladder-airport.md`.

**Two execution options:**

1. **Subagent-Driven (recommended)** — fresh subagent per task, review between tasks  
2. **Inline Execution** — this session executes tasks with executing-plans checkpoints  

Which approach?
