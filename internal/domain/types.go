package domain

import (
	"encoding/json"
	"time"
)

// Run mirrors the runs table: one declared measurement event (§7). Structural
// truth (sample/device/contact_config) is referenced by ID; the manifest hash
// freezes the declared file set.
type Run struct {
	ID               string           `json:"id"`
	ManifestHash     string           `json:"manifest_hash"`
	AgentID          string           `json:"agent_id"`
	MeasurementType  string           `json:"measurement_type"`
	CompletionSource CompletionSource `json:"completion_source"`
	SampleID         *string          `json:"sample_id,omitempty"`
	DeviceID         *string          `json:"device_id,omitempty"`
	ContactConfigID  *string          `json:"contact_config_id,omitempty"`
	MeasPath         string           `json:"meas_path"`
	OperatorComment  string           `json:"operator_comment"`
	State            RunState         `json:"state"`
	DeclaredBy       string           `json:"declared_by"`
	DeclaredAt       time.Time        `json:"declared_at"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
}

// RunFile mirrors run_files: one verified raw file belonging to a run.
type RunFile struct {
	ID        int64     `json:"id"`
	RunID     string    `json:"run_id"`
	Name      string    `json:"name"`
	Bytes     int64     `json:"bytes"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
}

// Job mirrors a row in the durable Postgres jobs queue (§15). It is keyed by an
// idempotency key so retries and duplicate notifications are safe.
type Job struct {
	ID             int64           `json:"id"`
	JobType        JobType         `json:"job_type"`
	RunID          *string         `json:"run_id,omitempty"`
	State          JobState        `json:"state"`
	Priority       int             `json:"priority"`
	AttemptCount   int             `json:"attempt_count"`
	MaxAttempts    int             `json:"max_attempts"`
	AvailableAt    time.Time       `json:"available_at"`
	LockedBy       *string         `json:"locked_by,omitempty"`
	LockedUntil    *time.Time      `json:"locked_until,omitempty"`
	IdempotencyKey string          `json:"idempotency_key"`
	Payload        json.RawMessage `json:"payload_json,omitempty"`
	Result         json.RawMessage `json:"result_json,omitempty"`
	LastError      *string         `json:"last_error,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	FinishedAt     *time.Time      `json:"finished_at,omitempty"`
}

// Agent mirrors the agents table: a measurement-PC agent the server trusts but
// does not consider infallible (§4).
type Agent struct {
	ID          string     `json:"id"`
	DisplayName string     `json:"display_name"`
	KeyHash     string     `json:"-"` // argon2/bcrypt hash of the shared key; never serialized
	Enabled     bool       `json:"enabled"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// AgentAllowedRoot is one filesystem root an agent is permitted to expose. A
// manifest meas_path is valid only if it resolves under one of these (§4, §13).
type AgentAllowedRoot struct {
	ID      int64  `json:"id"`
	AgentID string `json:"agent_id"`
	Root    string `json:"root"`
}

// RunStateTransition mirrors run_state_transitions: the append-only audit log
// of every state change (§16).
type RunStateTransition struct {
	ID        int64           `json:"id"`
	RunID     string          `json:"run_id"`
	FromState RunState        `json:"from_state"`
	ToState   RunState        `json:"to_state"`
	ActorType ActorType       `json:"actor_type"`
	ActorID   string          `json:"actor_id"`
	Reason    string          `json:"reason"`
	Payload   json.RawMessage `json:"payload_json,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}
