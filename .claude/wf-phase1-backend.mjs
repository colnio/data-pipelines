export const meta = {
  name: 'labdata-phase1-backend',
  description: 'Build lab-data Go backend leaf packages in parallel with Sonnet agents',
  phases: [
    { title: 'Schema+Pure', detail: 'migrations spine + manifest validation + safe transfer (no cross-deps)' },
    { title: 'DB modules', detail: 'jobs queue + state machine + agent auth (need the schema)' },
  ],
}

const REPO = '/Users/colnio/go/data-pipelines'

const SUMMARY = {
  type: 'object',
  properties: {
    package: { type: 'string', description: 'package or area built' },
    files_created: { type: 'array', items: { type: 'string' }, description: 'relative paths created/edited' },
    api_surface: { type: 'array', items: { type: 'string' }, description: 'exported funcs/types other packages can call' },
    migrations_added: { type: 'array', items: { type: 'string' }, description: 'migration filenames added (if any)' },
    tests_command: { type: 'string', description: 'exact go test command run' },
    tests_passed: { type: 'boolean', description: 'true only if the test command exited 0' },
    test_summary: { type: 'string', description: 'tail of test output / pass-fail counts' },
    integration_notes: { type: 'string', description: 'what the orchestrator must know to wire this in (signatures, gotchas)' },
    notes: { type: 'string' },
  },
  required: ['package', 'files_created', 'tests_passed', 'test_summary'],
  additionalProperties: false,
}

// Shared rules every agent must follow.
function rules(pkgDir, testDB) {
  return `
REPO: ${REPO} (module github.com/colnio/data-pipelines). cd there for all commands.

READ FIRST (do not skip):
- ${REPO}/AGENTS.md  (conventions, golden rules, huma gotchas)
- ${REPO}/lab-data-system-architecture-updated.md  (design source of truth — read the sections named in your task)
- ${REPO}/internal/domain/state.go, manifest.go, types.go  (the shared contracts — USE these types, do not redefine)
- ${REPO}/internal/db/db.go  (DBTX interface, pool)
- ${REPO}/internal/testsupport/db.go  (the skip-if-unset Postgres test helper)
- the migration files your tables live in (read them; the SQL columns are the source of truth)

HARD CONSTRAINTS:
- Work ONLY inside ${pkgDir}. Do NOT create or edit files anywhere else. Do NOT edit cmd/server/main.go, cmd/worker, go.mod, go.sum, or sibling internal/ packages.
- Do NOT run 'go mod tidy', 'go get', or 'go build ./...'. All deps are already available: chi/v5, danielgtaylor/huma/v2, jackc/pgx/v5, pressly/goose/v3, joho/godotenv, golang-jwt/jwt/v5, google/uuid, golang.org/x/crypto/bcrypt, klauspost/compress/zstd, stretchr/testify.
- Build/test ONLY your package, e.g.:  go vet ./${pkgDir.replace(REPO + '/', '')}/...  and  go test ./${pkgDir.replace(REPO + '/', '')}/...
- For DB-backed tests, set a UNIQUE test database so parallel agents never collide:
  TEST_DATABASE_URL=postgres://lab:lab@localhost:5432/${testDB}?sslmode=disable
  Use internal/testsupport.NewPool(t) — it auto-creates that DB, runs all migrations, and SKIPS cleanly if Postgres is unreachable. A local Postgres IS running at localhost:5432 (user lab, password lab), so your tests WILL run — they must pass.
- Run gofmt on your files. Make sure go vet is clean for your package.

DEFINITION OF DONE: your package compiles, 'go vet ./<yourpkg>/...' is clean, and 'go test ./<yourpkg>/...' passes (exit 0). Report the EXACT command and whether it passed in the structured result. Do not claim success without running it.`
}

// ─────────────────────────────────────────────────────────────────────────────
// STAGE 1: migrations spine + the two pure (no-DB) packages.
// ─────────────────────────────────────────────────────────────────────────────

const migrationsPrompt = `You are completing the Postgres schema spine for the lab-data system (architecture §23, plus §5 §8 §9 §17 §20). The integration-critical tables (agents, agent_allowed_roots, runs, run_files, manifests, instrument_metadata_raw, run_state_transitions, jobs) ALREADY EXIST in migrations 00010–00030 — do NOT touch or recreate them. You add the REMAINING spine tables as new goose migrations.

Read migrations 00010_agents.sql, 00020_runs.sql, 00030_jobs.sql first to match style (goose '-- +goose Up'/'-- +goose Down', GENERATED ALWAYS AS IDENTITY for bigint PKs, text natural-key PKs for catalog entities, timestamptz NOT NULL DEFAULT now(), jsonb for *_json columns, gen_random_uuid() for uuid PKs).

Create these migration files (numbers strictly increasing, append-only):
- migrations/00040_catalog.sql:
    samples (id text PK e.g. '9D66P1'; material_stack text; dielectric jsonb; fabrication_batch text; params_json jsonb; notes text; created_at)
    devices (id text PK e.g. 'gr_mob_31_f1'; sample_id text REFERENCES samples(id); device_class text NOT NULL e.g. fet/mim_cap/resistive_switch/spin_valve/afm_area/ellipsometry_region; fabrication_id text; lifecycle_state text; notes text; created_at)
    contact_configs (id text PK e.g. 'gr_mob_31_f1__sd_b1b2'; device_id text REFERENCES devices(id); terminal_roles_json jsonb; is_default boolean DEFAULT false; notes text; created_at)
  (FKs WITHIN the catalog module are fine. runs already references these by bare text id with NO FK — leave that as is.)
- migrations/00050_parameter_versions.sql (architecture §9):
    sample_parameter_versions (id bigint identity PK; sample_id text; version int; oxide_thickness_nm double precision; dielectric text; stack_composition_json jsonb; params_json jsonb; created_at; UNIQUE(sample_id, version))
    contact_geometry_versions (id identity PK; contact_config_id text; version int; length_um double precision; width_um double precision; terminal_roles_json jsonb; created_at; UNIQUE(contact_config_id, version))
    processing_parameter_versions (id identity PK; scope text; scope_key text; version int; params_json jsonb; created_at; UNIQUE(scope, scope_key, version))
    calibration_versions (id identity PK; instrument text; version int; constants_json jsonb; created_at; UNIQUE(instrument, version))
- migrations/00060_conditions.sql (architecture §8):
    condition_labels (id identity PK; canonical_name text UNIQUE NOT NULL; aliases_json jsonb DEFAULT '[]'; description text DEFAULT ''; created_at)
    run_condition_labels (run_id text REFERENCES runs(id) ON DELETE CASCADE; condition_label_id bigint REFERENCES condition_labels(id); PRIMARY KEY(run_id, condition_label_id))
    treatment_events (id identity PK; device_id text; treatment_type text; started_at timestamptz; ended_at timestamptz; parameters_json jsonb DEFAULT '{}'; performed_by text; notes text; created_at)
- migrations/00070_pipeline.sql (architecture §17):
    parser_results (id identity PK; run_id text REFERENCES runs(id) ON DELETE CASCADE; parser_version text; status text; columns_expected int; rows_declared int; rows_actual int; warnings_json jsonb DEFAULT '[]'; output_json jsonb DEFAULT '{}'; created_at)
    analysis_artifacts (id identity PK; run_id text REFERENCES runs(id) ON DELETE CASCADE; analysis_version int NOT NULL DEFAULT 1; kind text; path text; sha256 text; meta_json jsonb DEFAULT '{}'; created_at)
    review_artifacts (id identity PK; run_id text REFERENCES runs(id) ON DELETE CASCADE; version int NOT NULL DEFAULT 1; summary_json jsonb DEFAULT '{}'; plots_json jsonb DEFAULT '[]'; metrics_json jsonb DEFAULT '{}'; parser_warnings_json jsonb DEFAULT '[]'; llm_summary text DEFAULT ''; created_at)
    review_decisions (id identity PK; run_id text REFERENCES runs(id) ON DELETE CASCADE; review_artifact_id bigint REFERENCES review_artifacts(id); decision text CHECK (decision IN ('approve','request_changes','quarantine','supersede')); reviewer text; reason text DEFAULT ''; created_at)
- migrations/00080_publishing.sql (architecture §5 reproducibility receipt + §5 processing environments):
    processing_environments (id identity PK; created_at; python_version text; os_image_or_container_digest text; lock_hash text; critical_packages_json jsonb DEFAULT '{}'; docker_image_digest text; entrypoint_command text; notes text)
    published_results (id uuid PK DEFAULT gen_random_uuid(); run_id text REFERENCES runs(id); manifest_hash text; raw_file_hashes_json jsonb DEFAULT '[]'; parameter_version_ids_json jsonb DEFAULT '{}'; processing_git_sha text; processing_dirty_tree boolean DEFAULT false; processing_environment_id bigint REFERENCES processing_environments(id); processing_command text; parser_version text; instrument_software_version text; random_seed text; review_decision_id bigint REFERENCES review_decisions(id); superseded_by uuid; published_by text; published_at timestamptz NOT NULL DEFAULT now())
    published_artifacts (id identity PK; published_result_id uuid REFERENCES published_results(id) ON DELETE CASCADE; path text; sha256 text; kind text; bytes bigint; created_at)
- migrations/00090_ops.sql (architecture §20 §21 §6 + human auth):
    llm_records (id identity PK; run_id text REFERENCES runs(id) ON DELETE CASCADE; model text; provider text; prompt_version text; input_policy text; input_hash text; output_hash text; created_at)
    notifications (id identity PK; event_type text; channel text; target text; payload_json jsonb DEFAULT '{}'; status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','sent','failed','skipped')); last_error text DEFAULT ''; created_at; sent_at timestamptz)
    backup_records (id identity PK; run_id text; scope text; status text; location text; checksum text; verified_at timestamptz; created_at)
    users (id uuid PK DEFAULT gen_random_uuid(); email text UNIQUE NOT NULL; password_hash text NOT NULL DEFAULT ''; display_name text DEFAULT ''; global_role text NOT NULL DEFAULT 'member' CHECK (global_role IN ('admin','pi','member')); status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','active','disabled')); created_at)
  (No other table FKs to users — store reviewer/published_by/performed_by as bare text.)

Add reasonable indexes (FK columns, created_at DESC where listed, run_id lookups).

Then create migrations/spine_test.go (package migrations_test) that verifies the WHOLE migration set applies:
  - import internal/testsupport and internal/db
  - func TestSpineMigratesAndHasTables(t *testing.T): pool := testsupport.NewPool(t); then query to_regclass for a representative set of new tables (samples, devices, contact_configs, sample_parameter_versions, condition_labels, parser_results, review_decisions, published_results, processing_environments, notifications, users) and fail if any is NULL.
Run it with TEST_DATABASE_URL=postgres://lab:lab@localhost:5432/labdata_test_spine?sslmode=disable

${rules(REPO + '/migrations', 'labdata_test_spine')}

IMPORTANT: your test command is exactly:  TEST_DATABASE_URL=postgres://lab:lab@localhost:5432/labdata_test_spine?sslmode=disable go test ./migrations/...`

const manifestPrompt = `Build internal/manifest: validation and canonical hashing of the manifest contract (architecture §11, §13). PURE package — depends only on internal/domain and the standard library (no DB, no platform import).

Use domain.Manifest / domain.ManifestFile / domain.ManifestArchive / domain.CompletionSource / domain.ManifestSchemaVersion (read internal/domain/manifest.go).

Create internal/manifest/manifest.go with:
- type ValidationError struct (wraps a list of field problems; implements error with a readable joined message). Provide accessors so the API layer can surface details.
- func Validate(m domain.Manifest) error  — equivalent to ValidateAt(m, time.Now()).
- func ValidateAt(m domain.Manifest, now time.Time) error  — checks:
    * SchemaVersion == domain.ManifestSchemaVersion (else reject)
    * RunID non-empty, matches ^[A-Za-z0-9._:-]{1,128}$
    * CompletionSource is domain.SourceInstrument or domain.SourceOperator
    * DeclaredBy non-empty; AgentID non-empty; MeasurementType non-empty
    * DeclaredAt not zero and not more than 24h in the future relative to now
    * MeasPath non-empty AND absolute (starts with '/')  [root-confinement is agentauth's job, not here]
    * Files non-empty; for each file: Name non-empty, contains no '/' or '\\\\' and is not '.'/'..' (manifest files are flat names within the run), Bytes >= 0, SHA256 matches ^[0-9a-f]{64}$ (lowercase hex). File Names must be unique.
    * If InstrumentJSON != "" it must equal one of the Files' Name.
    * Archive.Name non-empty; Archive.SHA256 matches ^[0-9a-f]{64}$.
    * ConditionLabels entries (if any) non-empty; ParamVersions values (if any) >= 1.
  Accumulate ALL problems into one ValidationError (don't stop at the first).
- func CanonicalJSON(m domain.Manifest) ([]byte, error)  — deterministic bytes for the same logical manifest. (Go's encoding/json marshals map keys sorted and struct fields in declaration order, so json.Marshal of the struct is deterministic; document this. Slice order — Files, ConditionLabels — is significant and preserved.)
- func Hash(m domain.Manifest) (string, error)  — lowercase hex sha256 of CanonicalJSON(m). This is the idempotency manifest_hash.

Create internal/manifest/manifest_test.go (stdlib testing + optionally stretchr/testify):
- a valid manifest passes.
- table-driven invalid cases: bad schema version, empty run_id, bad run_id charset, bad completion_source, empty declared_by/agent_id/measurement_type, zero declared_at, far-future declared_at, relative meas_path, empty files, file name with '/', file name '..', negative bytes, uppercase/short/long sha256, duplicate file names, instrument_json not in files, empty archive name, bad archive sha256, param version 0.
- Hash determinism: same manifest hashes equal; reordering Files changes the hash; building the ParamVersions map in different insertion orders yields the SAME hash (map key order independence).

${rules(REPO + '/internal/manifest', 'unused-no-db')}
Your test command:  go test ./internal/manifest/...  (no DB needed)`

const transferPrompt = `Build internal/transfer: safe archive handling, hash verification, and atomic promotion (architecture §13 "Safe archive unpacking", "Two hash layers", "Files first DB last", §5 immutable raw store). PURE/filesystem package — depends on internal/domain, stdlib (archive/tar, compress/gzip, crypto/sha256, io, os, path/filepath), and github.com/klauspost/compress/zstd. No DB, no platform import.

Create internal/transfer/transfer.go (you may split into unpack.go / verify.go / promote.go):

type UnpackOptions struct { MaxTotalBytes int64; MaxFileBytes int64; MaxEntries int }
type ExtractedFile struct { RelPath string; Bytes int64; SHA256 string }

- func VerifyArchiveSHA256(archivePath, expectedHex string) error
    Stream the file through sha256; compare case-insensitively to expectedHex; wrap a typed error (e.g. ErrArchiveHashMismatch) so the caller can map to the manifest_mismatch/transfer_failed state.
- func SafeUnpack(archivePath, destDir string, opts UnpackOptions) ([]ExtractedFile, error)
    Detect compression by extension (.tar.zst/.zst -> zstd via klauspost, .tar.gz/.tgz -> gzip, .tar -> none). Iterate tar entries and REJECT (typed ErrUnsafeArchive with detail): absolute paths; any path containing '..'; paths that escape destDir after filepath.Clean+Join (re-check the resolved path is within destDir); symlinks (tar.TypeSymlink); hardlinks (tar.TypeLink); device/char/block (TypeBlock/TypeChar); FIFO (TypeFifo); any non-regular, non-dir type; duplicate normalized paths. ENFORCE bomb guards: running total uncompressed bytes <= MaxTotalBytes, per-entry size <= MaxFileBytes, entry count <= MaxEntries. For dirs: mkdir within destDir (0755). For regular files: create parent dirs within destDir, write with 0644, and compute sha256 while streaming (use io.Copy with a limited reader to also guard against a lying header size). Return the ExtractedFiles (RelPath relative to destDir, forward-slash).
- func VerifyAgainstManifest(extracted []ExtractedFile, m domain.Manifest, allowExtra bool) error
    Build a map of extracted by RelPath. For every domain.ManifestFile: it must be present, Bytes must match, SHA256 must match (case-insensitive). If !allowExtra, any extracted file NOT listed in the manifest is rejected (architecture §13 step 10). If m.InstrumentJSON != "", require it present. Return a typed ErrManifestMismatch with which file/why.
- func Promote(stagingRunDir, rawRunDir string) error
    "Files first, DB last": atomically place the verified tree into the immutable raw store. Create the parent of rawRunDir; if rawRunDir already exists, return ErrAlreadyPromoted (caller treats as idempotent success). Otherwise os.Rename(stagingRunDir, rawRunDir) (same filesystem assumed); then make it immutable to normal users: walk the tree setting files to 0444 and dirs to 0555. Best-effort fsync of the parent directory. Provide errors.Is-able sentinels (ErrAlreadyPromoted).

Create internal/transfer/transfer_test.go (stdlib testing + testify ok) building archives IN CODE (archive/tar into a bytes.Buffer written to a temp file; for zstd wrap with klauspost zstd.NewWriter). MUST include negative tests:
- path traversal entry ('../escape'), absolute path entry ('/etc/passwd'), symlink entry, hardlink entry, fifo entry, duplicate path, total-bytes over limit, single-file over limit, entry-count over limit -> each returns an error and writes NOTHING outside destDir.
- happy path: plain .tar and .tar.zst both unpack and return correct sha256/bytes.
- VerifyArchiveSHA256: match and mismatch.
- VerifyAgainstManifest: all-match passes; missing file, byte mismatch, sha mismatch, and unexpected-extra-file (allowExtra=false) each fail; allowExtra=true tolerates extras.
- Promote: promote a staging dir, assert files exist read-only (0444) in raw with identical sha256; promoting again returns ErrAlreadyPromoted.
Use t.TempDir() for all filesystem work.

${rules(REPO + '/internal/transfer', 'unused-no-db')}
Your test command:  go test ./internal/transfer/...  (no DB needed)`

// ─────────────────────────────────────────────────────────────────────────────
// STAGE 2: DB-backed packages (need the schema present).
// ─────────────────────────────────────────────────────────────────────────────

const jobsPrompt = `Build internal/jobs: the durable Postgres jobs queue and a worker runtime (architecture §15). Depends on internal/domain, internal/db, jackc/pgx/v5, stdlib. The 'jobs' table already exists — read migrations/00030_jobs.sql for the exact columns (id, job_type, run_id, state, priority, attempt_count, max_attempts, available_at, locked_by, locked_until, idempotency_key, payload_json, result_json, last_error, created_at, started_at, finished_at). Use domain.Job, domain.JobState, domain.JobType.

Create internal/jobs/queue.go:
- type Queue struct { pool *pgxpool.Pool } ; func NewQueue(pool *pgxpool.Pool) *Queue
- type EnqueueParams struct { JobType domain.JobType; RunID *string; Priority int; MaxAttempts int; AvailableAt time.Time; IdempotencyKey string; Payload json.RawMessage }
- func (q *Queue) Enqueue(ctx, EnqueueParams) (domain.Job, error)
    INSERT ... ON CONFLICT (idempotency_key) DO NOTHING; if no row inserted, SELECT the existing job by idempotency_key and return it (IDEMPOTENT — duplicate enqueues return the same job, no duplicate work). MaxAttempts<=0 defaults to 5. Zero AvailableAt means now().
- func (q *Queue) Claim(ctx, workerID string, lockTTL time.Duration) (*domain.Job, error)
    In a transaction: SELECT id FROM jobs WHERE state='queued' AND available_at<=now() ORDER BY priority DESC, created_at ASC FOR UPDATE SKIP LOCKED LIMIT 1; if none, return (nil,nil). Else UPDATE that row SET state='running', locked_by=$worker, locked_until=now()+lockTTL, started_at=COALESCE(started_at, now()), attempt_count=attempt_count+1 RETURNING *. Commit. Return the claimed job.
- func (q *Queue) Complete(ctx, jobID int64, result json.RawMessage) error  -> state='succeeded', result_json=$, finished_at=now(), locked_by=NULL, locked_until=NULL.
- func (q *Queue) Fail(ctx, jobID int64, errMsg string) (dead bool, err error)
    Load attempt_count, max_attempts. If attempt_count>=max_attempts: state='dead', last_error=$, finished_at=now(), locked_*=NULL, return dead=true. Else: state='queued', available_at=now()+backoff(attempt_count), last_error=$, locked_*=NULL, return dead=false.
- func backoff(attempt int) time.Duration  — exponential with a cap (e.g. min(2^attempt seconds, 5m)).
- func (q *Queue) ReclaimStale(ctx) (int, error)  — find jobs WHERE state='running' AND locked_until<now(); for each, apply the same retry-or-dead policy as Fail (reason 'lock expired'). Return count.
- func (q *Queue) Get(ctx, jobID int64) (domain.Job, error)  — helper for tests/inspection.

Create internal/jobs/worker.go:
- type Handler func(ctx context.Context, job domain.Job) (json.RawMessage, error)
- type WorkerOptions struct { PollInterval time.Duration; LockTTL time.Duration; ReclaimInterval time.Duration }
- type Worker struct {...} ; func NewWorker(q *Queue, workerID string, handlers map[domain.JobType]Handler, opts WorkerOptions) *Worker
- func (w *Worker) Run(ctx context.Context) error  — loop until ctx done: claim a job; if nil, sleep PollInterval; else dispatch by job_type (unknown type -> Fail with a clear error), on handler success Complete with the returned result, on error Fail. Periodically (ReclaimInterval) call ReclaimStale. Respect context cancellation promptly. Apply sane defaults for zero options.

Create internal/jobs/queue_test.go using internal/testsupport.NewPool(t) (DB-backed). Truncate the jobs table between tests (testsupport.Truncate). Enqueue with RunID=nil to avoid needing a runs row (run_id is nullable). Tests:
- Enqueue idempotency: same IdempotencyKey twice -> one row, identical id.
- Claim returns highest priority then oldest; sets state=running, locked_by, attempt_count=1.
- A second Claim when only one queued job exists returns (nil,nil) after the first claim (it's now running).
- Complete -> succeeded with result.
- Fail below max -> requeued with available_at in the future and incremented-attempt semantics; Fail at max -> dead.
- ReclaimStale: manually set a job to running with locked_until in the past, then ReclaimStale requeues/deads it.
- (Optional) a Worker run test: enqueue a job, run Worker in a goroutine with a short cancel, assert the handler ran and the job is succeeded.
Run with TEST_DATABASE_URL=postgres://lab:lab@localhost:5432/labdata_test_jobs?sslmode=disable

${rules(REPO + '/internal/jobs', 'labdata_test_jobs')}
Your test command:  TEST_DATABASE_URL=postgres://lab:lab@localhost:5432/labdata_test_jobs?sslmode=disable go test ./internal/jobs/...`

const statemachinePrompt = `Build internal/statemachine: the ONLY writer of runs.state, with legality checks and an audit row per transition (architecture §14, §16). Depends on internal/domain, internal/db, pgx, stdlib. Read migrations/00020_runs.sql (runs + run_state_transitions columns) and internal/domain/state.go (RunState constants), types.go (RunStateTransition, ActorType).

Create internal/statemachine/transitions.go (PURE):
- var allowed = map[domain.RunState][]domain.RunState{...} encoding the §14 graph. Include the happy path AND sensible recovery edges:
    declared -> pulling, transfer_failed
    pulling -> unpacked, transfer_failed
    unpacked -> verified, unsafe_archive, manifest_mismatch
    verified -> promoted, manifest_mismatch
    promoted -> needs_metadata, parsing
    needs_metadata -> parsing
    parsing -> validated, parser_failed, quarantined
    validated -> processing
    processing -> awaiting_review, processing_failed
    awaiting_review -> approved, changes_requested, quarantined, review_rejected
    changes_requested -> processing, awaiting_review
    approved -> publishing
    publishing -> published, processing_failed
    transfer_failed -> pulling            (re-drive)
    manifest_mismatch -> pulling           (re-pull)
    parser_failed -> parsing               (re-drive after fix)
    processing_failed -> processing
    quarantined -> parsing, processing     (after manual clearing)
  published, unsafe_archive, review_rejected are terminal (no outgoing edges) — document this.
- func CanTransition(from, to domain.RunState) bool
- func NextStates(from domain.RunState) []domain.RunState

Create internal/statemachine/statemachine.go (DB):
- typed errors: ErrUnknownState, ErrIllegalTransition, ErrStateMismatch (exported, errors.Is-able), each carrying from/to/expected for the API layer to map (409 for mismatch, 422 for illegal).
- type TransitionParams struct { RunID string; ExpectedFrom domain.RunState; To domain.RunState; ActorType domain.ActorType; ActorID string; Reason string; Payload json.RawMessage }  (ExpectedFrom == "" means "don't check, use current").
- func CurrentState(ctx context.Context, q db.DBTX, runID string) (domain.RunState, error)
- func Transition(ctx context.Context, pool *pgxpool.Pool, p TransitionParams) (domain.RunStateTransition, error)
    Open a tx. SELECT state FROM runs WHERE id=$1 FOR UPDATE (row lock; if no row -> not found error). If p.ExpectedFrom != "" and current != ExpectedFrom -> ErrStateMismatch (rollback, write nothing). If !CanTransition(current, p.To) -> ErrIllegalTransition (rollback). UPDATE runs SET state=p.To, updated_at=now() WHERE id=$1. INSERT INTO run_state_transitions (run_id, from_state, to_state, actor_type, actor_id, reason, payload_json) VALUES (...) RETURNING the row. Commit. Return the transition row. Default Payload to '{}' when nil.
  Also provide func TransitionTx(ctx, tx pgx.Tx, p) so callers already in a tx can compose (optional but preferred — implement Transition by managing its own tx and TransitionTx for the shared-tx case).

Create internal/statemachine/statemachine_test.go:
- PURE tests: CanTransition for representative legal and illegal pairs; terminal states have no NextStates.
- DB tests via internal/testsupport.NewPool(t): seed prerequisites — INSERT an agent (agents requires key_hash; any non-empty string is fine for the FK), then INSERT a run in state 'declared' (runs.agent_id REFERENCES agents(id); set declared_at, manifest_hash, meas_path, completion_source). Provide a seedRun(t, pool) helper. Then:
    * happy transition declared->pulling writes a transition row and updates runs.state; the returned row has correct from/to.
    * ExpectedFrom mismatch (e.g. expected 'verified' when current is 'declared') -> ErrStateMismatch and NO transition row / NO state change.
    * illegal transition (declared->published) -> ErrIllegalTransition and no change.
    * a second legal transition appends a second audit row.
  Truncate runs, run_state_transitions, agents between tests.
Run with TEST_DATABASE_URL=postgres://lab:lab@localhost:5432/labdata_test_sm?sslmode=disable

${rules(REPO + '/internal/statemachine', 'labdata_test_sm')}
Your test command:  TEST_DATABASE_URL=postgres://lab:lab@localhost:5432/labdata_test_sm?sslmode=disable go test ./internal/statemachine/...`

const agentauthPrompt = `Build internal/agentauth: authenticate measurement-PC agents and confine a manifest meas_path to an agent's allowed roots (architecture §4, §13 step 3). Depends on internal/domain, internal/db, pgx, golang.org/x/crypto/bcrypt, stdlib (path/filepath). Read migrations/00010_agents.sql (agents, agent_allowed_roots columns) and domain.Agent / domain.AgentAllowedRoot.

Create internal/agentauth/pathvalidate.go (PURE — the security-critical part):
- func ResolveUnderRoot(measPath string, roots []string) (matchedRoot string, ok bool)
    measPath MUST be absolute. Clean it. For each root: Clean(root); it matches iff cleanedMeasPath == cleanedRoot OR cleanedMeasPath has prefix cleanedRoot+string(os.PathSeparator). This MUST reject the prefix-attack ('/data/runs' must NOT match root '/data/run'). Reject any measPath containing a '..' element or that is not absolute.
- func ValidateMeasPath(measPath string, roots []string) error  — returns a typed ErrPathNotAllowed (exported, errors.Is-able) when ResolveUnderRoot is false or measPath is relative/contains traversal.

Create internal/agentauth/agentauth.go (DB):
- typed errors ErrAgentUnknown, ErrAgentDisabled, ErrBadKey (exported) — Authenticate returns one of these; the API layer maps all of them to 401 without leaking which (log detail server-side).
- func HashKey(rawKey string) (string, error)  — bcrypt.GenerateFromPassword (default cost). Used to register agents and in tests.
- type Service struct { pool *pgxpool.Pool } ; func NewService(pool *pgxpool.Pool) *Service
- func (s *Service) Authenticate(ctx, agentID, providedKey string) (*domain.Agent, error)
    Load agent by id (ErrAgentUnknown if missing). If !enabled -> ErrAgentDisabled. bcrypt.CompareHashAndPassword(key_hash, providedKey); mismatch -> ErrBadKey. On success, best-effort UPDATE agents SET last_seen_at=now() (ignore its error), and return the agent (never expose key_hash to callers beyond the struct field).
    NOTE: to avoid a user-enumeration timing oracle, when the agent is unknown still perform a bcrypt compare against a dummy hash before returning ErrAgentUnknown.
- func (s *Service) AllowedRoots(ctx, agentID string) ([]string, error)
- func (s *Service) ValidateMeasPath(ctx, agentID, measPath string) error  — load roots, delegate to the pure ValidateMeasPath; if the agent has no roots configured, reject (fail closed).

Create internal/agentauth/agentauth_test.go:
- PURE path tests: exact root match; subdir match; prefix-attack rejected ('/data/runs-evil' vs root '/data/runs'); traversal '/data/runs/../../etc' rejected; relative path rejected; empty roots rejected.
- DB tests via internal/testsupport.NewPool(t): register an agent (INSERT agents with HashKey output + enabled=true) and some agent_allowed_roots; Authenticate succeeds with the right key; wrong key -> ErrBadKey; unknown id -> ErrAgentUnknown; disabled agent -> ErrAgentDisabled; ValidateMeasPath integration (allowed vs disallowed). Truncate agents, agent_allowed_roots between tests.
Run with TEST_DATABASE_URL=postgres://lab:lab@localhost:5432/labdata_test_agentauth?sslmode=disable

${rules(REPO + '/internal/agentauth', 'labdata_test_agentauth')}
Your test command:  TEST_DATABASE_URL=postgres://lab:lab@localhost:5432/labdata_test_agentauth?sslmode=disable go test ./internal/agentauth/...`

// ─────────────────────────────────────────────────────────────────────────────
// Run it.
// ─────────────────────────────────────────────────────────────────────────────

phase('Schema+Pure')
log('Stage 1: migrations spine + manifest + transfer (parallel)')
const stage1 = await parallel([
  () => agent(migrationsPrompt, { phase: 'Schema+Pure', label: 'migrations-spine', model: 'sonnet', schema: SUMMARY }),
  () => agent(manifestPrompt,   { phase: 'Schema+Pure', label: 'manifest',        model: 'sonnet', schema: SUMMARY }),
  () => agent(transferPrompt,   { phase: 'Schema+Pure', label: 'transfer',        model: 'sonnet', schema: SUMMARY }),
])

phase('DB modules')
log('Stage 2: jobs + statemachine + agentauth (parallel, against the validated schema)')
const stage2 = await parallel([
  () => agent(jobsPrompt,         { phase: 'DB modules', label: 'jobs',         model: 'sonnet', schema: SUMMARY }),
  () => agent(statemachinePrompt, { phase: 'DB modules', label: 'statemachine', model: 'sonnet', schema: SUMMARY }),
  () => agent(agentauthPrompt,    { phase: 'DB modules', label: 'agentauth',    model: 'sonnet', schema: SUMMARY }),
])

return { stage1: stage1.filter(Boolean), stage2: stage2.filter(Boolean) }
