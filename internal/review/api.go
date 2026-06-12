package review

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/colnio/data-pipelines/internal/domain"
	"github.com/colnio/data-pipelines/internal/jobs"
	"github.com/colnio/data-pipelines/internal/notify"
	"github.com/colnio/data-pipelines/internal/platform"
	"github.com/colnio/data-pipelines/internal/run"
	"github.com/colnio/data-pipelines/internal/statemachine"
)

// Register wires all review endpoints onto the huma API.
func Register(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "reviews-list",
		Method:      http.MethodGet,
		Path:        "/v1/reviews",
		Summary:     "List runs awaiting review",
		Description: "Returns runs in state awaiting_review with their latest review_artifact summary.",
		Tags:        []string{"reviews"},
	}, svc.handleList)

	huma.Register(api, huma.Operation{
		OperationID: "reviews-get",
		Method:      http.MethodGet,
		Path:        "/v1/reviews/{run_id}",
		Summary:     "Get review bundle",
		Description: "Returns the run, latest review_artifact, files, and transitions.",
		Tags:        []string{"reviews"},
	}, svc.handleGet)

	huma.Register(api, huma.Operation{
		OperationID: "reviews-approve",
		Method:      http.MethodPost,
		Path:        "/v1/reviews/{run_id}/approve",
		Summary:     "Approve a run",
		Description: "Transitions awaiting_review→approved, records decision, and enqueues publish_run.",
		Tags:        []string{"reviews"},
	}, svc.handleApprove)

	huma.Register(api, huma.Operation{
		OperationID: "reviews-request-changes",
		Method:      http.MethodPost,
		Path:        "/v1/reviews/{run_id}/request-changes",
		Summary:     "Request changes on a run",
		Description: "Transitions awaiting_review→changes_requested and records decision with reason.",
		Tags:        []string{"reviews"},
	}, svc.handleRequestChanges)

	huma.Register(api, huma.Operation{
		OperationID: "reviews-quarantine",
		Method:      http.MethodPost,
		Path:        "/v1/reviews/{run_id}/quarantine",
		Summary:     "Quarantine a run",
		Description: "Transitions awaiting_review→quarantined and records decision with reason.",
		Tags:        []string{"reviews"},
	}, svc.handleQuarantine)

	huma.Register(api, huma.Operation{
		OperationID: "reviews-patch-metadata",
		Method:      http.MethodPatch,
		Path:        "/v1/reviews/{run_id}/metadata",
		Summary:     "Correct operator comment",
		Description: "Identity/display correction (§9): updates runs.operator_comment without changing state.",
		Tags:        []string{"reviews"},
	}, svc.handlePatchMetadata)
}

// ── reviewArtifactSummary holds the subset of review_artifacts returned in listings.
type reviewArtifactSummary struct {
	ID                 int64           `json:"id"`
	Version            int             `json:"version"`
	MetricsJSON        json.RawMessage `json:"metrics_json"`
	ParserWarningsJSON json.RawMessage `json:"parser_warnings_json"`
	LLMSummary         string          `json:"llm_summary"`
	CreatedAt          time.Time       `json:"created_at"`
}

// reviewArtifactFull is the complete review_artifact for the detail view.
type reviewArtifactFull struct {
	ID                 int64           `json:"id"`
	RunID              string          `json:"run_id"`
	Version            int             `json:"version"`
	SummaryJSON        json.RawMessage `json:"summary_json"`
	PlotsJSON          json.RawMessage `json:"plots_json"`
	MetricsJSON        json.RawMessage `json:"metrics_json"`
	ParserWarningsJSON json.RawMessage `json:"parser_warnings_json"`
	LLMSummary         string          `json:"llm_summary"`
	CreatedAt          time.Time       `json:"created_at"`
}

// ── List ─────────────────────────────────────────────────────────────────────

type reviewListInput struct {
	Limit int `query:"limit"`
}

type reviewListItem struct {
	Run            domain.Run             `json:"run"`
	LatestArtifact *reviewArtifactSummary `json:"latest_artifact,omitempty"`
}

type reviewListOutput struct {
	Body struct {
		Reviews []reviewListItem `json:"reviews"`
	}
}

func (s *Service) handleList(ctx context.Context, in *reviewListInput) (*reviewListOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequireScope(p, platform.ScopeReadReviews); err != nil {
		return nil, err
	}

	limit := in.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	runs, _, err := s.runs.List(ctx, run.ListFilter{
		State: string(domain.StateAwaitingReview),
		Limit: limit,
	})
	if err != nil {
		return nil, err
	}

	items := make([]reviewListItem, 0, len(runs))
	for _, r := range runs {
		item := reviewListItem{Run: r}
		artifact, err := s.latestArtifactSummary(ctx, r.ID)
		if err == nil {
			item.LatestArtifact = artifact
		}
		items = append(items, item)
	}

	out := &reviewListOutput{}
	out.Body.Reviews = items
	return out, nil
}

// ── Get ──────────────────────────────────────────────────────────────────────

type reviewGetInput struct {
	RunID string `path:"run_id"`
}

type reviewGetOutput struct {
	Body struct {
		Run            domain.Run                  `json:"run"`
		LatestArtifact *reviewArtifactFull         `json:"latest_artifact,omitempty"`
		Files          []domain.RunFile            `json:"files"`
		Transitions    []domain.RunStateTransition `json:"transitions"`
	}
}

func (s *Service) handleGet(ctx context.Context, in *reviewGetInput) (*reviewGetOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequireScope(p, platform.ScopeReadReviews); err != nil {
		return nil, err
	}

	r, err := s.runs.Get(ctx, in.RunID)
	if errors.Is(err, run.ErrNotFound) {
		return nil, platform.NotFound("run.not_found", "run does not exist")
	}
	if err != nil {
		return nil, mapSMErr(err)
	}

	files, err := s.runs.Files(ctx, in.RunID)
	if err != nil {
		return nil, err
	}

	transitions, err := s.runs.Transitions(ctx, in.RunID)
	if err != nil {
		return nil, err
	}

	artifact, _ := s.latestArtifactFull(ctx, in.RunID) // nil is fine

	out := &reviewGetOutput{}
	out.Body.Run = r
	out.Body.Files = files
	out.Body.Transitions = transitions
	out.Body.LatestArtifact = artifact
	return out, nil
}

// ── Approve ──────────────────────────────────────────────────────────────────

type reviewApproveInput struct {
	RunID string `path:"run_id"`
}

type reviewApproveOutput struct {
	Body struct {
		RunID    string `json:"run_id"`
		State    string `json:"state"`
		JobID    int64  `json:"job_id"`
		Decision int64  `json:"decision_id"`
	}
}

func (s *Service) handleApprove(ctx context.Context, in *reviewApproveInput) (*reviewApproveOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequireScope(p, platform.ScopeWriteReviews); err != nil {
		return nil, err
	}

	reviewer := p.Email

	_, err := statemachine.Transition(ctx, s.pool, statemachine.TransitionParams{
		RunID:        in.RunID,
		ExpectedFrom: domain.StateAwaitingReview,
		To:           domain.StateApproved,
		ActorType:    domain.ActorReviewer,
		ActorID:      reviewer,
		Reason:       "approved by reviewer",
	})
	if err != nil {
		return nil, mapSMErr(err)
	}

	// Record the review decision.
	decisionID, err := s.insertDecision(ctx, in.RunID, "approve", reviewer, "")
	if err != nil {
		return nil, err
	}

	// Enqueue publish_run (idempotent).
	runIDCopy := in.RunID
	job, err := s.queue.Enqueue(ctx, jobs.EnqueueParams{
		JobType:        domain.JobPublishRun,
		RunID:          &runIDCopy,
		Priority:       5,
		IdempotencyKey: "publish:" + in.RunID,
		Payload:        json.RawMessage(`{}`),
	})
	if err != nil {
		return nil, err
	}

	out := &reviewApproveOutput{}
	out.Body.RunID = in.RunID
	out.Body.State = string(domain.StateApproved)
	out.Body.JobID = job.ID
	out.Body.Decision = decisionID
	return out, nil
}

// ── Request changes ──────────────────────────────────────────────────────────

type reviewRequestChangesInput struct {
	RunID string `path:"run_id"`
	Body  struct {
		Reason string `json:"reason"`
	}
}

type reviewRequestChangesOutput struct {
	Body struct {
		RunID      string `json:"run_id"`
		State      string `json:"state"`
		DecisionID int64  `json:"decision_id"`
	}
}

func (s *Service) handleRequestChanges(ctx context.Context, in *reviewRequestChangesInput) (*reviewRequestChangesOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequireScope(p, platform.ScopeWriteReviews); err != nil {
		return nil, err
	}

	reviewer := p.Email

	_, err := statemachine.Transition(ctx, s.pool, statemachine.TransitionParams{
		RunID:        in.RunID,
		ExpectedFrom: domain.StateAwaitingReview,
		To:           domain.StateChangesRequested,
		ActorType:    domain.ActorReviewer,
		ActorID:      reviewer,
		Reason:       in.Body.Reason,
	})
	if err != nil {
		return nil, mapSMErr(err)
	}

	decisionID, err := s.insertDecision(ctx, in.RunID, "request_changes", reviewer, in.Body.Reason)
	if err != nil {
		return nil, err
	}

	out := &reviewRequestChangesOutput{}
	out.Body.RunID = in.RunID
	out.Body.State = string(domain.StateChangesRequested)
	out.Body.DecisionID = decisionID
	return out, nil
}

// ── Quarantine ────────────────────────────────────────────────────────────────

type reviewQuarantineInput struct {
	RunID string `path:"run_id"`
	Body  struct {
		Reason string `json:"reason"`
	}
}

type reviewQuarantineOutput struct {
	Body struct {
		RunID      string `json:"run_id"`
		State      string `json:"state"`
		DecisionID int64  `json:"decision_id"`
	}
}

func (s *Service) handleQuarantine(ctx context.Context, in *reviewQuarantineInput) (*reviewQuarantineOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequireScope(p, platform.ScopeWriteReviews); err != nil {
		return nil, err
	}

	reviewer := p.Email

	_, err := statemachine.Transition(ctx, s.pool, statemachine.TransitionParams{
		RunID:        in.RunID,
		ExpectedFrom: domain.StateAwaitingReview,
		To:           domain.StateQuarantined,
		ActorType:    domain.ActorReviewer,
		ActorID:      reviewer,
		Reason:       in.Body.Reason,
	})
	if err != nil {
		return nil, mapSMErr(err)
	}

	decisionID, err := s.insertDecision(ctx, in.RunID, "quarantine", reviewer, in.Body.Reason)
	if err != nil {
		return nil, err
	}

	// Best-effort notification; never fail the primary action.
	_ = notify.Enqueue(ctx, s.queue, s.pool, "quarantined", in.RunID, "Run "+in.RunID+" quarantined", nil)

	out := &reviewQuarantineOutput{}
	out.Body.RunID = in.RunID
	out.Body.State = string(domain.StateQuarantined)
	out.Body.DecisionID = decisionID
	return out, nil
}

// ── PATCH metadata ────────────────────────────────────────────────────────────

type reviewPatchMetadataInput struct {
	RunID string `path:"run_id"`
	Body  struct {
		OperatorComment string `json:"operator_comment"`
	}
}

type reviewPatchMetadataOutput struct {
	Body struct {
		RunID           string `json:"run_id"`
		OperatorComment string `json:"operator_comment"`
	}
}

func (s *Service) handlePatchMetadata(ctx context.Context, in *reviewPatchMetadataInput) (*reviewPatchMetadataOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequireScope(p, platform.ScopeWriteReviews); err != nil {
		return nil, err
	}

	// Simple audited UPDATE — do NOT change state.
	ct, err := s.pool.Exec(ctx,
		`UPDATE runs SET operator_comment = $1, updated_at = now() WHERE id = $2`,
		in.Body.OperatorComment, in.RunID)
	if err != nil {
		return nil, err
	}
	if ct.RowsAffected() == 0 {
		return nil, platform.NotFound("run.not_found", "run does not exist")
	}

	out := &reviewPatchMetadataOutput{}
	out.Body.RunID = in.RunID
	out.Body.OperatorComment = in.Body.OperatorComment
	return out, nil
}

// ── internal helpers ──────────────────────────────────────────────────────────

// insertDecision records a review_decisions row and returns its id.
func (s *Service) insertDecision(ctx context.Context, runID, decision, reviewer, reason string) (int64, error) {
	// Look up the latest review_artifact id (may be null).
	var artifactID *int64
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM review_artifacts WHERE run_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`,
		runID).Scan(&artifactID)
	if err != nil && err != pgx.ErrNoRows {
		return 0, err
	}

	var decisionID int64
	err = s.pool.QueryRow(ctx, `
		INSERT INTO review_decisions (run_id, review_artifact_id, decision, reviewer, reason)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		runID, artifactID, decision, reviewer, reason,
	).Scan(&decisionID)
	if err != nil {
		return 0, err
	}
	return decisionID, nil
}

// latestArtifactSummary loads only the fields needed for the list view.
func (s *Service) latestArtifactSummary(ctx context.Context, runID string) (*reviewArtifactSummary, error) {
	var a reviewArtifactSummary
	var metrics, warnings []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, version, metrics_json, parser_warnings_json, llm_summary, created_at
		FROM review_artifacts WHERE run_id = $1
		ORDER BY created_at DESC, id DESC LIMIT 1`,
		runID).Scan(&a.ID, &a.Version, &metrics, &warnings, &a.LLMSummary, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	a.MetricsJSON = metrics
	a.ParserWarningsJSON = warnings
	return &a, nil
}

// latestArtifactFull loads all review_artifact columns for the detail view.
func (s *Service) latestArtifactFull(ctx context.Context, runID string) (*reviewArtifactFull, error) {
	var a reviewArtifactFull
	var summary, plots, metrics, warnings []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, run_id, version, summary_json, plots_json, metrics_json, parser_warnings_json, llm_summary, created_at
		FROM review_artifacts WHERE run_id = $1
		ORDER BY created_at DESC, id DESC LIMIT 1`,
		runID).Scan(&a.ID, &a.RunID, &a.Version, &summary, &plots, &metrics, &warnings, &a.LLMSummary, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	a.SummaryJSON = summary
	a.PlotsJSON = plots
	a.MetricsJSON = metrics
	a.ParserWarningsJSON = warnings
	return &a, nil
}
