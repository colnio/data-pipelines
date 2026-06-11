// Package statemachine is the ONLY writer of runs.state. It validates
// transition legality against the graph defined in architecture §14 and writes
// an audit row to run_state_transitions for every change (§16).
package statemachine

import "github.com/colnio/data-pipelines/internal/domain"

// allowed encodes the directed graph of valid run state transitions (§14).
//
// Terminal states (no outgoing edges):
//   - published       – normal end state; run is publicly accessible.
//   - unsafe_archive  – security halt; requires manual intervention outside
//     the normal lifecycle.
//   - review_rejected – reviewer permanently rejected the run; no recovery
//     path is defined in the automated workflow.
var allowed = map[domain.RunState][]domain.RunState{
	// Happy path: file transfer
	domain.StateDeclared: {
		domain.StatePulling,
		domain.StateTransferFailed,
	},
	domain.StatePulling: {
		domain.StateUnpacked,
		domain.StateTransferFailed,
	},
	domain.StateUnpacked: {
		domain.StateVerified,
		domain.StateUnsafeArchive,
		domain.StateManifestMismatch,
	},
	domain.StateVerified: {
		domain.StatePromoted,
		domain.StateManifestMismatch,
	},

	// Happy path: ingest pipeline
	domain.StatePromoted: {
		domain.StateNeedsMetadata,
		domain.StateParsing,
	},
	domain.StateNeedsMetadata: {
		domain.StateParsing,
	},
	domain.StateParsing: {
		domain.StateValidated,
		domain.StateParserFailed,
		domain.StateQuarantined,
	},
	domain.StateValidated: {
		domain.StateProcessing,
	},
	domain.StateProcessing: {
		domain.StateAwaitingReview,
		domain.StateProcessingFailed,
	},
	domain.StateAwaitingReview: {
		domain.StateApproved,
		domain.StateChangesRequested,
		domain.StateQuarantined,
		domain.StateReviewRejected,
	},
	domain.StateChangesRequested: {
		domain.StateProcessing,
		domain.StateAwaitingReview,
	},
	domain.StateApproved: {
		domain.StatePublishing,
	},
	domain.StatePublishing: {
		domain.StatePublished,
		domain.StateProcessingFailed,
	},

	// Recovery / re-drive edges
	domain.StateTransferFailed: {
		domain.StatePulling, // re-drive
	},
	domain.StateManifestMismatch: {
		domain.StatePulling, // re-pull
	},
	domain.StateParserFailed: {
		domain.StateParsing, // re-drive after fix
	},
	domain.StateProcessingFailed: {
		domain.StateProcessing,
	},
	domain.StateQuarantined: {
		domain.StateParsing,
		domain.StateProcessing, // after manual clearing
	},

	// Terminal states have no outgoing edges and are intentionally absent:
	//   domain.StatePublished       (normal end)
	//   domain.StateUnsafeArchive   (security halt)
	//   domain.StateReviewRejected  (permanent rejection)
}

// CanTransition reports whether the transition from → to is legal per §14.
func CanTransition(from, to domain.RunState) bool {
	for _, s := range allowed[from] {
		if s == to {
			return true
		}
	}
	return false
}

// NextStates returns the states reachable from from, or nil if from is
// terminal or unknown.
func NextStates(from domain.RunState) []domain.RunState {
	return allowed[from]
}
