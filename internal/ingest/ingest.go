// Package ingest is the agent-facing front door (architecture §13): it
// authenticates a measurement-PC agent, validates the manifest and confines
// meas_path to the agent's allowed roots, idempotently declares the run, and
// enqueues the pull pipeline. The heavy lifting (pull → verify → unpack →
// promote) happens in the worker pipeline (pipeline.go), not in the request.
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/colnio/data-pipelines/internal/agentauth"
	"github.com/colnio/data-pipelines/internal/domain"
	"github.com/colnio/data-pipelines/internal/jobs"
	"github.com/colnio/data-pipelines/internal/manifest"
	"github.com/colnio/data-pipelines/internal/run"
)

// Service handles manifest ingestion.
type Service struct {
	pool   *pgxpool.Pool
	agents *agentauth.Service
	runs   *run.Repo
	queue  *jobs.Queue
	log    *slog.Logger
}

// NewService constructs the ingest service.
func NewService(pool *pgxpool.Pool, agents *agentauth.Service, runs *run.Repo, queue *jobs.Queue, log *slog.Logger) *Service {
	return &Service{pool: pool, agents: agents, runs: runs, queue: queue, log: log}
}

// Register wires the agent ingest endpoint. It authenticates with a per-agent
// shared key in headers (NOT the human JWT), so it is exempt from the human
// auth/scope chain.
func Register(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "agent-post-manifest",
		Method:      http.MethodPost,
		Path:        "/v1/agents/manifest",
		Summary:     "Declare a completed run (agent manifest POST)",
		Description: "A measurement-PC agent POSTs the run manifest to declare completion. " +
			"Authenticated with X-Agent-Id + X-Agent-Key. Idempotent on run_id: a duplicate " +
			"POST returns the existing run without re-enqueuing work.",
		Tags: []string{"ingest"},
	}, svc.handleManifest)
}

type manifestInput struct {
	AgentID  string          `header:"X-Agent-Id" doc:"Agent identifier (matches agents.id)"`
	AgentKey string          `header:"X-Agent-Key" doc:"Agent shared key"`
	Body     domain.Manifest `doc:"The run manifest (architecture §11)"`
}

type manifestOutput struct {
	Body struct {
		RunID   string          `json:"run_id"`
		State   domain.RunState `json:"state"`
		Created bool            `json:"created" doc:"true if this POST declared a new run; false if it already existed"`
	}
}

func (s *Service) handleManifest(ctx context.Context, in *manifestInput) (*manifestOutput, error) {
	// 1. Authenticate the agent. All auth failures map to 401 without leaking
	// which check failed; the detail is logged server-side.
	agent, err := s.agents.Authenticate(ctx, in.AgentID, in.AgentKey)
	if err != nil {
		s.log.Warn("agent auth failed", "agent_id", in.AgentID, "err", err)
		return nil, huma.Error401Unauthorized("agent authentication failed")
	}

	m := in.Body

	// 2. The manifest's declared agent_id must match the authenticated agent.
	if m.AgentID != agent.ID {
		return nil, huma.Error400BadRequest("manifest agent_id does not match authenticated agent")
	}

	// 3. Validate the manifest contract (§11). Surface every field problem.
	if err := manifest.ValidateAt(m, time.Now()); err != nil {
		var ve *manifest.ValidationError
		if errors.As(err, &ve) {
			details := make([]string, 0, len(ve.FieldErrors()))
			for _, fe := range ve.FieldErrors() {
				details = append(details, fmt.Sprintf("%s: %s", fe.Field, fe.Message))
			}
			return nil, huma.Error422UnprocessableEntity("manifest validation failed", errors.New(joinErrs(details)))
		}
		return nil, huma.Error400BadRequest("invalid manifest: " + err.Error())
	}

	// 4. Confine meas_path to the agent's allowed roots (§4, §13 step 3).
	if err := s.agents.ValidateMeasPath(ctx, agent.ID, m.MeasPath); err != nil {
		s.log.Warn("meas_path rejected", "agent_id", agent.ID, "meas_path", m.MeasPath, "err", err)
		return nil, huma.Error403Forbidden("meas_path is not under an allowed root for this agent")
	}

	// 5. Compute the canonical manifest hash for idempotency + provenance.
	hash, err := manifest.Hash(m)
	if err != nil {
		return nil, fmt.Errorf("hash manifest: %w", err)
	}
	canonical, err := manifest.CanonicalJSON(m)
	if err != nil {
		return nil, fmt.Errorf("canonicalize manifest: %w", err)
	}

	// 6. Idempotently declare the run + record the manifest.
	r, created, err := s.runs.Create(ctx, run.CreateParams{
		RunID:             m.RunID,
		ManifestHash:      hash,
		AgentID:           agent.ID,
		MeasurementType:   m.MeasurementType,
		CompletionSource:  m.CompletionSource,
		SampleID:          emptyToNil(m.SampleID),
		DeviceID:          emptyToNil(m.DeviceID),
		ContactConfigID:   emptyToNil(m.ContactConfigID),
		MeasPath:          m.MeasPath,
		OperatorComment:   m.OperatorComment,
		DeclaredBy:        m.DeclaredBy,
		DeclaredAt:        m.DeclaredAt,
		ConditionLabels:   m.ConditionLabels,
		SchemaVersion:     m.SchemaVersion,
		CanonicalManifest: canonical,
	})
	if err != nil {
		return nil, fmt.Errorf("declare run: %w", err)
	}

	// 7. Enqueue the pull pipeline, idempotently (one pull job per run).
	payload, _ := json.Marshal(struct {
		RunID string `json:"run_id"`
	}{RunID: r.ID})
	if _, err := s.queue.Enqueue(ctx, jobs.EnqueueParams{
		JobType:        domain.JobPullRun,
		RunID:          &r.ID,
		IdempotencyKey: "pull:" + r.ID,
		Payload:        payload,
	}); err != nil {
		return nil, fmt.Errorf("enqueue pull job: %w", err)
	}

	s.log.Info("manifest ingested", "run_id", r.ID, "agent_id", agent.ID, "created", created, "state", r.State)
	out := &manifestOutput{}
	out.Body.RunID = r.ID
	out.Body.State = r.State
	out.Body.Created = created
	return out, nil
}

func emptyToNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func joinErrs(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "; "
		}
		out += p
	}
	return out
}
