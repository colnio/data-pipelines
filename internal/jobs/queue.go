// Package jobs implements the durable Postgres jobs queue and worker runtime
// (architecture §15). Jobs are claimed with FOR UPDATE SKIP LOCKED; a crashed
// worker leaves a stale locked_until that another worker reclaims via
// ReclaimStale.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/colnio/data-pipelines/internal/domain"
)

// Queue wraps a pgx pool and provides the core queue operations: Enqueue,
// Claim, Complete, Fail, ReclaimStale, and Get.
type Queue struct {
	pool *pgxpool.Pool
}

// NewQueue creates a Queue backed by the given connection pool.
func NewQueue(pool *pgxpool.Pool) *Queue {
	return &Queue{pool: pool}
}

// EnqueueParams holds the caller-supplied fields for a new job row.
type EnqueueParams struct {
	JobType        domain.JobType
	RunID          *string
	Priority       int
	MaxAttempts    int
	AvailableAt    time.Time
	IdempotencyKey string
	Payload        json.RawMessage
}

// Enqueue inserts a new job row. If a row with the same IdempotencyKey already
// exists the existing job is returned unchanged (idempotent). MaxAttempts <= 0
// defaults to 5. A zero AvailableAt defaults to now().
func (q *Queue) Enqueue(ctx context.Context, p EnqueueParams) (domain.Job, error) {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 5
	}
	if p.AvailableAt.IsZero() {
		p.AvailableAt = time.Now()
	}
	payload := p.Payload
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}

	const insertSQL = `
INSERT INTO jobs (
    job_type, run_id, priority, max_attempts, available_at,
    idempotency_key, payload_json
) VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING id`

	var insertedID int64
	err := q.pool.QueryRow(ctx, insertSQL,
		string(p.JobType), p.RunID, p.Priority, p.MaxAttempts, p.AvailableAt,
		p.IdempotencyKey, payload,
	).Scan(&insertedID)

	if err == pgx.ErrNoRows {
		// Row already existed; fetch and return it.
		return q.getByKey(ctx, p.IdempotencyKey)
	}
	if err != nil {
		return domain.Job{}, fmt.Errorf("jobs: enqueue insert: %w", err)
	}
	return q.Get(ctx, insertedID)
}

// getByKey fetches a job by its idempotency key (used after an ON CONFLICT DO NOTHING).
func (q *Queue) getByKey(ctx context.Context, key string) (domain.Job, error) {
	const sel = `
SELECT id, job_type, run_id, state, priority, attempt_count, max_attempts,
       available_at, locked_by, locked_until, idempotency_key,
       payload_json, result_json, last_error, created_at, started_at, finished_at
FROM jobs WHERE idempotency_key = $1`

	row := q.pool.QueryRow(ctx, sel, key)
	return scanJob(row)
}

// Get fetches a job by primary key. Useful for tests and inspection.
func (q *Queue) Get(ctx context.Context, jobID int64) (domain.Job, error) {
	const sel = `
SELECT id, job_type, run_id, state, priority, attempt_count, max_attempts,
       available_at, locked_by, locked_until, idempotency_key,
       payload_json, result_json, last_error, created_at, started_at, finished_at
FROM jobs WHERE id = $1`

	row := q.pool.QueryRow(ctx, sel, jobID)
	j, err := scanJob(row)
	if err == pgx.ErrNoRows {
		return domain.Job{}, fmt.Errorf("jobs: job %d not found", jobID)
	}
	return j, err
}

// Claim atomically picks the highest-priority oldest queued job whose
// available_at <= now(), marks it running, and returns it. Returns (nil, nil)
// when no job is available.
func (q *Queue) Claim(ctx context.Context, workerID string, lockTTL time.Duration) (*domain.Job, error) {
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("jobs: claim begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	const pickSQL = `
SELECT id FROM jobs
WHERE state = 'queued' AND available_at <= now()
ORDER BY priority DESC, created_at ASC
FOR UPDATE SKIP LOCKED
LIMIT 1`

	var jobID int64
	err = tx.QueryRow(ctx, pickSQL).Scan(&jobID)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("jobs: claim select: %w", err)
	}

	const updateSQL = `
UPDATE jobs
SET state        = 'running',
    locked_by    = $2,
    locked_until = now() + $3::interval,
    started_at   = COALESCE(started_at, now()),
    attempt_count = attempt_count + 1
WHERE id = $1
RETURNING id, job_type, run_id, state, priority, attempt_count, max_attempts,
          available_at, locked_by, locked_until, idempotency_key,
          payload_json, result_json, last_error, created_at, started_at, finished_at`

	row := tx.QueryRow(ctx, updateSQL, jobID, workerID, fmt.Sprintf("%d seconds", int(lockTTL.Seconds())))
	job, err := scanJob(row)
	if err != nil {
		return nil, fmt.Errorf("jobs: claim update: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("jobs: claim commit: %w", err)
	}
	return &job, nil
}

// Complete marks a job as succeeded and stores its result.
func (q *Queue) Complete(ctx context.Context, jobID int64, result json.RawMessage) error {
	if len(result) == 0 {
		result = json.RawMessage("{}")
	}
	const sql = `
UPDATE jobs
SET state        = 'succeeded',
    result_json  = $2,
    finished_at  = now(),
    locked_by    = NULL,
    locked_until = NULL
WHERE id = $1`

	ct, err := q.pool.Exec(ctx, sql, jobID, result)
	if err != nil {
		return fmt.Errorf("jobs: complete: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("jobs: complete: job %d not found", jobID)
	}
	return nil
}

// Fail records a job failure. If attempt_count >= max_attempts the job is
// moved to state 'dead'; otherwise it is re-queued with an exponential backoff
// on available_at. Returns dead=true when the job has been exhausted.
func (q *Queue) Fail(ctx context.Context, jobID int64, errMsg string) (dead bool, err error) {
	// Load current counts.
	var attempts, maxAttempts int
	const selSQL = `SELECT attempt_count, max_attempts FROM jobs WHERE id = $1`
	if err := q.pool.QueryRow(ctx, selSQL, jobID).Scan(&attempts, &maxAttempts); err != nil {
		if err == pgx.ErrNoRows {
			return false, fmt.Errorf("jobs: fail: job %d not found", jobID)
		}
		return false, fmt.Errorf("jobs: fail: load counts: %w", err)
	}

	if attempts >= maxAttempts {
		const deadSQL = `
UPDATE jobs
SET state        = 'dead',
    last_error   = $2,
    finished_at  = now(),
    locked_by    = NULL,
    locked_until = NULL
WHERE id = $1`
		if _, err := q.pool.Exec(ctx, deadSQL, jobID, errMsg); err != nil {
			return false, fmt.Errorf("jobs: fail->dead: %w", err)
		}
		return true, nil
	}

	// Re-queue with backoff.
	bo := backoff(attempts)
	const requeueSQL = `
UPDATE jobs
SET state        = 'queued',
    available_at = now() + $2::interval,
    last_error   = $3,
    locked_by    = NULL,
    locked_until = NULL
WHERE id = $1`
	if _, err := q.pool.Exec(ctx, requeueSQL, jobID, fmt.Sprintf("%d seconds", int(bo.Seconds())), errMsg); err != nil {
		return false, fmt.Errorf("jobs: fail->requeue: %w", err)
	}
	return false, nil
}

// backoff returns the delay before a job is eligible to be retried after
// attempt attempts. Uses exponential backoff capped at 5 minutes.
func backoff(attempt int) time.Duration {
	const cap = 5 * time.Minute
	secs := math.Pow(2, float64(attempt))
	d := time.Duration(secs) * time.Second
	if d > cap {
		return cap
	}
	return d
}

// ReclaimStale finds jobs that are in state 'running' but whose locked_until
// has passed (i.e. the worker crashed or timed out). Each such job is treated
// as a failure: it is either re-queued or moved to dead, exactly as Fail does.
// Returns the number of jobs reclaimed.
func (q *Queue) ReclaimStale(ctx context.Context) (int, error) {
	// Collect ids of stale running jobs.
	const selStaleSQL = `
SELECT id FROM jobs
WHERE state = 'running' AND locked_until < now()`

	rows, err := q.pool.Query(ctx, selStaleSQL)
	if err != nil {
		return 0, fmt.Errorf("jobs: reclaim stale select: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("jobs: reclaim stale scan: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("jobs: reclaim stale rows: %w", err)
	}

	count := 0
	for _, id := range ids {
		if _, err := q.Fail(ctx, id, "lock expired"); err != nil {
			return count, fmt.Errorf("jobs: reclaim stale fail job %d: %w", id, err)
		}
		count++
	}
	return count, nil
}

// scanJob reads a domain.Job from a pgx row (QueryRow).
func scanJob(row pgx.Row) (domain.Job, error) {
	var j domain.Job
	var jobType string
	var state string
	var payload []byte
	var result []byte

	err := row.Scan(
		&j.ID,
		&jobType,
		&j.RunID,
		&state,
		&j.Priority,
		&j.AttemptCount,
		&j.MaxAttempts,
		&j.AvailableAt,
		&j.LockedBy,
		&j.LockedUntil,
		&j.IdempotencyKey,
		&payload,
		&result,
		&j.LastError,
		&j.CreatedAt,
		&j.StartedAt,
		&j.FinishedAt,
	)
	if err != nil {
		return domain.Job{}, err
	}
	j.JobType = domain.JobType(jobType)
	j.State = domain.JobState(state)
	if len(payload) > 0 {
		j.Payload = json.RawMessage(payload)
	}
	if len(result) > 0 {
		j.Result = json.RawMessage(result)
	}
	return j, nil
}
