package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/colnio/data-pipelines/internal/platform"
)

// ── GET /v1/admin/jobs ────────────────────────────────────────────────────────

type adminListJobsInput struct {
	State   string `query:"state" doc:"Filter by job state: queued, running, succeeded, failed, dead"`
	JobType string `query:"job_type" doc:"Filter by job_type"`
	Limit   int    `query:"limit" doc:"Max results (1–500, default 50)"`
}

type adminJobRow struct {
	ID             int64            `json:"id"`
	JobType        string           `json:"job_type"`
	RunID          *string          `json:"run_id,omitempty"`
	State          string           `json:"state"`
	Priority       int              `json:"priority"`
	AttemptCount   int              `json:"attempt_count"`
	MaxAttempts    int              `json:"max_attempts"`
	IdempotencyKey string           `json:"idempotency_key"`
	PayloadJSON    json.RawMessage  `json:"payload_json"`
	LastError      *string          `json:"last_error,omitempty"`
	AvailableAt    time.Time        `json:"available_at"`
	CreatedAt      time.Time        `json:"created_at"`
	StartedAt      *time.Time       `json:"started_at,omitempty"`
	FinishedAt     *time.Time       `json:"finished_at,omitempty"`
}

type adminListJobsOutput struct {
	Body struct {
		Jobs []adminJobRow `json:"jobs"`
	}
}

func (s *Service) handleListJobs(ctx context.Context, in *adminListJobsInput) (*adminListJobsOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequirePrivileged(p); err != nil {
		return nil, err
	}

	limit := in.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	query := `SELECT id, job_type, run_id, state, priority, attempt_count, max_attempts,
	                 idempotency_key, payload_json, last_error, available_at, created_at,
	                 started_at, finished_at
	          FROM jobs WHERE 1=1`
	args := []any{}
	n := 1

	if in.State != "" {
		query += ` AND state = $` + itoa(n)
		args = append(args, in.State)
		n++
	}
	if in.JobType != "" {
		query += ` AND job_type = $` + itoa(n)
		args = append(args, in.JobType)
		n++
	}
	query += ` ORDER BY created_at DESC LIMIT $` + itoa(n)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []adminJobRow
	for rows.Next() {
		var j adminJobRow
		var payload []byte
		if err := rows.Scan(
			&j.ID, &j.JobType, &j.RunID, &j.State,
			&j.Priority, &j.AttemptCount, &j.MaxAttempts,
			&j.IdempotencyKey, &payload, &j.LastError,
			&j.AvailableAt, &j.CreatedAt, &j.StartedAt, &j.FinishedAt,
		); err != nil {
			return nil, err
		}
		j.PayloadJSON = payload
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := &adminListJobsOutput{}
	if jobs == nil {
		jobs = []adminJobRow{}
	}
	out.Body.Jobs = jobs
	return out, nil
}

// ── GET /v1/admin/audit ───────────────────────────────────────────────────────

type adminListAuditInput struct {
	RunID string `query:"run_id" doc:"Filter by run_id"`
	Limit int    `query:"limit" doc:"Max results (1–500, default 50)"`
}

type adminAuditRow struct {
	ID          int64           `json:"id"`
	RunID       string          `json:"run_id"`
	FromState   string          `json:"from_state"`
	ToState     string          `json:"to_state"`
	ActorType   string          `json:"actor_type"`
	ActorID     string          `json:"actor_id"`
	Reason      string          `json:"reason"`
	PayloadJSON json.RawMessage `json:"payload_json"`
	CreatedAt   time.Time       `json:"created_at"`
}

type adminListAuditOutput struct {
	Body struct {
		Transitions []adminAuditRow `json:"transitions"`
	}
}

func (s *Service) handleListAudit(ctx context.Context, in *adminListAuditInput) (*adminListAuditOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequirePrivileged(p); err != nil {
		return nil, err
	}

	limit := in.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	query := `SELECT id, run_id, from_state, to_state, actor_type, actor_id, reason,
	                 payload_json, created_at
	          FROM run_state_transitions WHERE 1=1`
	args := []any{}
	n := 1

	if in.RunID != "" {
		query += ` AND run_id = $` + itoa(n)
		args = append(args, in.RunID)
		n++
	}
	query += ` ORDER BY created_at DESC LIMIT $` + itoa(n)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var transitions []adminAuditRow
	for rows.Next() {
		var t adminAuditRow
		var payload []byte
		if err := rows.Scan(
			&t.ID, &t.RunID, &t.FromState, &t.ToState,
			&t.ActorType, &t.ActorID, &t.Reason, &payload, &t.CreatedAt,
		); err != nil {
			return nil, err
		}
		t.PayloadJSON = payload
		transitions = append(transitions, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := &adminListAuditOutput{}
	if transitions == nil {
		transitions = []adminAuditRow{}
	}
	out.Body.Transitions = transitions
	return out, nil
}

// registerInspectionHandlers wires the jobs/audit read-only endpoints onto api.
func registerInspectionHandlers(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-jobs",
		Method:      http.MethodGet,
		Path:        "/v1/admin/jobs",
		Summary:     "List jobs",
		Description: "Read-only inspection of the jobs queue, optionally filtered. Requires admin or pi role.",
		Tags:        []string{"admin"},
	}, svc.handleListJobs)

	huma.Register(api, huma.Operation{
		OperationID: "admin-list-audit",
		Method:      http.MethodGet,
		Path:        "/v1/admin/audit",
		Summary:     "List run state transitions",
		Description: "Read-only audit log of state transitions, optionally filtered by run_id. Requires admin or pi role.",
		Tags:        []string{"admin"},
	}, svc.handleListAudit)
}

// itoa converts a small integer to its decimal string representation without
// importing strconv (avoids an extra import for a trivial need).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
