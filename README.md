# Lab Data Management System

Reconstructable measurement-data management for a materials-science / nanoelectronics lab
(FETs, capacitors, resistive switches, spin valves, AFM, ellipsometry). Built to the spec in
[`lab-data-system-architecture-updated.md`](./lab-data-system-architecture-updated.md).

**Hard requirement:** any published result must be reconstructable from recorded inputs.

A measurement-PC agent declares a completed run by POSTing a **manifest**; the server pulls the
archive, hash-verifies and safely unpacks it, **atomically promotes** the bytes into an immutable
raw store, then a Python worker parses/validates/plots it and a human reviewer approves it. Every
state change is audited; published outputs are append-only with a reproducibility receipt.

## Stack

| Layer | Tech |
|---|---|
| API + web | Go (chi + **huma v2**, OpenAPI 3.1) + Vite/React/Mantine |
| Catalog / state / queue | **Postgres** (source of truth) |
| Migrations | goose (embedded, advisory-locked, run on boot) |
| Durable queue | custom `jobs` table, `FOR UPDATE SKIP LOCKED` (no River/Prefect) |
| Processing workers | **Python** (psycopg, numpy, matplotlib) |

Stack and module conventions are reused from the sibling project `colnio/project-management-site`
(see [`AGENTS.md`](./AGENTS.md)).

## Repo layout

```
cmd/server        API server (boots: config → pool → migrate → huma+chi → :8080)
cmd/worker        Go background worker (pull pipeline + publish)
internal/
  domain          shared contracts: RunState/Job/Manifest + row structs
  platform        huma+chi server, error envelope, principal, auth/rate-limit/idempotency
  config db       config loader; pgx pool + goose runner; DBTX interface
  manifest        manifest validation + canonical hashing (§11)
  transfer        safe tar/zst unpack, hash verify, atomic immutable promote (§13,§5)
  jobs            durable Postgres queue + worker runtime (§15)
  statemachine    the only writer of runs.state; audited transitions (§14,§16)
  agentauth       bcrypt agent auth + meas_path root-confinement (§4)
  run             runs/run_files/manifests repo + browse API (§7,§19)
  ingest          agent manifest endpoint + transfer/promote pipeline (§13)
  auth            human email/password + JWT (platform Verifier) (§22)
  review          review gate + Pipeline-B publish + reproducibility receipt (§17,§5)
migrations        goose SQL: the §23 schema spine + transition_run() function
workers/          Python Pipeline-A: claim parse_run → parse/validate/plot → awaiting_review (§17)
web/              Vite review UI (browse, run detail, review queue, approve/publish)
```

## Quickstart

```sh
# 1. Postgres (shared dev stack)
make up && make dbs                 # starts Postgres, creates labdata + labdata_test

# 2. API server (migrates on boot) → http://localhost:8080  (/docs for OpenAPI)
make server

# 3. Go worker (transfer pull pipeline + publish) — separate shell
make worker

# 4. Python Pipeline-A worker — separate shell
cd workers && python3 -m venv .venv && .venv/bin/pip install -r requirements.txt
DATABASE_URL=postgres://lab:lab@localhost:5432/labdata LABDATA_ROOT=/srv/labdata \
  .venv/bin/python worker.py

# 5. Web UI (dev) → http://localhost:5173, proxies to :8080
cd web && pnpm install && pnpm dev

# 6. JupyterHub (dev container) → http://localhost:8000 (architecture §18)
make readonly NB_PASSWORD=secret   # create labdata_nb read-only Postgres user
make jupyter   NB_PASSWORD=secret   # build + run the Hub (needs the API + Postgres up)
```

JupyterHub gives lab members **read-only** server-side exploration: log in with the
same web account (API-backed SSO), `import labdata` for catalog queries
(`labdata.catalog` over the `v_*` views as `labdata_readonly`), `labdata.store`
to resolve raw/processed paths, and `labdata.duck` for DuckDB SQL. Raw/published
are mounted read-only; only per-user scratch is writable. Starter notebooks live
in `notebooks/`. Production deploys via TLJH — see `deploy/jupyterhub/TLJH.md`.

Configuration is via env / `.env` (see [`.env.example`](./.env.example)).

## The lifecycle

```
agent POST /v1/agents/manifest
  → declared → pulling → unpacked → verified → promoted        (Go worker, ingest pipeline)
    → parsing → validated → processing → awaiting_review        (Python Pipeline-A worker)
      → approved → publishing → published                       (reviewer in UI + Go publish)
```

Failure/holding states (`transfer_failed`, `manifest_mismatch`, `unsafe_archive`, `parser_failed`,
`quarantined`, `needs_metadata`, …) are first-class and recoverable. The transition graph is
enforced identically in Go (`internal/statemachine`) and in SQL (`transition_run()`, used by the
Python worker) — a parity test guards against drift.

## Testing

```sh
make test          # go test ./... (parallel; each package auto-isolates its own test DB)
make test-ci       # one shared test DB, serialized (-p 1)
cd workers && TEST_DATABASE_URL=postgres://lab:lab@localhost:5432/labdata_test_py \
  .venv/bin/python -m pytest tests
cd web && pnpm test:run
```

DB-backed Go tests skip cleanly when Postgres is unreachable, so `go test ./...` always passes
without Docker.

## v1 acceptance criteria (spec §25)

| Criterion | Status |
|---|---|
| Run finalized by manifest declaration | ✅ `POST /v1/agents/manifest` |
| Server pulls, verifies, stages, promotes without manual copying | ✅ `internal/ingest` pipeline |
| Raw files immutable after promotion | ✅ promoted tree is `0444`/`0555` |
| Python worker parses ≥1 measurement type | ✅ FET transfer (`workers/`) |
| Reviewer can approve / request changes | ✅ `internal/review` + UI |
| Scientific param correction → new version + reprocess | ◑ identity correction done; param-version reprocess is a follow-up |
| Published outputs append-only and hashed | ✅ `published_results` / `published_artifacts` |
| Reproducibility receipt per published result | ✅ §5 receipt in `published_results` |
| Missed notification recoverable by reconciliation | ◑ queue `ReclaimStale` done; agent reconciliation scan is a follow-up |
| Duplicate notifications/retries don't duplicate runs/artifacts | ✅ idempotent on run_id / manifest hash / idempotency key |
| JupyterHub read-only explore | ✅ `v_*` views + `labdata_readonly` role + `labdata` notebook lib + Hub (§18) |
| Backups + tested restore | ☐ deferred (spec §6) |

## Follow-ups

- Artifact image-serving endpoint (the UI shows plot file paths; serving the PNGs is next).
- Operator completion path + mid-measurement guard (§12), agent reconciliation scan (§13).
- Scientific parameter-correction → reprocess workflow (§9).
- Backups/restore (§6), JupyterHub (§18), LLM review (§20) — all explicitly deferred in the spec.
