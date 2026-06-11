package jobs_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/colnio/data-pipelines/internal/domain"
	"github.com/colnio/data-pipelines/internal/jobs"
	"github.com/colnio/data-pipelines/internal/testsupport"
)

// newQueueAndPool returns a Queue and the underlying pool (for test setup SQL).
func newQueueAndPool(t *testing.T) (*jobs.Queue, *pgxpool.Pool) {
	t.Helper()
	pool := testsupport.NewPool(t)
	testsupport.Truncate(t, pool, "jobs")
	return jobs.NewQueue(pool), pool
}

func baseParams(key string) jobs.EnqueueParams {
	return jobs.EnqueueParams{
		JobType:        domain.JobSendNotification,
		Priority:       0,
		MaxAttempts:    3,
		AvailableAt:    time.Now().Add(-time.Second), // already available
		IdempotencyKey: key,
		Payload:        json.RawMessage(`{"hello":"world"}`),
	}
}

// ---- Enqueue idempotency -----------------------------------------------

func TestEnqueue_Idempotent(t *testing.T) {
	q, _ := newQueueAndPool(t)
	ctx := context.Background()

	p := baseParams("idem-key-1")
	j1, err := q.Enqueue(ctx, p)
	require.NoError(t, err)
	assert.NotZero(t, j1.ID)

	j2, err := q.Enqueue(ctx, p)
	require.NoError(t, err)

	assert.Equal(t, j1.ID, j2.ID, "second enqueue with same key must return same job id")

	// Only one row should exist.
	j3, err := q.Get(ctx, j1.ID)
	require.NoError(t, err)
	assert.Equal(t, j1.ID, j3.ID)
}

// ---- Claim ordering -------------------------------------------------------

func TestClaim_PriorityThenAge(t *testing.T) {
	q, _ := newQueueAndPool(t)
	ctx := context.Background()

	// Enqueue three jobs: two at priority 0, one at priority 10.
	older := baseParams("order-old")
	older.Priority = 0
	older.AvailableAt = time.Now().Add(-3 * time.Second) // older

	newer := baseParams("order-new")
	newer.Priority = 0
	newer.AvailableAt = time.Now().Add(-time.Second) // newer

	high := baseParams("order-high")
	high.Priority = 10
	high.AvailableAt = time.Now().Add(-2 * time.Second)

	jOlder, err := q.Enqueue(ctx, older)
	require.NoError(t, err)
	jNewer, err := q.Enqueue(ctx, newer)
	require.NoError(t, err)
	jHigh, err := q.Enqueue(ctx, high)
	require.NoError(t, err)

	// First claim: highest priority.
	c1, err := q.Claim(ctx, "w1", 5*time.Minute)
	require.NoError(t, err)
	require.NotNil(t, c1)
	assert.Equal(t, jHigh.ID, c1.ID, "highest priority job should be claimed first")
	assert.Equal(t, domain.JobRunning, c1.State)
	assert.NotNil(t, c1.LockedBy)
	assert.Equal(t, "w1", *c1.LockedBy)
	assert.Equal(t, 1, c1.AttemptCount)

	// Second claim: among the two priority-0 jobs, the older one.
	c2, err := q.Claim(ctx, "w1", 5*time.Minute)
	require.NoError(t, err)
	require.NotNil(t, c2)
	assert.Equal(t, jOlder.ID, c2.ID, "older job should be claimed before newer")

	// Third claim: the remaining job.
	c3, err := q.Claim(ctx, "w1", 5*time.Minute)
	require.NoError(t, err)
	require.NotNil(t, c3)
	assert.Equal(t, jNewer.ID, c3.ID)
}

func TestClaim_NoneAvailable(t *testing.T) {
	q, _ := newQueueAndPool(t)
	ctx := context.Background()

	p := baseParams("claim-nil-1")
	_, err := q.Enqueue(ctx, p)
	require.NoError(t, err)

	// Claim the one job.
	c1, err := q.Claim(ctx, "w1", 5*time.Minute)
	require.NoError(t, err)
	require.NotNil(t, c1)

	// Second claim must return nil.
	c2, err := q.Claim(ctx, "w2", 5*time.Minute)
	require.NoError(t, err)
	assert.Nil(t, c2, "no queued jobs left; second claim should return nil")
}

// ---- Complete --------------------------------------------------------------

func TestComplete(t *testing.T) {
	q, _ := newQueueAndPool(t)
	ctx := context.Background()

	j, err := q.Enqueue(ctx, baseParams("complete-1"))
	require.NoError(t, err)

	c, err := q.Claim(ctx, "w1", 5*time.Minute)
	require.NoError(t, err)
	require.NotNil(t, c)

	result := json.RawMessage(`{"status":"ok"}`)
	err = q.Complete(ctx, j.ID, result)
	require.NoError(t, err)

	done, err := q.Get(ctx, j.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.JobSucceeded, done.State)
	assert.NotNil(t, done.FinishedAt)
	assert.Nil(t, done.LockedBy)
	assert.Nil(t, done.LockedUntil)
	assert.JSONEq(t, `{"status":"ok"}`, string(done.Result))
}

// ---- Fail ------------------------------------------------------------------

func TestFail_BelowMax_Requeued(t *testing.T) {
	q, _ := newQueueAndPool(t)
	ctx := context.Background()

	p := baseParams("fail-requeue-1")
	p.MaxAttempts = 3
	j, err := q.Enqueue(ctx, p)
	require.NoError(t, err)

	// Claim it (attempt_count becomes 1).
	c, err := q.Claim(ctx, "w1", 5*time.Minute)
	require.NoError(t, err)
	require.NotNil(t, c)
	assert.Equal(t, 1, c.AttemptCount)

	dead, err := q.Fail(ctx, j.ID, "transient error")
	require.NoError(t, err)
	assert.False(t, dead)

	requeued, err := q.Get(ctx, j.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.JobQueued, requeued.State)
	// available_at must be in the future (backoff applied).
	assert.True(t, requeued.AvailableAt.After(time.Now()), "available_at should be in the future after a requeue")
	assert.Nil(t, requeued.LockedBy)
	assert.Nil(t, requeued.LockedUntil)
	require.NotNil(t, requeued.LastError)
	assert.Contains(t, *requeued.LastError, "transient error")
}

func TestFail_AtMax_Dead(t *testing.T) {
	q, pool := newQueueAndPool(t)
	ctx := context.Background()

	p := baseParams("fail-dead-1")
	p.MaxAttempts = 2
	j, err := q.Enqueue(ctx, p)
	require.NoError(t, err)

	// Claim twice so attempt_count reaches 2.
	for i := 0; i < 2; i++ {
		// After first fail the job is re-queued; make it immediately available.
		if i > 0 {
			_, err = pool.Exec(ctx, "UPDATE jobs SET available_at = now() - interval '1s' WHERE id = $1", j.ID)
			require.NoError(t, err)
		}
		c, err := q.Claim(ctx, "w1", 5*time.Minute)
		require.NoError(t, err)
		require.NotNil(t, c, "expected a job on attempt %d", i+1)

		dead, err := q.Fail(ctx, j.ID, "fatal")
		require.NoError(t, err)
		if i < 1 {
			assert.False(t, dead)
		} else {
			assert.True(t, dead)
		}
	}

	done, err := q.Get(ctx, j.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.JobDead, done.State)
	assert.NotNil(t, done.FinishedAt)
}

// ---- ReclaimStale ----------------------------------------------------------

func TestReclaimStale_Requeued(t *testing.T) {
	q, pool := newQueueAndPool(t)
	ctx := context.Background()

	p := baseParams("reclaim-1")
	p.MaxAttempts = 5
	j, err := q.Enqueue(ctx, p)
	require.NoError(t, err)

	// Manually put the job into state='running' with a locked_until in the past.
	_, err = pool.Exec(ctx,
		`UPDATE jobs SET state='running', locked_by='dead-worker', locked_until=now()-interval '1 minute', attempt_count=1 WHERE id=$1`,
		j.ID,
	)
	require.NoError(t, err)

	n, err := q.ReclaimStale(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	reclaimed, err := q.Get(ctx, j.ID)
	require.NoError(t, err)
	// attempt_count=1 < max_attempts=5 → should be re-queued.
	assert.Equal(t, domain.JobQueued, reclaimed.State)
	assert.Nil(t, reclaimed.LockedBy)
	assert.Nil(t, reclaimed.LockedUntil)
}

func TestReclaimStale_Dead(t *testing.T) {
	q, pool := newQueueAndPool(t)
	ctx := context.Background()

	p := baseParams("reclaim-dead-1")
	p.MaxAttempts = 1
	j, err := q.Enqueue(ctx, p)
	require.NoError(t, err)

	// Simulate a stale running job where attempt_count has already hit max.
	_, err = pool.Exec(ctx,
		`UPDATE jobs SET state='running', locked_by='dead-worker', locked_until=now()-interval '1 minute', attempt_count=1 WHERE id=$1`,
		j.ID,
	)
	require.NoError(t, err)

	n, err := q.ReclaimStale(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	reclaimed, err := q.Get(ctx, j.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.JobDead, reclaimed.State)
}

// ---- Worker integration test -----------------------------------------------

func TestWorker_RunAndComplete(t *testing.T) {
	pool := testsupport.NewPool(t)
	testsupport.Truncate(t, pool, "jobs")
	q := jobs.NewQueue(pool)
	ctx := context.Background()

	var handlerCalled atomic.Bool

	handlers := map[domain.JobType]jobs.Handler{
		domain.JobSendNotification: func(ctx context.Context, job domain.Job) (json.RawMessage, error) {
			handlerCalled.Store(true)
			return json.RawMessage(`{"done":true}`), nil
		},
	}

	opts := jobs.WorkerOptions{
		PollInterval:    20 * time.Millisecond,
		LockTTL:         10 * time.Second,
		ReclaimInterval: time.Hour, // don't reclaim during test
	}
	w := jobs.NewWorker(q, "test-worker", handlers, opts)

	// Enqueue a job.
	j, err := q.Enqueue(ctx, baseParams("worker-test-1"))
	require.NoError(t, err)

	// Run the worker with a short timeout.
	workerCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	// Run in background; ignore context-cancelled error.
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		_ = w.Run(workerCtx)
	}()

	// Wait until the job is succeeded or the outer timeout fires.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		jj, getErr := q.Get(ctx, j.ID)
		if getErr == nil && jj.State == domain.JobSucceeded {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	cancel()
	<-workerDone

	assert.True(t, handlerCalled.Load(), "handler must have been called")

	final, err := q.Get(ctx, j.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.JobSucceeded, final.State)
}
