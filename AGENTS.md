# AGENTS.md — conventions for the lab-data server

Contract for anyone (human or LLM) adding code. Source-of-truth design is
`lab-data-system-architecture-updated.md`. Stack and module conventions are
reused from the `project-management-site` repo.

## Golden rules

1. **One module owns its tables.** Never read/write another module's tables
   directly. Cross-module access goes through the owning package's Go API.
   Cross-entity references to sibling domains are stored as **bare ids** (no FK
   to a sibling module's tables) so modules stay independent and parallel-buildable.
2. **Structural truth lives in Postgres, is editable, and is versioned.** Names
   and paths only *propose* metadata; stored truth is explicit DB relationships
   and parameter-version ids.
3. **Files first, DB last.** Raw bytes are durably placed and hash-verified
   before the database records a run as `promoted`.
4. **Idempotency everywhere.** Every operation is keyed by `run_id`, manifest
   hash, archive hash, or a job idempotency key so retries are safe.
5. **Do not let arbitrary code set `runs.state`.** All transitions go through the
   `statemachine` transition function, which validates legality and writes an
   audit row (`run_state_transitions`).
6. **Audit every mutation and every transition.** Tests assert it.

## Build & tooling

- Go module: `github.com/colnio/data-pipelines`. `GOTOOLCHAIN=auto` is set.
- HTTP: **huma v2** over **chi v5** (OpenAPI 3.1 auto-generated). DB: **pgx v5**
  pool, hand-written SQL inside each module. Migrations: **goose**, embedded in
  `/migrations`, run on boot behind a Postgres advisory lock.
- Jobs queue: a **custom Postgres `jobs` table** claimed with `FOR UPDATE SKIP
  LOCKED` (architecture §15) — NOT River, NOT Prefect.
- **Do not run `go mod tidy` / `go get` while other agents work in parallel** —
  it corrupts `go.mod`. Shared deps are pre-fetched centrally. Available external
  deps: chi/v5, huma/v2, pgx/v5, goose/v3, godotenv, golang-jwt/v5, google/uuid,
  golang.org/x/crypto, klauspost/compress (zstd), stretchr/testify.
- Build/test only your own package: `go test ./internal/<yourpkg>/...`. Do NOT
  run `go build ./...` (sibling packages may be mid-flight). Do NOT add files
  outside your assigned directory. Do NOT edit `cmd/server/main.go` or
  `cmd/worker/main.go` — expose `Register`/`NewService`; the orchestrator wires.

## Migrations

- One `*.sql` file per change in `/migrations`, goose format
  (`-- +goose Up` / `-- +goose Down`). Numbers strictly increasing, append-only.
- Reserved ranges: extensions 00001; the lab-data schema spine 00010–00090.
- Use `gen_random_uuid()` (pgcrypto) for uuid PKs; `timestamptz NOT NULL DEFAULT now()`.

## Module structure & wiring

- Expose `func Register(api huma.API, svc *Service)` and `func NewService(...)`.
- Use shared `platform` helpers: `Principal`/`PrincipalFrom`, error constructors
  (`NotFound`/`Forbidden`/`BadRequest`/`Conflict`/`Errorf`), `RequireScope`,
  ETag helpers. Route framework errors through the `ErrorModel` envelope.
- huma gotchas (crash at boot, not in unit tests): **no pointer types** for
  `query`/`path`/`header` params (use value types, treat zero as absent);
  prefer unique, module-prefixed input type names to avoid schema-name collisions.

## Shared domain types

`internal/domain` holds the cross-cutting contracts — do not redefine these:
- `RunState` + state constants; `JobState`/`JobType`; `ActorType`; `CompletionSource`.
- `Manifest`/`ManifestFile`/`ManifestArchive` (the agent↔server JSON contract, §11).
- Row structs: `Run`, `RunFile`, `Job`, `Agent`, `AgentAllowedRoot`, `RunStateTransition`.

## Testing (Definition of Done per module)

- Pure-logic packages (manifest validation, safe-unpack, path validation,
  transition legality): table-driven unit tests, no DB.
- DB-backed packages: use `internal/testsupport.NewPool(t)` — it auto-creates the
  test DB, runs migrations once, and **skips cleanly** when Postgres is
  unreachable (so `go test ./...` passes without Docker). Set
  `TEST_DATABASE_URL` to a dedicated database.
- Security-critical paths (safe archive unpacking, agent path validation) MUST
  have negative tests: traversal, symlink, absolute path, oversize, manifest
  mismatch, unexpected files.
