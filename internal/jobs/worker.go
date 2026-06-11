package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/colnio/data-pipelines/internal/domain"
)

// Handler is the function signature for a job handler. It receives the job and
// returns a JSON result (or nil) on success, or an error on failure. The
// Queue will call Complete or Fail accordingly.
type Handler func(ctx context.Context, job domain.Job) (json.RawMessage, error)

// WorkerOptions tunes the Worker's polling and maintenance intervals.
// Zero values are replaced with sane defaults in NewWorker.
type WorkerOptions struct {
	// PollInterval is how long to sleep when no job is available.
	PollInterval time.Duration
	// LockTTL is how long a claimed job is held before it is eligible for
	// reclaim by another worker.
	LockTTL time.Duration
	// ReclaimInterval is how often ReclaimStale is called.
	ReclaimInterval time.Duration
}

// Worker polls the jobs queue, dispatches jobs to registered handlers, and
// periodically reclaims stale locks left behind by crashed workers.
type Worker struct {
	q        *Queue
	workerID string
	handlers map[domain.JobType]Handler
	opts     WorkerOptions
	log      *slog.Logger
}

// NewWorker creates a Worker. Zero WorkerOptions fields are replaced with:
//
//	PollInterval    = 1s
//	LockTTL         = 5m
//	ReclaimInterval = 1m
func NewWorker(q *Queue, workerID string, handlers map[domain.JobType]Handler, opts WorkerOptions) *Worker {
	if opts.PollInterval <= 0 {
		opts.PollInterval = time.Second
	}
	if opts.LockTTL <= 0 {
		opts.LockTTL = 5 * time.Minute
	}
	if opts.ReclaimInterval <= 0 {
		opts.ReclaimInterval = time.Minute
	}
	return &Worker{
		q:        q,
		workerID: workerID,
		handlers: handlers,
		opts:     opts,
		log:      slog.Default(),
	}
}

// Run starts the worker loop. It runs until ctx is cancelled, then returns
// ctx.Err(). It claims one job per iteration; if none is available it sleeps
// PollInterval. Every ReclaimInterval it also reclaims stale running jobs.
func (w *Worker) Run(ctx context.Context) error {
	reclaimTicker := time.NewTicker(w.opts.ReclaimInterval)
	defer reclaimTicker.Stop()

	for {
		// Check for context cancellation first.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Periodic reclaim check (non-blocking).
		select {
		case <-reclaimTicker.C:
			n, err := w.q.ReclaimStale(ctx)
			if err != nil {
				w.log.Error("jobs: worker reclaim stale", "worker", w.workerID, "err", err)
			} else if n > 0 {
				w.log.Info("jobs: reclaimed stale jobs", "worker", w.workerID, "count", n)
			}
		default:
		}

		// Attempt to claim a job.
		job, err := w.q.Claim(ctx, w.workerID, w.opts.LockTTL)
		if err != nil {
			w.log.Error("jobs: claim error", "worker", w.workerID, "err", err)
			// Back off a bit before retrying on transient DB errors.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(w.opts.PollInterval):
			}
			continue
		}
		if job == nil {
			// Nothing available; sleep then try again.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(w.opts.PollInterval):
			}
			continue
		}

		w.dispatch(ctx, *job)
	}
}

// dispatch runs the appropriate handler for a claimed job and then records
// success or failure. It does not return an error; problems are logged and the
// job is failed/dead via Fail.
func (w *Worker) dispatch(ctx context.Context, job domain.Job) {
	handler, ok := w.handlers[job.JobType]
	if !ok {
		msg := fmt.Sprintf("no handler registered for job type %q", job.JobType)
		w.log.Error("jobs: unknown job type", "worker", w.workerID, "job_id", job.ID, "job_type", job.JobType)
		if _, err := w.q.Fail(ctx, job.ID, msg); err != nil {
			w.log.Error("jobs: fail unknown-type job", "worker", w.workerID, "job_id", job.ID, "err", err)
		}
		return
	}

	result, err := handler(ctx, job)
	if err != nil {
		w.log.Error("jobs: handler error", "worker", w.workerID, "job_id", job.ID, "job_type", job.JobType, "err", err)
		if _, failErr := w.q.Fail(ctx, job.ID, err.Error()); failErr != nil {
			w.log.Error("jobs: fail job after handler error", "worker", w.workerID, "job_id", job.ID, "err", failErr)
		}
		return
	}

	if completeErr := w.q.Complete(ctx, job.ID, result); completeErr != nil {
		w.log.Error("jobs: complete job", "worker", w.workerID, "job_id", job.ID, "err", completeErr)
	}
}
