# TUIC / AnyTLS / VMess Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish end-to-end inbound support for TUIC, AnyTLS, and VMess (Panel templates/fill/convert/subscribe + Agent pin 1.12.22 with default QUIC/uTLS tags + docs).

**Architecture:** Same as parent design: Panel converts protocol params to full sing-box JSON; Agent runs in-process sing-box. Expand built-in templates and bump runtime so AnyTLS/TUIC actually start.

**Tech Stack:** Go panel modules, sing-box submodule v1.12.22, Makefile/CI build tags `with_quic,with_utls`.

**Spec:** `docs/superpowers/specs/2026-07-10-inbound-protocols-tuic-anytls-vmess-design.md`

---

## File map

| Path | Responsibility |
|------|----------------|
| `panel/internal/templates/templates.go` | Built-in form schemas (already partly done) |
| `panel/internal/inboundfill/fill.go` | Secret generation |
| `panel/internal/converter/converter.go` + `testdata/*` | sing-box inbound JSON |
| `panel/internal/subscription/render.go` | Clash + sing-box client |
| `panel/internal/api/handlers_inbounds.go` | Validate protocol against templates |
| `agent/sing-box` submodule | Pin v1.12.22 |
| `agent/go.mod` / `go.sum` | Module pin + replace |
| `agent/internal/control/runtime_box.go` | Version string fallback; fix API if needed |
| `Makefile`, `.github/workflows/*` | Default agent build tags + ldflags |
| `README.md`, `agent/README.md`, parent design | Operator docs |

---

### Task 1: Panel protocol surface (templates, fill, converter, subscription)

**Files:**
- Modify: `panel/internal/templates/templates.go`
- Modify: `panel/internal/inboundfill/fill.go` (+ tests)
- Modify: `panel/internal/converter/converter.go` (+ tests + golden)
- Modify: `panel/internal/subscription/render.go` (+ tests)
- Modify: `panel/internal/store/models.go` comment

- [x] **Step 1:** Ensure seven templates include `tuic`, `anytls`, `vmess` with fields per design.
- [x] **Step 2:** Ensure fill/converter/subscription cases + goldens/tests exist (prior session).
- [x] **Step 3:** Re-run panel unit tests.

```bash
cd panel && GOWORK=off go test ./internal/templates/ ./internal/inboundfill/ ./internal/converter/ ./internal/subscription/ -count=1
```

Expected: all `ok`.

---

### Task 2: API rejects unknown protocols

**Files:**
- Modify: `panel/internal/api/handlers_inbounds.go`
- Modify: `panel/internal/api/server_test.go` (optional assert)

- [x] **Step 1:** In `handleCreateInbound` (and update when protocol changes), if `!templates.Get(protocol)` return 400 `"unknown protocol"`.
- [x] **Step 2:** Run API tests: `cd panel && GOWORK=off go test ./internal/api/ -count=1`

---

### Task 3: Pin sing-box to v1.12.22

**Files:**
- Submodule: `agent/sing-box` → tag `v1.12.22`
- Modify: `agent/go.mod`, `agent/go.sum`
- Modify: `agent/internal/control/runtime_box.go` (`SingboxVersion` fallback)
- Modify: `agent/README.md`

- [x] **Step 1:** Init/checkout submodule at `v1.12.22`.
- [x] **Step 2:** Update `agent/go.mod` require/comment to `v1.12.22`; run `cd agent && go mod tidy` with submodule present.
- [x] **Step 3:** Fix any compile breaks in `agent/internal/**` only (`include.Context`, traffic tracker counters).
- [x] **Step 4:** Set `SingboxVersion` fallback to `"1.12.22"`.

---

### Task 4: Default build tags with_quic + with_utls

**Files:**
- Modify: `Makefile` (`agent` target)
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/release.yml`
- Modify: `agent/README.md`

Define:

```
AGENT_TAGS=with_quic,with_utls
AGENT_LDFLAGS=-X 'github.com/sagernet/sing-box/constant.Version=1.12.22'
```

- [x] **Step 1:** Makefile `agent` uses tags + ldflags.
- [x] **Step 2:** CI/release agent build steps use the same tags + ldflags.
- [x] **Step 3:** Document that full optional tags remain available.

---

### Task 5: Docs + parent design table

**Files:**
- Modify: `README.md` (protocol list — if not already)
- Modify: `docs/superpowers/specs/2026-07-09-ladder-airport-design.md` protocol rows
- Modify: `agent/README.md` pin + tags

- [x] **Step 1:** Update parent design §5.1 and §5.3 for seven protocols.
- [x] **Step 2:** Agent README pin v1.12.22; remove “AnyTLS needs future bump” warning.

---

### Task 6: Full verification

- [x] **Step 1:** Panel tests (all packages) — pass.
- [x] **Step 2:** Agent tests with tags — pass (incl. Apply smoke for TUIC/AnyTLS/VMess).
- [x] **Step 3:** Agent build: `make agent` — pass.
- [x] **Step 4:** Ready for commit / PR (user decision).

---

## Self-review vs spec

| Spec requirement | Task |
|------------------|------|
| Templates + fill + convert + subscribe | Task 1 |
| API unknown protocol reject | Task 2 |
| sing-box ≥ 1.12 pin | Task 3 |
| Default with_quic/with_utls | Task 4 |
| Docs | Task 5 |
| Success criteria tests | Task 6 |
