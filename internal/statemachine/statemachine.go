package statemachine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/colnio/data-pipelines/internal/domain"
)

// Sentinel errors returned by Transition and TransitionTx. Callers can use
// errors.As to extract the structured fields and map to HTTP status codes:
//   - ErrStateMismatch   → 409 Conflict
//   - ErrIllegalTransition → 422 Unprocessable Entity
//   - ErrUnknownState      → 422 Unprocessable Entity

// ErrUnknownState is returned when a RunState value is not in the domain
// AllRunStates list (i.e. the target state is not recognised).
type ErrUnknownState struct {
	State domain.RunState
}

func (e *ErrUnknownState) Error() string {
	return fmt.Sprintf("statemachine: unknown run state %q", e.State)
}

// ErrIllegalTransition is returned when the from→to transition is not in the
// allowed graph. Maps to HTTP 422.
type ErrIllegalTransition struct {
	From domain.RunState
	To   domain.RunState
}

func (e *ErrIllegalTransition) Error() string {
	return fmt.Sprintf("statemachine: transition %q → %q is not allowed", e.From, e.To)
}

// ErrStateMismatch is returned when the caller supplied an ExpectedFrom state
// that does not match the current DB state. Maps to HTTP 409.
type ErrStateMismatch struct {
	Expected domain.RunState
	Current  domain.RunState
}

func (e *ErrStateMismatch) Error() string {
	return fmt.Sprintf("statemachine: expected run in state %q but found %q", e.Expected, e.Current)
}

// ErrRunNotFound is returned when no run with the given ID exists.
type ErrRunNotFound struct {
	RunID string
}

func (e *ErrRunNotFound) Error() string {
	return fmt.Sprintf("statemachine: run %q not found", e.RunID)
}

// TransitionParams carries all inputs required for a state transition.
//
// If ExpectedFrom is the zero value (""), the current state check is skipped
// and the transition is applied optimistically (after legality check).
type TransitionParams struct {
	RunID        string
	ExpectedFrom domain.RunState
	To           domain.RunState
	ActorType    domain.ActorType
	ActorID      string
	Reason       string
	// Payload is stored as payload_json. Nil is normalised to '{}'.
	Payload json.RawMessage
}

// CurrentState reads the current state of a run using the given query handle
// (pool or tx). Returns ErrRunNotFound when the run does not exist.
func CurrentState(ctx context.Context, pool *pgxpool.Pool, runID string) (domain.RunState, error) {
	var state domain.RunState
	err := pool.QueryRow(ctx, `SELECT state FROM runs WHERE id = $1`, runID).Scan(&state)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", &ErrRunNotFound{RunID: runID}
		}
		return "", fmt.Errorf("statemachine: read state for run %q: %w", runID, err)
	}
	return state, nil
}

// Transition opens its own transaction, validates the transition, applies it,
// and writes an audit row to run_state_transitions. It returns the newly
// created audit row.
//
// Error mapping for HTTP handlers:
//   - *ErrRunNotFound      → 404
//   - *ErrStateMismatch    → 409
//   - *ErrIllegalTransition → 422
func Transition(ctx context.Context, pool *pgxpool.Pool, p TransitionParams) (domain.RunStateTransition, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return domain.RunStateTransition{}, fmt.Errorf("statemachine: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row, err := TransitionTx(ctx, tx, p)
	if err != nil {
		return domain.RunStateTransition{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.RunStateTransition{}, fmt.Errorf("statemachine: commit tx: %w", err)
	}
	return row, nil
}

// TransitionTx performs the same work as Transition but inside a caller-
// supplied transaction. Use this when the state change must be atomic with
// other writes in the same transaction.
func TransitionTx(ctx context.Context, tx pgx.Tx, p TransitionParams) (domain.RunStateTransition, error) {
	// Validate target state is known.
	if !p.To.Valid() {
		return domain.RunStateTransition{}, &ErrUnknownState{State: p.To}
	}

	// Normalise nil payload.
	payload := p.Payload
	if payload == nil {
		payload = json.RawMessage(`{}`)
	}

	// Lock the run row and read current state.
	var current domain.RunState
	err := tx.QueryRow(ctx,
		`SELECT state FROM runs WHERE id = $1 FOR UPDATE`,
		p.RunID,
	).Scan(&current)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.RunStateTransition{}, &ErrRunNotFound{RunID: p.RunID}
		}
		return domain.RunStateTransition{}, fmt.Errorf("statemachine: lock run %q: %w", p.RunID, err)
	}

	// Optional optimistic-concurrency check.
	if p.ExpectedFrom != "" && current != p.ExpectedFrom {
		return domain.RunStateTransition{}, &ErrStateMismatch{
			Expected: p.ExpectedFrom,
			Current:  current,
		}
	}

	// Legality check.
	if !CanTransition(current, p.To) {
		return domain.RunStateTransition{}, &ErrIllegalTransition{
			From: current,
			To:   p.To,
		}
	}

	// Apply state change.
	_, err = tx.Exec(ctx,
		`UPDATE runs SET state = $1, updated_at = now() WHERE id = $2`,
		string(p.To), p.RunID,
	)
	if err != nil {
		return domain.RunStateTransition{}, fmt.Errorf("statemachine: update run %q state: %w", p.RunID, err)
	}

	// Write audit row.
	var row domain.RunStateTransition
	var createdAt time.Time
	err = tx.QueryRow(ctx,
		`INSERT INTO run_state_transitions
		    (run_id, from_state, to_state, actor_type, actor_id, reason, payload_json)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, run_id, from_state, to_state, actor_type, actor_id, reason, payload_json, created_at`,
		p.RunID,
		string(current),
		string(p.To),
		string(p.ActorType),
		p.ActorID,
		p.Reason,
		payload,
	).Scan(
		&row.ID,
		&row.RunID,
		&row.FromState,
		&row.ToState,
		&row.ActorType,
		&row.ActorID,
		&row.Reason,
		&row.Payload,
		&createdAt,
	)
	if err != nil {
		return domain.RunStateTransition{}, fmt.Errorf("statemachine: insert transition audit row: %w", err)
	}
	row.CreatedAt = createdAt
	return row, nil
}
