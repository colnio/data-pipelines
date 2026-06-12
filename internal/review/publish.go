package review

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/colnio/data-pipelines/internal/domain"
	"github.com/colnio/data-pipelines/internal/jobs"
	"github.com/colnio/data-pipelines/internal/notify"
	"github.com/colnio/data-pipelines/internal/statemachine"
)

// Handlers returns the map of job handlers owned by this module.
// The orchestrator wires these into the Go worker.
func (s *Service) Handlers() map[domain.JobType]jobs.Handler {
	return map[domain.JobType]jobs.Handler{
		domain.JobPublishRun: s.handlePublish,
	}
}

// publishResult is the JSON result stored in jobs.result_json on success.
type publishResult struct {
	RunID             string `json:"run_id"`
	PublishedResultID string `json:"published_result_id"`
	AlreadyPublished  bool   `json:"already_published,omitempty"`
}

// handlePublish is the Pipeline B worker handler. It:
//  1. Transitions approved→publishing (or no-ops if already published).
//  2. Builds the reproducibility receipt in published_results.
//  3. Copies analysis_artifacts rows into published_artifacts (append-only).
//  4. Transitions publishing→published.
func (s *Service) handlePublish(ctx context.Context, job domain.Job) (json.RawMessage, error) {
	if job.RunID == nil {
		return nil, fmt.Errorf("publish: job %d has no run_id", job.ID)
	}
	runID := *job.RunID

	// Idempotency: if already published, return success immediately.
	current, err := statemachine.CurrentState(ctx, s.pool, runID)
	if err != nil {
		return nil, mapSMErr(err)
	}
	if current == domain.StatePublished {
		var existingID string
		_ = s.pool.QueryRow(ctx,
			`SELECT id FROM published_results WHERE run_id = $1 ORDER BY published_at DESC LIMIT 1`,
			runID).Scan(&existingID)
		r, _ := json.Marshal(publishResult{RunID: runID, PublishedResultID: existingID, AlreadyPublished: true})
		return r, nil
	}

	// Re-entrant: a retried job whose prior attempt already moved the run to
	// 'publishing' (e.g. it failed mid-receipt) must RESUME, not re-require
	// 'approved' — otherwise it deadlocks in 'publishing' forever. Only perform
	// the approved→publishing transition when still 'approved'.
	switch current {
	case domain.StateApproved:
		if _, err = statemachine.Transition(ctx, s.pool, statemachine.TransitionParams{
			RunID:        runID,
			ExpectedFrom: domain.StateApproved,
			To:           domain.StatePublishing,
			ActorType:    domain.ActorWorker,
			ActorID:      "publish-worker",
			Reason:       "starting Pipeline B publish",
		}); err != nil {
			return nil, mapSMErr(err)
		}
	case domain.StatePublishing:
		// resume — receipt build below is idempotent
	default:
		return nil, fmt.Errorf("publish: run %s in state %q, expected approved/publishing/published", runID, current)
	}

	publishedID, err := s.buildAndInsertReceipt(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("publish: build receipt for run %s: %w", runID, err)
	}

	// Transition publishing→published.
	_, err = statemachine.Transition(ctx, s.pool, statemachine.TransitionParams{
		RunID:        runID,
		ExpectedFrom: domain.StatePublishing,
		To:           domain.StatePublished,
		ActorType:    domain.ActorWorker,
		ActorID:      "publish-worker",
		Reason:       "Pipeline B complete",
	})
	if err != nil {
		return nil, mapSMErr(err)
	}

	// Best-effort notification after successful publish; never fail the job.
	_ = notify.Enqueue(ctx, s.queue, s.pool, "published", runID, "Run "+runID+" published", nil)

	r, _ := json.Marshal(publishResult{RunID: runID, PublishedResultID: publishedID})
	return r, nil
}

// buildAndInsertReceipt creates the published_results row and associated
// published_artifacts rows, then returns the new published_result_id (uuid).
func (s *Service) buildAndInsertReceipt(ctx context.Context, runID string) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Idempotent resume: if a published_results row already exists for this run
	// (a prior attempt got past the insert), reuse it instead of duplicating.
	var existingID string
	if err := tx.QueryRow(ctx,
		`SELECT id FROM published_results WHERE run_id = $1 ORDER BY published_at DESC LIMIT 1`,
		runID).Scan(&existingID); err == nil {
		return existingID, nil
	} else if err != pgx.ErrNoRows {
		return "", fmt.Errorf("check existing published_results: %w", err)
	}

	// ── Load the run ──────────────────────────────────────────────────────────
	var manifestHash string
	if err := tx.QueryRow(ctx, `SELECT manifest_hash FROM runs WHERE id = $1`, runID).Scan(&manifestHash); err != nil {
		return "", fmt.Errorf("load run: %w", err)
	}

	// ── raw_file_hashes_json: list of {name, sha256} from run_files ──────────
	rows, err := tx.Query(ctx, `SELECT name, sha256 FROM run_files WHERE run_id = $1 ORDER BY name`, runID)
	if err != nil {
		return "", fmt.Errorf("query run_files: %w", err)
	}
	type fileHash struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
	}
	var fileHashes []fileHash
	for rows.Next() {
		var fh fileHash
		if err := rows.Scan(&fh.Name, &fh.SHA256); err != nil {
			rows.Close()
			return "", fmt.Errorf("scan run_file: %w", err)
		}
		fileHashes = append(fileHashes, fh)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("run_files rows: %w", err)
	}
	rawFileHashesJSON, err := json.Marshal(fileHashes)
	if err != nil {
		return "", err
	}

	// ── parameter_version_ids_json: from the stored manifest (if present) ────
	// A missing manifest must not block publishing — record empty param versions
	// rather than deadlocking the run in 'publishing'.
	paramVerJSON := []byte(`{}`)
	var manifestRaw []byte
	switch err := tx.QueryRow(ctx,
		`SELECT raw_json FROM manifests WHERE run_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`,
		runID).Scan(&manifestRaw); err {
	case nil:
		var m domain.Manifest
		if err := json.Unmarshal(manifestRaw, &m); err != nil {
			return "", fmt.Errorf("decode manifest: %w", err)
		}
		if pv, mErr := json.Marshal(m.ParamVersions); mErr == nil && pv != nil {
			paramVerJSON = pv
		}
	case pgx.ErrNoRows:
		// leave paramVerJSON = {}
	default:
		return "", fmt.Errorf("load manifest: %w", err)
	}

	// ── parser_version: from the latest parser_results row ───────────────────
	var parserVersion *string
	if err := tx.QueryRow(ctx,
		`SELECT parser_version FROM parser_results WHERE run_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`,
		runID).Scan(&parserVersion); err != nil && err != pgx.ErrNoRows {
		return "", fmt.Errorf("load parser_results: %w", err)
	}

	// ── review_decision_id + published_by from the latest approve decision ───
	var reviewDecisionID *int64
	var publishedBy string
	if err := tx.QueryRow(ctx, `
		SELECT id, reviewer FROM review_decisions
		WHERE run_id = $1 AND decision = 'approve'
		ORDER BY created_at DESC, id DESC LIMIT 1`,
		runID).Scan(&reviewDecisionID, &publishedBy); err != nil && err != pgx.ErrNoRows {
		return "", fmt.Errorf("load review_decision: %w", err)
	}

	// ── INSERT published_results ──────────────────────────────────────────────
	var publishedID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO published_results (
			run_id, manifest_hash, raw_file_hashes_json,
			parameter_version_ids_json, parser_version,
			review_decision_id, published_by, published_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,now())
		RETURNING id`,
		runID, manifestHash, rawFileHashesJSON, paramVerJSON,
		parserVersion, reviewDecisionID, publishedBy,
	).Scan(&publishedID); err != nil {
		return "", fmt.Errorf("insert published_results: %w", err)
	}

	// ── INSERT published_artifacts (one per analysis_artifacts row) ───────────
	artRows, err := tx.Query(ctx, `
		SELECT path, sha256, kind FROM analysis_artifacts
		WHERE run_id = $1 ORDER BY id`, runID)
	if err != nil {
		return "", fmt.Errorf("query analysis_artifacts: %w", err)
	}
	type artifact struct {
		Path   *string
		SHA256 *string
		Kind   *string
	}
	var artifacts []artifact
	for artRows.Next() {
		var a artifact
		if err := artRows.Scan(&a.Path, &a.SHA256, &a.Kind); err != nil {
			artRows.Close()
			return "", fmt.Errorf("scan analysis_artifact: %w", err)
		}
		artifacts = append(artifacts, a)
	}
	artRows.Close()
	if err := artRows.Err(); err != nil {
		return "", fmt.Errorf("analysis_artifacts rows: %w", err)
	}

	for _, a := range artifacts {
		if _, err := tx.Exec(ctx, `
			INSERT INTO published_artifacts (published_result_id, path, sha256, kind)
			VALUES ($1,$2,$3,$4)`,
			publishedID, a.Path, a.SHA256, a.Kind,
		); err != nil {
			return "", fmt.Errorf("insert published_artifact: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return publishedID, nil
}
