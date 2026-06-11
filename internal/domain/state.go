// Package domain holds the shared types, enums, and the manifest contract used
// across the lab-data server: API, jobs queue, state machine, transfer, and
// agent auth. It deliberately depends on nothing else in the project so every
// other internal package can import it without creating cycles.
package domain

// RunState is the lifecycle state of a single declared measurement run.
//
// The happy path is:
//
//	declared → pulling → unpacked → verified → promoted
//	  → (needs_metadata) → parsing → validated → processing
//	  → awaiting_review → approved → publishing → published
//
// changes_requested loops back into processing/awaiting_review. The remaining
// constants are failure / holding states. See architecture §14.
type RunState string

const (
	StateDeclared         RunState = "declared"
	StatePulling          RunState = "pulling"
	StateUnpacked         RunState = "unpacked"
	StateVerified         RunState = "verified"
	StatePromoted         RunState = "promoted"
	StateNeedsMetadata    RunState = "needs_metadata"
	StateParsing          RunState = "parsing"
	StateValidated        RunState = "validated"
	StateProcessing       RunState = "processing"
	StateAwaitingReview   RunState = "awaiting_review"
	StateApproved         RunState = "approved"
	StatePublishing       RunState = "publishing"
	StatePublished        RunState = "published"
	StateChangesRequested RunState = "changes_requested"

	// Failure / holding states.
	StateTransferFailed   RunState = "transfer_failed"
	StateManifestMismatch RunState = "manifest_mismatch"
	StateUnsafeArchive    RunState = "unsafe_archive"
	StateParserFailed     RunState = "parser_failed"
	StateQuarantined      RunState = "quarantined"
	StateProcessingFailed RunState = "processing_failed"
	StateReviewRejected   RunState = "review_rejected"
)

// AllRunStates lists every valid run state. Used for DB enum/constraint checks
// and validation.
var AllRunStates = []RunState{
	StateDeclared, StatePulling, StateUnpacked, StateVerified, StatePromoted,
	StateNeedsMetadata, StateParsing, StateValidated, StateProcessing,
	StateAwaitingReview, StateApproved, StatePublishing, StatePublished,
	StateChangesRequested, StateTransferFailed, StateManifestMismatch,
	StateUnsafeArchive, StateParserFailed, StateQuarantined,
	StateProcessingFailed, StateReviewRejected,
}

// Valid reports whether s is a known run state.
func (s RunState) Valid() bool {
	for _, v := range AllRunStates {
		if v == s {
			return true
		}
	}
	return false
}

// JobState is the lifecycle state of a row in the durable jobs queue (§15).
type JobState string

const (
	JobQueued    JobState = "queued"
	JobRunning   JobState = "running"
	JobSucceeded JobState = "succeeded"
	JobFailed    JobState = "failed"
	JobDead      JobState = "dead"
)

// JobType identifies what a queued job does. See architecture §15.
type JobType string

const (
	JobPullRun              JobType = "pull_run"
	JobVerifyRun            JobType = "verify_run"
	JobPromoteRun           JobType = "promote_run"
	JobParseRun             JobType = "parse_run"
	JobValidateRun          JobType = "validate_run"
	JobProcessRun           JobType = "process_run"
	JobCreateReviewArtifact JobType = "create_review_artifact"
	JobSendNotification     JobType = "send_notification"
	JobPublishRun           JobType = "publish_run"
	JobReconcileAgent       JobType = "reconcile_agent"
	JobBackupCheck          JobType = "backup_check"
)

// ActorType identifies who/what performed a state transition (§16).
type ActorType string

const (
	ActorAgent    ActorType = "agent"
	ActorWorker   ActorType = "worker"
	ActorAPI      ActorType = "api"
	ActorReviewer ActorType = "reviewer"
	ActorSystem   ActorType = "system"
)

// CompletionSource records which door a run came through (§12). Both converge
// on the same finalize_run / manifest contract.
type CompletionSource string

const (
	SourceInstrument CompletionSource = "instrument"
	SourceOperator   CompletionSource = "operator"
)
