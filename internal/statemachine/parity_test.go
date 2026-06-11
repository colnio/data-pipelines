package statemachine_test

import (
	"context"
	"testing"

	"github.com/colnio/data-pipelines/internal/domain"
	"github.com/colnio/data-pipelines/internal/statemachine"
	"github.com/colnio/data-pipelines/internal/testsupport"
)

// TestAllowedTransitionsParity guards the cross-language transition contract:
// the Go allowed-transition graph (internal/statemachine, used by the Go
// pipeline) MUST equal the seeded allowed_transitions table (migration 00100,
// used by the SQL transition_run() that Python workers call). If they drift, a
// transition legal in one language is illegal in the other.
func TestAllowedTransitionsParity(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()

	rows, err := pool.Query(ctx, `SELECT from_state, to_state FROM allowed_transitions`)
	if err != nil {
		t.Fatalf("query allowed_transitions: %v", err)
	}
	defer rows.Close()

	sqlEdges := map[[2]string]bool{}
	for rows.Next() {
		var from, to string
		if err := rows.Scan(&from, &to); err != nil {
			t.Fatal(err)
		}
		sqlEdges[[2]string{from, to}] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	// Every (from,to) over all known states must agree between Go and SQL.
	for _, from := range domain.AllRunStates {
		for _, to := range domain.AllRunStates {
			goLegal := statemachine.CanTransition(from, to)
			sqlLegal := sqlEdges[[2]string{string(from), string(to)}]
			if goLegal != sqlLegal {
				t.Errorf("transition %s -> %s: Go=%v SQL=%v (graphs drifted)", from, to, goLegal, sqlLegal)
			}
		}
	}
}
