// Package session exposes the "process a sample-session" trigger. A session is
// the unit of review in this lab: one measurement session over a sample (all its
// devices) is aggregated into the C-V/C-F and IV-breakdown summary plots that go
// to the reviewer. This module creates the synthetic session run (run package)
// and enqueues a process_session job; the Python worker runs the notebooks and
// drives the run to awaiting_review, after which it flows through the normal
// review -> publish path.
package session

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/colnio/data-pipelines/internal/domain"
	"github.com/colnio/data-pipelines/internal/jobs"
	"github.com/colnio/data-pipelines/internal/platform"
	"github.com/colnio/data-pipelines/internal/run"
)

// Service wires the run repository and the durable queue.
type Service struct {
	runs  *run.Repo
	queue *jobs.Queue
}

// NewService constructs the session service. The pool is currently unused beyond
// the injected repo/queue but kept for symmetry with the other modules.
func NewService(_ *pgxpool.Pool, runs *run.Repo, queue *jobs.Queue) *Service {
	return &Service{runs: runs, queue: queue}
}

// Register mounts the session endpoints. Triggering session processing is a
// privileged operator action (admin or pi).
func Register(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "sessions-process",
		Method:      http.MethodPost,
		Path:        "/v1/sessions/process",
		Summary:     "Process a sample session",
		Description: "Aggregate a sample-session's device measurements into review summaries: creates the synthetic session run and enqueues a process_session job.",
		Tags:        []string{"sessions"},
	}, svc.handleProcess)
}

type processSessionInput struct {
	Body struct {
		SampleID   string `json:"sample_id" doc:"Sample id, e.g. 9D66P1"`
		SessionKey string `json:"session_key" doc:"Session key, e.g. the date 2026-06-10"`
		SessionDir string `json:"session_dir" doc:"Server-side path to the session directory containing the per-device measurement folders"`
	}
}

type processSessionOutput struct {
	Body struct {
		RunID    string `json:"run_id"`
		Created  bool   `json:"created"`
		Enqueued bool   `json:"enqueued"`
	}
}

func (s *Service) handleProcess(ctx context.Context, in *processSessionInput) (*processSessionOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequirePrivileged(p); err != nil {
		return nil, err
	}
	if in.Body.SampleID == "" || in.Body.SessionKey == "" || in.Body.SessionDir == "" {
		return nil, platform.BadRequest("session.invalid", "sample_id, session_key and session_dir are required")
	}

	runID, created, err := s.runs.EnsureSessionRun(ctx, in.Body.SampleID, in.Body.SessionKey, in.Body.SessionDir, p.Email)
	if err != nil {
		return nil, err
	}

	payload, _ := json.Marshal(map[string]string{
		"run_id":      runID,
		"sample_id":   in.Body.SampleID,
		"session_key": in.Body.SessionKey,
		"session_dir": in.Body.SessionDir,
	})
	if _, err := s.queue.Enqueue(ctx, jobs.EnqueueParams{
		JobType:        domain.JobProcessSession,
		RunID:          &runID,
		Priority:       5,
		IdempotencyKey: "process_session:" + runID,
		Payload:        payload,
	}); err != nil {
		return nil, err
	}

	out := &processSessionOutput{}
	out.Body.RunID = runID
	out.Body.Created = created
	out.Body.Enqueued = true
	return out, nil
}
