# Lab Data Review Web UI

Vite + React + TypeScript SPA using Mantine 8, TanStack Router 1, TanStack Query 5.
Talks to the Go API at `http://localhost:8080`.

## Prerequisites

- Node.js v18+
- pnpm 11.4+

## Install

```bash
cd web
pnpm install
```

## Dev server

Expects the Go API running on `:8080`. The Vite dev server proxies `/v1/*` to it.

```bash
pnpm dev
# Open http://localhost:5173
```

## Regenerate TypeScript types from OpenAPI spec

```bash
pnpm openapi
# Writes src/api/schema.d.ts from ./openapi.json
```

Re-run this whenever `openapi.json` changes.

## Build

```bash
pnpm build
# Type-checks then runs vite build → dist/
```

## Tests

```bash
pnpm test:run   # run once
pnpm test       # watch mode
```

## Pages & routes

| Path | Description |
|------|-------------|
| `/login` | Login form |
| `/` | Runs browse (filter by state/sample/device/type) |
| `/runs/:id` | Run detail: metadata, files table, audit timeline |
| `/reviews` | Runs awaiting review with key metrics preview |
| `/reviews/:runId` | Full review detail: metrics, warnings, LLM summary, actions |

## Follow-ups / known gaps

- **Artifact image serving**: `plots_json` contains server-side file paths. A `GET /v1/files/:id` or similar endpoint is needed before images can be rendered. Currently shown as raw JSON.
- **Pagination**: the `next_cursor` field from list endpoints is not wired up — all queries use default limit.
- **Register page**: `/v1/auth/register` is implemented in the API but there is no registration UI page yet (admin workflow assumed for now).
- **Role-based UI**: some actions (approve, quarantine) should be gated on `global_role` in a production build.
