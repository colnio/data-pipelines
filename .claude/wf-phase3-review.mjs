export const meta = {
  name: 'labdata-phase3-review',
  description: 'Human auth, review/publish (Go), and Python Pipeline-A worker — parallel Sonnet agents',
  phases: [{ title: 'Modules', detail: 'auth + review/publish + python parse worker' }],
}

const REPO = '/Users/colnio/go/data-pipelines'

const SUMMARY = {
  type: 'object',
  properties: {
    package: { type: 'string' },
    files_created: { type: 'array', items: { type: 'string' } },
    api_surface: { type: 'array', items: { type: 'string' } },
    tests_command: { type: 'string' },
    tests_passed: { type: 'boolean' },
    test_summary: { type: 'string' },
    integration_notes: { type: 'string', description: 'what the orchestrator needs to wire this in' },
    notes: { type: 'string' },
  },
  required: ['package', 'files_created', 'tests_passed', 'test_summary'],
  additionalProperties: false,
}

function goRules(pkgDir, testDB) {
  return `
REPO: ${REPO} (module github.com/colnio/data-pipelines). cd there for all commands.
READ FIRST: ${REPO}/AGENTS.md ; ${REPO}/lab-data-system-architecture-updated.md (the sections in your task) ;
  ${REPO}/internal/domain/*.go ; ${REPO}/internal/platform/{principal,errors,scopes,deps,auth_middleware}.go ;
  ${REPO}/internal/run/run.go ; ${REPO}/internal/statemachine/statemachine.go ; ${REPO}/internal/jobs/{queue,worker}.go ;
  and the migration files holding the tables you read/write.
Match the huma module pattern already used in this repo (huma.Register + Operation; input structs with Body/query/path/header tags — NO pointer types for query/path/header; output structs with a Body field; return platform error constructors; read the caller via platform.PrincipalFrom and gate with platform.RequireScope).
HARD CONSTRAINTS:
- Work ONLY inside ${pkgDir}. Do NOT edit cmd/server/main.go, cmd/worker/main.go, go.mod, go.sum, or sibling packages. Expose Register/NewService; the orchestrator wires them.
- Do NOT run 'go mod tidy' / 'go get' / 'go build ./...'. Available deps: chi/v5, huma/v2 (+humatest), pgx/v5, goose/v3, godotenv, golang-jwt/jwt/v5, google/uuid, golang.org/x/crypto, stretchr/testify.
- Build/test ONLY your package: go vet ./${pkgDir.replace(REPO + '/', '')}/... and go test ./${pkgDir.replace(REPO + '/', '')}/...
- DB tests: use internal/testsupport.NewPool(t); run with TEST_DATABASE_URL=postgres://lab:lab@localhost:5432/${testDB}?sslmode=disable . A local Postgres IS running (localhost:5432, user lab, pw lab) so tests WILL run and must pass. gofmt your files.
DEFINITION OF DONE: compiles, 'go vet' clean, 'go test ./<yourpkg>/...' passes (exit 0). Report the exact command + pass/fail.`
}

const authPrompt = `Build internal/auth: human reviewer authentication — email/password accounts and JWT access tokens — implementing the platform.TokenVerifier interface so the platform AuthResolver middleware resolves a Principal (architecture §22; reviewers approve in the UI). The 'users' table already exists (migration 00090_ops.sql: id uuid, email unique, password_hash, display_name, global_role in {admin,pi,member}, status in {pending,active,disabled}, created_at).

Read internal/platform/{deps.go (TokenVerifier), principal.go (Principal), auth_middleware.go (dispatch by token prefix)}.

Create internal/auth/auth.go:
- type Config struct { JWTSigningKey string; AccessTokenTTL time.Duration; AllowedEmailDomains []string; IsProduction bool }  (DECOUPLED from internal/config — the orchestrator maps config.Config → this).
- type Service struct{...} ; func NewService(pool *pgxpool.Pool, cfg Config, log *slog.Logger) (*Service, error)  (error if signing key empty in production).
- Implement platform.TokenVerifier on *Service:
    * VerifyAccessToken(ctx, raw string) (*platform.Principal, error): parse the HS256 JWT signed with cfg.JWTSigningKey; on valid + unexpired, load the user (must be status='active'), return &platform.Principal{UserID, Email, GlobalRole}. Invalid/expired/disabled → error.
    * VerifyPAT(ctx, raw) (*platform.Principal, error): return platform.Unauthorized("personal access tokens not supported yet").
    * VerifyInternalAIToken(ctx, raw) (*platform.Principal, error): return platform.Unauthorized("internal tokens not supported").
- func (s *Service) issueAccessToken(userID uuid.UUID, email, role string) (string, error): golang-jwt v5, HS256, claims sub=userID, email, role, exp=now+AccessTokenTTL, iat.
- password hashing with golang.org/x/crypto/bcrypt.

Create internal/auth/api.go (Register + handlers):
- POST /v1/auth/register {email, password, display_name}: validate email is under an AllowedEmailDomains domain; password length >= 8; insert user (bcrypt hash). In development status='active'; in production status='pending'. Reject duplicate email (409). Return {user_id}.
- POST /v1/auth/login {email, password}: load user, bcrypt compare; reject if status != 'active' (403). Return {access_token, token_type:"bearer", expires_in, user:{id,email,display_name,global_role}}.
- GET /v1/auth/me: read platform.PrincipalFrom(ctx); 401 if absent; return the current user profile.
- (no refresh-cookie complexity required for v1.)

Create internal/auth/auth_test.go (DB via testsupport.NewPool):
- register → login → VerifyAccessToken round trip returns the right Principal.
- wrong password rejected; unknown email rejected; disallowed email domain rejected at register; duplicate email → conflict; expired/garbage token → VerifyAccessToken error; pending/disabled user cannot login.
Test against TEST_DATABASE_URL=postgres://lab:lab@localhost:5432/labdata_test_auth?sslmode=disable
${goRules(REPO + '/internal/auth', 'labdata_test_auth')}
Your test command: TEST_DATABASE_URL=postgres://lab:lab@localhost:5432/labdata_test_auth?sslmode=disable go test ./internal/auth/...
INTEGRATION: report the exact NewService signature and the platform.TokenVerifier method set so the orchestrator can wire *Service as platform.ServerDeps.Verifier.`

const reviewPrompt = `Build internal/review: the human review gate (architecture §17 "Human review") and Pipeline B publishing (§17 "Pipeline B", §5 reproducibility receipt). Go module. Depends on internal/{domain,platform,run,statemachine,jobs}. The Python parser WRITES review_artifacts; this module READS them and owns review_decisions, published_results, published_artifacts.

Read migrations 00070_pipeline.sql (parser_results, analysis_artifacts, review_artifacts, review_decisions) and 00080_publishing.sql (processing_environments, published_results, published_artifacts), plus internal/run/run.go (Repo: Get, Files, Transitions, Manifest), internal/statemachine/statemachine.go (Transition + typed errors), internal/jobs (Queue.Enqueue, Handler).

Create internal/review/review.go:
- type Service struct{...} ; func NewService(pool *pgxpool.Pool, runs *run.Repo, queue *jobs.Queue, log *slog.Logger) *Service
- helper to map statemachine typed errors (*ErrStateMismatch→409, *ErrIllegalTransition→422, *ErrRunNotFound→404) to platform errors via errors.As.

Create internal/review/api.go (Register + handlers; all require platform principal + scope):
- GET  /v1/reviews?limit=  → runs in state 'awaiting_review' with their latest review_artifact summary (metrics/warnings). scope read:reviews.
- GET  /v1/reviews/{run_id} → bundle: run + latest review_artifact (metrics_json, plots_json, parser_warnings_json, llm_summary) + files + transitions. scope read:reviews. 404 if no run.
- POST /v1/reviews/{run_id}/approve → statemachine.Transition(awaiting_review→approved, actor reviewer=principal email); INSERT review_decisions(decision='approve', reviewer, review_artifact_id=latest); enqueue a publish_run job (idempotency_key 'publish:'+run_id). scope write:reviews.
- POST /v1/reviews/{run_id}/request-changes {reason} → Transition(awaiting_review→changes_requested); review_decisions(decision='request_changes', reason). scope write:reviews.
- POST /v1/reviews/{run_id}/quarantine {reason} → Transition(awaiting_review→quarantined); review_decisions(decision='quarantine', reason). scope write:reviews.
- PATCH /v1/reviews/{run_id}/metadata {operator_comment} → identity/display correction (§9): a simple audited UPDATE of runs.operator_comment (do NOT change runs.state here; this is an identity edit, not a scientific correction). scope write:reviews.

Create internal/review/publish.go (Pipeline B worker handler):
- func (s *Service) Handlers() map[domain.JobType]jobs.Handler  → { domain.JobPublishRun: s.handlePublish }
- handlePublish(ctx, job): for an 'approved' run, Transition(approved→publishing); build the reproducibility receipt (§5): INSERT published_results (run_id, manifest_hash from runs, raw_file_hashes_json from run_files sha256 list, parameter_version_ids_json from the stored manifest param_versions, parser_version from latest parser_results, review_decision_id from latest approve decision, published_by from the approve decision's reviewer, published_at=now()); for each analysis_artifacts row of the run, INSERT published_artifacts (path, sha256, kind) — append-only, never overwrite; then Transition(publishing→published). Idempotent: if already 'published', no-op success. Return a small JSON result.

Create internal/review/review_test.go (DB via testsupport.NewPool):
Seed helper: insert an agent, a run, drive it to 'awaiting_review' (you may UPDATE runs.state directly IN THE TEST setup to reach the precondition, or call statemachine.Transition through the legal path — either is fine for seeding), and insert a review_artifact. Then:
- approve → run becomes 'approved', a review_decisions row exists, a publish_run job is enqueued.
- request-changes → 'changes_requested' + decision row.
- handlePublish on the approved run → published_results + published_artifacts rows created and run becomes 'published'; running it again is a no-op (still one published_results row).
- GET handlers via humatest with a principal in context: build the huma API, and inject a principal using platform.WithPrincipal on the request context (or test the Service methods directly if simpler). At minimum unit-test the publish + approve DB logic.
Test against TEST_DATABASE_URL=postgres://lab:lab@localhost:5432/labdata_test_review?sslmode=disable
${goRules(REPO + '/internal/review', 'labdata_test_review')}
Your test command: TEST_DATABASE_URL=postgres://lab:lab@localhost:5432/labdata_test_review?sslmode=disable go test ./internal/review/...
INTEGRATION: report NewService signature, the Register function, and Handlers() so the orchestrator wires the routes (server) and the publish_run handler (worker).`

const pythonPrompt = `Build the Python Pipeline-A worker under workers/ (architecture §17 "Pipeline A", §3 "Processing workers", §16). It claims parse_run jobs from the SAME Postgres jobs table the Go server uses, parses one measurement type, validates, computes metrics, plots, writes scientific outputs, and transitions the run promoted→parsing→validated→processing→awaiting_review. It NEVER writes runs.state directly — it calls the SQL function transition_run() (migration 00100), which enforces legality and writes the audit row.

CONTEXT TO READ: ${REPO}/lab-data-system-architecture-updated.md §17 and §15-§16 ; the schema in ${REPO}/migrations/00020_runs.sql, 00030_jobs.sql, 00070_pipeline.sql, 00100_transition_function.sql. The Go pipeline stores raw files at LABDATA_ROOT/raw/<sample_id>/<device_id>/<run_id>/<filename> and records run_files(run_id,name,bytes,sha256). The manifest JSON is in manifests.raw_json (latest by created_at) and includes measurement_type and the file list.

DELIVERABLES under workers/ :
- workers/requirements.txt — pin dependencies. Use a Postgres driver that installs cleanly on this Python (try psycopg[binary]; if no wheel for this interpreter, use pg8000 which is pure-Python). numpy for math, matplotlib (Agg backend, no display) for plotting, pytest for tests. If a package can't install, choose a working alternative and NOTE it.
- workers/labdata/__init__.py
- workers/labdata/db.py — connection from DATABASE_URL (default postgres://lab:lab@localhost:5432/labdata?sslmode=disable); thin helpers: claim_job(worker_id, job_types) using SELECT ... FOR UPDATE SKIP LOCKED then UPDATE to running (mirror internal/jobs/queue.go semantics: priority DESC, created_at ASC, available_at<=now(), attempt_count+1, locked_until); complete_job(id, result); fail_job(id, err, max_attempts) with exponential backoff and dead at max; transition(run_id, expected_from, to, actor_type, actor_id, reason, payload) calling SELECT transition_run(...). Map the function's RAISE codes to Python exceptions.
- workers/labdata/parsers/fet_transfer.py — parse a 2-column numeric measurement file (header line then "V,I" rows, comma or whitespace separated; tolerate scientific notation). Return parsed arrays + validation result (architecture §17 parser validation: expected column count, declared-vs-actual row count if a header declares it, reject all-NaN trailing rows, basic sanity). Raise/flag on malformed input.
- workers/labdata/parsers/fet_transfer.py also computes metrics: on/off current ratio, I_max, I_min, and a crude threshold-voltage estimate (e.g. max-transconductance intercept or simply the V at steepest dI/dV). Keep robust to small/noisy data.
- workers/labdata/pipeline.py — the parse_run handler: load run + manifest + raw dir; transition to 'parsing' (expected_from '' tolerant: current may be 'promoted' or 'needs_metadata'); parse + validate the primary data file; on validation failure transition to 'parser_failed' (or 'quarantined') and fail the job; on success render an I-V plot PNG to LABDATA_ROOT/processed/<run_id>/analysis_version_001/transfer.png (matplotlib Agg), sha256 it; INSERT parser_results, analysis_artifacts (the plot), review_artifacts (metrics_json, plots_json=[path], parser_warnings_json, llm_summary=''); transition parsing→validated→processing→awaiting_review; complete the job.
- workers/worker.py — entrypoint: poll loop claiming job_types=['parse_run'], dispatch to pipeline, complete/fail; respects SIGINT. Reads DATABASE_URL and LABDATA_ROOT from env.
- workers/tests/test_fet_transfer.py — pure unit tests of the parser + metrics on in-memory sample data (valid + malformed cases). No DB.
- workers/tests/test_pipeline_integration.py — DB integration (skip cleanly if Postgres unreachable): connect to TEST DB; ensure schema present (the Go migrations already created it — connect to a database that has them, default postgres://lab:lab@localhost:5432/labdata_test?sslmode=disable, overridable via TEST_DATABASE_URL). Seed: insert an agent, a run in state 'promoted' with sample_id/device_id, a manifest row, write a small raw data file under a temp LABDATA_ROOT/raw/<sample>/<device>/<run>/, enqueue a parse_run job. Run ONE worker iteration. Assert: run state becomes 'awaiting_review', a review_artifacts row exists with metrics, the job is 'succeeded', the plot file exists. Truncate the tables you touch between tests.
- workers/README.md — how to create the venv, install, and run the worker + tests.
- workers/pyproject.toml OR setup is optional; requirements.txt is enough.

SETUP/RUN: create a virtualenv at workers/.venv (python3 -m venv), pip install -r requirements.txt into it, and run pytest with it. The .venv is gitignored. The local Postgres is running at localhost:5432 (user lab, pw lab); the 'labdata_test' database has all Go migrations applied (it is created/migrated by the Go test helper) — if it does not exist or lacks the schema, create it and apply migrations by connecting and running the SQL, OR point TEST_DATABASE_URL at a database you migrate. Simplest: use TEST_DATABASE_URL=postgres://lab:lab@localhost:5432/labdata_test_py?sslmode=disable and, in a test fixture, create that DB and apply all ${REPO}/migrations/*.sql in numeric order (strip the goose '-- +goose' directives: execute the statements between '-- +goose Up' and '-- +goose Down'; respect '-- +goose StatementBegin/StatementEnd' blocks as single statements).

HARD CONSTRAINTS: work ONLY under workers/. Do NOT touch Go files, go.mod, or migrations. Match the jobs-table and transition_run contracts EXACTLY (read the SQL).
DEFINITION OF DONE: 'workers/.venv/bin/python -m pytest workers/tests' passes (exit 0) with Postgres available. Report the exact command and pass/fail, and which DB driver/plot lib you ended up using.
Your test command: cd ${REPO} && TEST_DATABASE_URL=postgres://lab:lab@localhost:5432/labdata_test_py?sslmode=disable workers/.venv/bin/python -m pytest workers/tests -v`

phase('Modules')
log('Phase 3: auth (Go) + review/publish (Go) + Python Pipeline-A worker, in parallel')
const results = await parallel([
  () => agent(authPrompt,   { phase: 'Modules', label: 'auth',   model: 'sonnet', schema: SUMMARY }),
  () => agent(reviewPrompt, { phase: 'Modules', label: 'review', model: 'sonnet', schema: SUMMARY }),
  () => agent(pythonPrompt, { phase: 'Modules', label: 'python', model: 'sonnet', schema: SUMMARY }),
])
return { results: results.filter(Boolean) }
