# Fleet UI Consolidation Design

**Date:** 2026-07-10  
**Status:** Approved for implementation (user directed autonomous execution)

## Problem

The Panel frontend duplicated node-related surfaces:

| Page | Route | Overlap |
|------|-------|---------|
| Dashboard (总览) | `/` | Node list/cards, metrics, batch apply/start/stop |
| Nodes (节点) | `/nodes` | Node list, status, install command, probe/delete |
| NodeDetail | `/nodes/:id` | Metrics, start/stop, install command |
| Batch | `/batch` | Multi-select + apply/start/stop |

Users saw two similar “node lists” and repeated ops across pages.

## Goals

1. **Single primary surface** for node monitoring + lifecycle management.
2. **Clear hierarchy:** list-first; add via modal; deep config via side drawer.
3. **Remove standalone Batch page**; keep multi-select batch on the list toolbar.
4. **Compatible redirects** for old bookmarks (`/nodes`, `/nodes/:id`, `/batch`).
5. **Shared helpers** for status labels and byte formatting (no duplicated utilities).

## Non-goals

- Backend API changes.
- Redesign of Inbounds / Subscriptions / Settings.
- Real-time websockets (keep existing poll / fleet refresh).

## Information architecture

### Navigation

Before: `总览 | 节点 | 入站 | 订阅 | 批量操作 | 设置`  
After: `节点 | 入站 | 订阅 | 设置`

- Single entry labeled **节点**.
- Default authenticated home remains `/`.

### Routes

| Path | Behavior |
|------|----------|
| `/` | Render `FleetPage` |
| `/nodes` | Redirect → `/` |
| `/nodes/:id` | Redirect → `/?node=:id` (opens drawer) |
| `/batch` | Redirect → `/` |
| Other routes | Unchanged |

Drawer open state is driven by `?node=<id>` so refresh/share still works; closing the drawer clears the query.

### Page layout

```
StatsBar (total / online / offline / running)
Toolbar (refresh, view toggle, label filter, select-all, batch ops, add node)
Node list (cards | table) with multi-select and row actions
AddNodeModal
NodeDetailDrawer
Last-task summary (optional, after batch/single ops)
```

## Component boundaries

### `FleetPage` (`pages/Fleet.tsx`)

Owns:

- Fleet overview load (`fleetOverview` / `fleetRefresh`) + optional 15s auto-refresh.
- Selection set, label filter, view mode (`cards` | `table`, persisted in `localStorage`).
- Batch apply/start/stop; single-node apply/start/stop from cards.
- Query sync for `?node=` ↔ drawer.
- Orchestrates modal open and list refresh after mutations.

### `StatsBar`

Pure presentational: counts from `FleetOverview`.

### `AddNodeModal`

Bootstrap form + install command display (from former Nodes page).  
On success: show install command in-modal; parent reloads fleet.

### `NodeDetailDrawer`

Port of former `NodeDetail` into a right-side drawer:

- Connection / TLS, install command, inbound attach + deploy, start/stop, metrics, config preview, log stream.
- Close → clear `?node` and abort log stream.

### Shared `lib/nodeDisplay.ts`

`formatBytes`, `formatTime`, `statusLabel`, `statusClass`, `runtimeLabel`, `isOnlineStatus`, `taskKindLabel`, `taskStatusLabel`.

## Data flow

1. Mount: `fleetOverview()` (cached) for fast paint.
2. Manual “探测全部” or auto interval: `fleetRefresh()`.
3. Mutations (bootstrap, delete, apply, inbounds, connection save): refresh overview (and drawer-local loads as needed).
4. Batch APIs remain `POST /batch/*`; no API removal.

## Migration / deletion

Delete after Fleet lands:

- `pages/Dashboard.tsx`
- `pages/Nodes.tsx`
- `pages/NodeDetail.tsx`
- `pages/Batch.tsx`

Update `App.tsx` nav + routes; Login continues to `/` (or `/nodes` → redirect).

## UX details

- **Add node:** primary button “添加节点” opens modal; not a permanent form on the page.
- **Detail:** clicking name / “详情” opens drawer; no full-page navigation.
- **Batch:** toolbar actions require ≥1 selected node; optional label filter narrows list then “全选” selects visible rows only.
- **Empty state:** CTA to open AddNodeModal.

## Testing / verification

- `npm run build` in `web/` succeeds.
- Manual smoke: list, add modal, open drawer via click and `/?node=`, batch toolbar, redirects from `/nodes`, `/batch`, `/nodes/:id`.

## Implementation notes

- Prefer compositional components under `web/src/components/`.
- Modal/drawer CSS: overlay + panel; drawer width ~min(480px, 100vw); high z-index above topbar.
- Escape key and backdrop click close modal/drawer (abort streams on drawer close).
