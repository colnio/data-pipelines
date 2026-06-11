package review_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/colnio/data-pipelines/internal/domain"
	"github.com/colnio/data-pipelines/internal/jobs"
	"github.com/colnio/data-pipelines/internal/platform"
	"github.com/colnio/data-pipelines/internal/review"
	"github.com/colnio/data-pipelines/internal/run"
	"github.com/colnio/data-pipelines/internal/statemachine"
	"github.com/colnio/data-pipelines/internal/testsupport"
)

// discardLogger returns a logger that discards all output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// truncateAll clears all tables the review tests write.
func truncateAll(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	testsupport.Truncate(t, pool,
		"published_artifacts",
		"published_results",
		"review_decisions",
		"review_artifacts",
		"analysis_artifacts",
		"parser_results",
		"run_state_transitions",
		"run_files",
		"manifests",
		"jobs",
		"runs",
		"agents",
	)
}

// seedRun inserts an agent, a run in awaiting_review state, a manifest,
// a review_artifact, a parser_results row, an analysis_artifact, and a
// run_file. Returns the run_id.
func seedRun(t *testing.T, pool *pgxpool.Pool, ctx context.Context) string {
	t.Helper()

	runID := "run-review-" + uuid.NewString()[:8]

	// Agent
	_, err := pool.Exec(ctx,
		`INSERT INTO agents (id, key_hash, enabled) VALUES ('agent-review-test','x',true)
		 ON CONFLICT DO NOTHING`)
	require.NoError(t, err)

	// Run in state 'awaiting_review' — set directly for test seeding.
	_, err = pool.Exec(ctx, `
		INSERT INTO runs (id, manifest_hash, agent_id, measurement_type, completion_source,
		                  meas_path, operator_comment, state, declared_by, declared_at)
		VALUES ($1,'mhash-1','agent-review-test','iv','instrument','/data','',
		        'awaiting_review','tester',now())`,
		runID)
	require.NoError(t, err)

	// Manifest row (required by the publish path).
	manifest := domain.Manifest{
		RunID:         runID,
		SchemaVersion: 1,
		AgentID:       "agent-review-test",
		ParamVersions: map[string]int{"stack": 1},
	}
	manifestJSON, err := json.Marshal(manifest)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO manifests (run_id, manifest_hash, schema_version, completion_source,
		                       declared_by, declared_at, raw_json)
		VALUES ($1,'mhash-1',1,'instrument','tester',now(),$2)`,
		runID, manifestJSON)
	require.NoError(t, err)

	// review_artifact
	_, err = pool.Exec(ctx, `
		INSERT INTO review_artifacts (run_id, version, metrics_json, parser_warnings_json, llm_summary)
		VALUES ($1, 1, '{"key":"val"}', '[]', 'all good')`,
		runID)
	require.NoError(t, err)

	// parser_results
	_, err = pool.Exec(ctx, `
		INSERT INTO parser_results (run_id, parser_version, status)
		VALUES ($1, 'v1.2.3', 'success')`,
		runID)
	require.NoError(t, err)

	// analysis_artifacts
	_, err = pool.Exec(ctx, `
		INSERT INTO analysis_artifacts (run_id, kind, path, sha256)
		VALUES ($1, 'plot', '/out/fig1.png', 'abc123')`,
		runID)
	require.NoError(t, err)

	// run_files
	_, err = pool.Exec(ctx, `
		INSERT INTO run_files (run_id, name, bytes, sha256)
		VALUES ($1, 'iv.data', 100, 'deadbeef')`,
		runID)
	require.NoError(t, err)

	return runID
}

// principalCtx returns a context that has the given principal stored in it,
// so platform.PrincipalFrom(ctx) returns it successfully.
func principalCtx(p *platform.Principal) context.Context {
	return platform.WithPrincipal(context.Background(), p)
}

// makePrincipal creates a test principal with ViaTokenID set (scope-enforced).
func makePrincipal(email string, scopes ...string) *platform.Principal {
	tokenID := uuid.New()
	return &platform.Principal{
		UserID:     uuid.New(),
		Email:      email,
		GlobalRole: "pi",
		ViaTokenID: &tokenID,
		Scopes:     scopes,
	}
}

// ── Test: approve → approved, decision row, publish job enqueued ─────────────

func TestApprove_HappyPath(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	truncateAll(t, pool)

	runID := seedRun(t, pool, ctx)

	queue := jobs.NewQueue(pool)
	runs := run.NewRepo(pool)
	svc := review.NewService(pool, runs, queue, discardLogger())

	_, api := humatest.New(t)
	review.Register(api, svc)

	pCtx := principalCtx(makePrincipal("reviewer@lab.example", platform.ScopeWriteReviews))
	resp := api.PostCtx(pCtx, "/v1/reviews/"+runID+"/approve", map[string]any{})
	require.Equal(t, http.StatusOK, resp.Code, "body: %s", resp.Body.String())

	// Run state is approved.
	state, err := statemachine.CurrentState(ctx, pool, runID)
	require.NoError(t, err)
	assert.Equal(t, domain.StateApproved, state)

	// review_decisions row exists with decision='approve'.
	var cnt int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM review_decisions WHERE run_id=$1 AND decision='approve'`, runID).Scan(&cnt))
	assert.Equal(t, 1, cnt)

	// publish_run job was enqueued.
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM jobs WHERE run_id=$1 AND job_type='publish_run'`, runID).Scan(&cnt))
	assert.Equal(t, 1, cnt)
}

// ── Test: approve is idempotent for the job enqueue ──────────────────────────

func TestApprove_JobEnqueueIdempotent(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	truncateAll(t, pool)

	runID := seedRun(t, pool, ctx)

	queue := jobs.NewQueue(pool)
	runs := run.NewRepo(pool)
	svc := review.NewService(pool, runs, queue, discardLogger())

	// Manually enqueue the job first (simulating a prior partial run).
	runIDCopy := runID
	_, err := queue.Enqueue(ctx, jobs.EnqueueParams{
		JobType:        domain.JobPublishRun,
		RunID:          &runIDCopy,
		IdempotencyKey: "publish:" + runID,
	})
	require.NoError(t, err)

	_, api := humatest.New(t)
	review.Register(api, svc)

	pCtx := principalCtx(makePrincipal("reviewer@lab.example", platform.ScopeWriteReviews))
	resp := api.PostCtx(pCtx, "/v1/reviews/"+runID+"/approve", map[string]any{})
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	// Still exactly one publish_run job.
	var cnt int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM jobs WHERE run_id=$1 AND job_type='publish_run'`, runID).Scan(&cnt))
	assert.Equal(t, 1, cnt, "second enqueue with same idempotency key must not create a second job")
}

// ── Test: request-changes → changes_requested + decision row ─────────────────

func TestRequestChanges_HappyPath(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	truncateAll(t, pool)

	runID := seedRun(t, pool, ctx)

	queue := jobs.NewQueue(pool)
	runs := run.NewRepo(pool)
	svc := review.NewService(pool, runs, queue, discardLogger())

	_, api := humatest.New(t)
	review.Register(api, svc)

	pCtx := principalCtx(makePrincipal("reviewer@lab.example", platform.ScopeWriteReviews))
	resp := api.PostCtx(pCtx, "/v1/reviews/"+runID+"/request-changes",
		map[string]any{"reason": "plots look wrong"})
	require.Equal(t, http.StatusOK, resp.Code, "body: %s", resp.Body.String())

	state, err := statemachine.CurrentState(ctx, pool, runID)
	require.NoError(t, err)
	assert.Equal(t, domain.StateChangesRequested, state)

	var cnt int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM review_decisions WHERE run_id=$1 AND decision='request_changes'`,
		runID).Scan(&cnt))
	assert.Equal(t, 1, cnt)
}

// ── Test: quarantine → quarantined + decision row ────────────────────────────

func TestQuarantine_HappyPath(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	truncateAll(t, pool)

	runID := seedRun(t, pool, ctx)

	queue := jobs.NewQueue(pool)
	runs := run.NewRepo(pool)
	svc := review.NewService(pool, runs, queue, discardLogger())

	_, api := humatest.New(t)
	review.Register(api, svc)

	pCtx := principalCtx(makePrincipal("reviewer@lab.example", platform.ScopeWriteReviews))
	resp := api.PostCtx(pCtx, "/v1/reviews/"+runID+"/quarantine",
		map[string]any{"reason": "suspicious data"})
	require.Equal(t, http.StatusOK, resp.Code, "body: %s", resp.Body.String())

	state, err := statemachine.CurrentState(ctx, pool, runID)
	require.NoError(t, err)
	assert.Equal(t, domain.StateQuarantined, state)

	var cnt int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM review_decisions WHERE run_id=$1 AND decision='quarantine'`,
		runID).Scan(&cnt))
	assert.Equal(t, 1, cnt)
}

// ── Test: handlePublish → published_results + published_artifacts, idempotent ─

func TestHandlePublish_HappyPath(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	truncateAll(t, pool)

	runID := seedRun(t, pool, ctx)

	queue := jobs.NewQueue(pool)
	runs := run.NewRepo(pool)
	svc := review.NewService(pool, runs, queue, discardLogger())

	// Drive the run to approved via the approve handler.
	_, api := humatest.New(t)
	review.Register(api, svc)

	pCtx := principalCtx(makePrincipal("reviewer@lab.example", platform.ScopeWriteReviews))
	resp := api.PostCtx(pCtx, "/v1/reviews/"+runID+"/approve", map[string]any{})
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	// Call the publish handler directly.
	handlers := svc.Handlers()
	handler, ok := handlers[domain.JobPublishRun]
	require.True(t, ok, "publish_run handler must be registered in Handlers()")

	result, err := handler(ctx, domain.Job{
		ID:             999,
		JobType:        domain.JobPublishRun,
		RunID:          &runID,
		IdempotencyKey: "publish:" + runID,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, result)

	// Run should be published.
	state, err := statemachine.CurrentState(ctx, pool, runID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatePublished, state)

	// published_results row exists.
	var cnt int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM published_results WHERE run_id=$1`, runID).Scan(&cnt))
	assert.Equal(t, 1, cnt)

	// published_artifacts row copied from analysis_artifacts.
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM published_artifacts pa
		JOIN published_results pr ON pr.id = pa.published_result_id
		WHERE pr.run_id = $1`, runID).Scan(&cnt))
	assert.Equal(t, 1, cnt, "one analysis_artifact should yield one published_artifact")

	// Receipt references the correct reviewer.
	var publishedBy string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT published_by FROM published_results WHERE run_id=$1`, runID).Scan(&publishedBy))
	assert.Equal(t, "reviewer@lab.example", publishedBy)

	// ── Idempotency: calling again is a no-op ────────────────────────────────
	result2, err := handler(ctx, domain.Job{
		ID:             1000,
		JobType:        domain.JobPublishRun,
		RunID:          &runID,
		IdempotencyKey: "publish:" + runID,
	})
	require.NoError(t, err)

	var res2 struct {
		AlreadyPublished bool `json:"already_published"`
	}
	require.NoError(t, json.Unmarshal(result2, &res2))
	assert.True(t, res2.AlreadyPublished, "second call must report already_published=true")

	// Still exactly one published_results row.
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM published_results WHERE run_id=$1`, runID).Scan(&cnt))
	assert.Equal(t, 1, cnt, "second publish call must not create a second receipt")
}

// ── Test: GET /v1/reviews lists awaiting_review runs ─────────────────────────

func TestHandleList_ReturnsAwaitingReview(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	truncateAll(t, pool)

	runID := seedRun(t, pool, ctx)

	queue := jobs.NewQueue(pool)
	runs := run.NewRepo(pool)
	svc := review.NewService(pool, runs, queue, discardLogger())

	_, api := humatest.New(t)
	review.Register(api, svc)

	pCtx := principalCtx(makePrincipal("reviewer@lab.example", platform.ScopeReadReviews))
	resp := api.GetCtx(pCtx, "/v1/reviews")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	var out struct {
		Reviews []struct {
			Run            domain.Run `json:"run"`
			LatestArtifact *struct {
				LLMSummary string `json:"llm_summary"`
			} `json:"latest_artifact"`
		} `json:"reviews"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	require.Len(t, out.Reviews, 1)
	assert.Equal(t, runID, out.Reviews[0].Run.ID)
	require.NotNil(t, out.Reviews[0].LatestArtifact)
	assert.Equal(t, "all good", out.Reviews[0].LatestArtifact.LLMSummary)
}

// ── Test: GET /v1/reviews/{run_id} returns bundle with artifact ───────────────

func TestHandleGet_ReturnsBundleWithArtifact(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	truncateAll(t, pool)

	runID := seedRun(t, pool, ctx)

	queue := jobs.NewQueue(pool)
	runs := run.NewRepo(pool)
	svc := review.NewService(pool, runs, queue, discardLogger())

	_, api := humatest.New(t)
	review.Register(api, svc)

	pCtx := principalCtx(makePrincipal("reviewer@lab.example", platform.ScopeReadReviews))
	resp := api.GetCtx(pCtx, "/v1/reviews/"+runID)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	var out struct {
		Run            domain.Run `json:"run"`
		LatestArtifact *struct {
			LLMSummary string `json:"llm_summary"`
		} `json:"latest_artifact"`
		Files       []domain.RunFile            `json:"files"`
		Transitions []domain.RunStateTransition `json:"transitions"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	assert.Equal(t, runID, out.Run.ID)
	require.NotNil(t, out.LatestArtifact)
	assert.Equal(t, "all good", out.LatestArtifact.LLMSummary)
	assert.Len(t, out.Files, 1)
}

// ── Test: GET /v1/reviews/{run_id} → 404 for unknown run ─────────────────────

func TestHandleGet_NotFound(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	_ = ctx
	truncateAll(t, pool)

	queue := jobs.NewQueue(pool)
	runs := run.NewRepo(pool)
	svc := review.NewService(pool, runs, queue, discardLogger())

	_, api := humatest.New(t)
	review.Register(api, svc)

	pCtx := principalCtx(makePrincipal("reviewer@lab.example", platform.ScopeReadReviews))
	resp := api.GetCtx(pCtx, "/v1/reviews/no-such-run")
	assert.Equal(t, http.StatusNotFound, resp.Code)
}

// ── Test: PATCH metadata updates operator_comment, does not change state ──────

func TestPatchMetadata_UpdatesComment(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	truncateAll(t, pool)

	runID := seedRun(t, pool, ctx)

	queue := jobs.NewQueue(pool)
	runs := run.NewRepo(pool)
	svc := review.NewService(pool, runs, queue, discardLogger())

	_, api := humatest.New(t)
	review.Register(api, svc)

	pCtx := principalCtx(makePrincipal("reviewer@lab.example", platform.ScopeWriteReviews))
	resp := api.PatchCtx(pCtx, "/v1/reviews/"+runID+"/metadata",
		map[string]any{"operator_comment": "fixed the label"})
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	// State must NOT have changed.
	state, err := statemachine.CurrentState(ctx, pool, runID)
	require.NoError(t, err)
	assert.Equal(t, domain.StateAwaitingReview, state)

	// operator_comment must be updated.
	r, err := runs.Get(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, "fixed the label", r.OperatorComment)
}

// ── Test: unauthenticated requests get 401 ───────────────────────────────────

func TestHandlers_Unauthenticated(t *testing.T) {
	pool := testsupport.NewPool(t)
	truncateAll(t, pool)

	queue := jobs.NewQueue(pool)
	runs := run.NewRepo(pool)
	svc := review.NewService(pool, runs, queue, discardLogger())

	_, api := humatest.New(t)
	review.Register(api, svc)

	cases := []struct {
		method string
		path   string
		body   any
	}{
		{"GET", "/v1/reviews", nil},
		{"GET", "/v1/reviews/any-id", nil},
		{"POST", "/v1/reviews/any-id/approve", map[string]any{}},
		{"POST", "/v1/reviews/any-id/request-changes", map[string]any{"reason": "x"}},
		{"POST", "/v1/reviews/any-id/quarantine", map[string]any{"reason": "x"}},
		{"PATCH", "/v1/reviews/any-id/metadata", map[string]any{"operator_comment": "x"}},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			var resp *http.Response
			// context.Background() has no principal → 401.
			emptyCtx := context.Background()
			switch tc.method {
			case "GET":
				resp = api.GetCtx(emptyCtx, tc.path).Result()
			case "POST":
				resp = api.PostCtx(emptyCtx, tc.path, tc.body).Result()
			case "PATCH":
				resp = api.PatchCtx(emptyCtx, tc.path, tc.body).Result()
			}
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
				"expected 401 for unauthenticated %s %s", tc.method, tc.path)
		})
	}
}
