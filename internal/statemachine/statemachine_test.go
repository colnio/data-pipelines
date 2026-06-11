package statemachine_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/colnio/data-pipelines/internal/domain"
	"github.com/colnio/data-pipelines/internal/statemachine"
	"github.com/colnio/data-pipelines/internal/testsupport"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Pure logic tests — no DB required
// ---------------------------------------------------------------------------

func TestCanTransition_LegalPairs(t *testing.T) {
	legal := [][2]domain.RunState{
		// Happy path
		{domain.StateDeclared, domain.StatePulling},
		{domain.StatePulling, domain.StateUnpacked},
		{domain.StateUnpacked, domain.StateVerified},
		{domain.StateVerified, domain.StatePromoted},
		{domain.StatePromoted, domain.StateNeedsMetadata},
		{domain.StatePromoted, domain.StateParsing},
		{domain.StateNeedsMetadata, domain.StateParsing},
		{domain.StateParsing, domain.StateValidated},
		{domain.StateValidated, domain.StateProcessing},
		{domain.StateProcessing, domain.StateAwaitingReview},
		{domain.StateAwaitingReview, domain.StateApproved},
		{domain.StateApproved, domain.StatePublishing},
		{domain.StatePublishing, domain.StatePublished},
		// Recovery edges
		{domain.StateDeclared, domain.StateTransferFailed},
		{domain.StatePulling, domain.StateTransferFailed},
		{domain.StateUnpacked, domain.StateUnsafeArchive},
		{domain.StateUnpacked, domain.StateManifestMismatch},
		{domain.StateVerified, domain.StateManifestMismatch},
		{domain.StateParsing, domain.StateParserFailed},
		{domain.StateParsing, domain.StateQuarantined},
		{domain.StateProcessing, domain.StateProcessingFailed},
		{domain.StateAwaitingReview, domain.StateChangesRequested},
		{domain.StateAwaitingReview, domain.StateQuarantined},
		{domain.StateAwaitingReview, domain.StateReviewRejected},
		{domain.StateChangesRequested, domain.StateProcessing},
		{domain.StateChangesRequested, domain.StateAwaitingReview},
		{domain.StatePublishing, domain.StateProcessingFailed},
		{domain.StateTransferFailed, domain.StatePulling},
		{domain.StateManifestMismatch, domain.StatePulling},
		{domain.StateParserFailed, domain.StateParsing},
		{domain.StateProcessingFailed, domain.StateProcessing},
		{domain.StateQuarantined, domain.StateParsing},
		{domain.StateQuarantined, domain.StateProcessing},
	}
	for _, pair := range legal {
		from, to := pair[0], pair[1]
		assert.True(t, statemachine.CanTransition(from, to),
			"expected legal transition %s → %s", from, to)
	}
}

func TestCanTransition_IllegalPairs(t *testing.T) {
	illegal := [][2]domain.RunState{
		{domain.StateDeclared, domain.StatePublished},
		{domain.StateDeclared, domain.StateVerified},
		{domain.StatePulling, domain.StatePromoted},
		{domain.StatePublished, domain.StateDeclared},
		{domain.StateUnsafeArchive, domain.StatePulling},
		{domain.StateReviewRejected, domain.StateProcessing},
		{domain.StateApproved, domain.StateReviewRejected},
	}
	for _, pair := range illegal {
		from, to := pair[0], pair[1]
		assert.False(t, statemachine.CanTransition(from, to),
			"expected illegal transition %s → %s", from, to)
	}
}

func TestNextStates_TerminalStates(t *testing.T) {
	terminals := []domain.RunState{
		domain.StatePublished,
		domain.StateUnsafeArchive,
		domain.StateReviewRejected,
	}
	for _, s := range terminals {
		next := statemachine.NextStates(s)
		assert.Empty(t, next, "terminal state %s should have no outgoing edges", s)
	}
}

func TestNextStates_NonTerminal(t *testing.T) {
	// declared has exactly pulling and transfer_failed
	next := statemachine.NextStates(domain.StateDeclared)
	assert.ElementsMatch(t, []domain.RunState{
		domain.StatePulling,
		domain.StateTransferFailed,
	}, next)
}

// ---------------------------------------------------------------------------
// DB tests
// ---------------------------------------------------------------------------

// seedRun inserts a prerequisite agent and a run in 'declared' state, returning
// the run ID. It also registers a t.Cleanup to truncate the affected tables.
func seedRun(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()

	agentID := "test-agent-sm-01"
	runID := "run-sm-test-" + t.Name()

	// Insert agent (key_hash can be any non-empty string for tests).
	_, err := pool.Exec(ctx,
		`INSERT INTO agents (id, display_name, key_hash, enabled)
		 VALUES ($1, 'SM Test Agent', 'x', true)
		 ON CONFLICT (id) DO NOTHING`,
		agentID,
	)
	require.NoError(t, err, "seed agent")

	// Insert run in declared state.
	_, err = pool.Exec(ctx,
		`INSERT INTO runs
		    (id, manifest_hash, agent_id, completion_source, meas_path, declared_at, state)
		 VALUES ($1, 'hash-abc', $2, 'instrument', '/data/test', $3, 'declared')`,
		runID, agentID, time.Now(),
	)
	require.NoError(t, err, "seed run")

	t.Cleanup(func() {
		testsupport.Truncate(t, pool,
			"run_state_transitions",
			"runs",
			"agents",
		)
	})

	return runID
}

func TestTransition_HappyPath(t *testing.T) {
	pool := testsupport.NewPool(t)
	runID := seedRun(t, pool)
	ctx := context.Background()

	row, err := statemachine.Transition(ctx, pool, statemachine.TransitionParams{
		RunID:     runID,
		To:        domain.StatePulling,
		ActorType: domain.ActorWorker,
		ActorID:   "worker-1",
		Reason:    "pull job dispatched",
	})
	require.NoError(t, err)

	// Returned row has correct from/to.
	assert.Equal(t, domain.StateDeclared, row.FromState)
	assert.Equal(t, domain.StatePulling, row.ToState)
	assert.Equal(t, runID, row.RunID)
	assert.Greater(t, row.ID, int64(0))

	// runs.state was updated.
	state, err := statemachine.CurrentState(ctx, pool, runID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatePulling, state)

	// Audit row exists in DB.
	var count int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM run_state_transitions WHERE run_id = $1`, runID,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestTransition_ExpectedFromMismatch(t *testing.T) {
	pool := testsupport.NewPool(t)
	runID := seedRun(t, pool)
	ctx := context.Background()

	// Expect 'verified' but the run is in 'declared'.
	_, err := statemachine.Transition(ctx, pool, statemachine.TransitionParams{
		RunID:        runID,
		ExpectedFrom: domain.StateVerified,
		To:           domain.StatePromoted,
		ActorType:    domain.ActorWorker,
		ActorID:      "worker-1",
	})
	require.Error(t, err)

	var mismatch *statemachine.ErrStateMismatch
	require.ErrorAs(t, err, &mismatch, "should be ErrStateMismatch")
	assert.Equal(t, domain.StateVerified, mismatch.Expected)
	assert.Equal(t, domain.StateDeclared, mismatch.Current)

	// State unchanged.
	state, err := statemachine.CurrentState(ctx, pool, runID)
	require.NoError(t, err)
	assert.Equal(t, domain.StateDeclared, state)

	// No audit row written.
	var count int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM run_state_transitions WHERE run_id = $1`, runID,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestTransition_IllegalTransition(t *testing.T) {
	pool := testsupport.NewPool(t)
	runID := seedRun(t, pool)
	ctx := context.Background()

	// declared → published is not in the allowed graph.
	_, err := statemachine.Transition(ctx, pool, statemachine.TransitionParams{
		RunID:     runID,
		To:        domain.StatePublished,
		ActorType: domain.ActorSystem,
		ActorID:   "system",
	})
	require.Error(t, err)

	var illegal *statemachine.ErrIllegalTransition
	require.ErrorAs(t, err, &illegal, "should be ErrIllegalTransition")
	assert.Equal(t, domain.StateDeclared, illegal.From)
	assert.Equal(t, domain.StatePublished, illegal.To)

	// State unchanged.
	state, err := statemachine.CurrentState(ctx, pool, runID)
	require.NoError(t, err)
	assert.Equal(t, domain.StateDeclared, state)

	// No audit row written.
	var count int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM run_state_transitions WHERE run_id = $1`, runID,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestTransition_MultipleTransitionsAppendAuditRows(t *testing.T) {
	pool := testsupport.NewPool(t)
	runID := seedRun(t, pool)
	ctx := context.Background()

	// First transition: declared → pulling
	row1, err := statemachine.Transition(ctx, pool, statemachine.TransitionParams{
		RunID:     runID,
		To:        domain.StatePulling,
		ActorType: domain.ActorWorker,
		ActorID:   "worker-1",
		Reason:    "first transition",
	})
	require.NoError(t, err)
	assert.Equal(t, domain.StateDeclared, row1.FromState)
	assert.Equal(t, domain.StatePulling, row1.ToState)

	// Second transition: pulling → unpacked
	row2, err := statemachine.Transition(ctx, pool, statemachine.TransitionParams{
		RunID:     runID,
		To:        domain.StateUnpacked,
		ActorType: domain.ActorWorker,
		ActorID:   "worker-1",
		Reason:    "second transition",
	})
	require.NoError(t, err)
	assert.Equal(t, domain.StatePulling, row2.FromState)
	assert.Equal(t, domain.StateUnpacked, row2.ToState)

	// Two audit rows exist.
	var count int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM run_state_transitions WHERE run_id = $1`, runID,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// runs.state is now unpacked.
	state, err := statemachine.CurrentState(ctx, pool, runID)
	require.NoError(t, err)
	assert.Equal(t, domain.StateUnpacked, state)
}
