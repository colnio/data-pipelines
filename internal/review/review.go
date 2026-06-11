// Package review implements the human review gate (architecture §17) and
// Pipeline B publishing (§17 "Pipeline B", §5 reproducibility receipt).
//
// The Python parser writes review_artifacts; this module reads them and owns
// review_decisions, published_results, published_artifacts.
package review

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/colnio/data-pipelines/internal/jobs"
	"github.com/colnio/data-pipelines/internal/platform"
	"github.com/colnio/data-pipelines/internal/run"
	"github.com/colnio/data-pipelines/internal/statemachine"
)

// Service is the review module's stateful core. It is the only writer of
// review_decisions, published_results, and published_artifacts.
type Service struct {
	pool  *pgxpool.Pool
	runs  *run.Repo
	queue *jobs.Queue
	log   *slog.Logger
}

// NewService constructs a Service.
func NewService(pool *pgxpool.Pool, runs *run.Repo, queue *jobs.Queue, log *slog.Logger) *Service {
	return &Service{pool: pool, runs: runs, queue: queue, log: log}
}

// mapSMErr converts statemachine typed errors to platform HTTP errors.
// Callers should call this last (after their own errors.Is checks) so the
// platform error is produced exactly once.
func mapSMErr(err error) error {
	if err == nil {
		return nil
	}
	var notFound *statemachine.ErrRunNotFound
	if errors.As(err, &notFound) {
		return platform.NotFound("run.not_found", "run does not exist")
	}
	var mismatch *statemachine.ErrStateMismatch
	if errors.As(err, &mismatch) {
		return platform.Conflict("run.state_mismatch",
			"run is not in the expected state: "+err.Error())
	}
	var illegal *statemachine.ErrIllegalTransition
	if errors.As(err, &illegal) {
		return platform.Errorf(http.StatusUnprocessableEntity,
			"run.illegal_transition", err.Error())
	}
	return err
}
