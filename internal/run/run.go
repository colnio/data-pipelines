// Package run owns the runs, run_files, manifests, and instrument_metadata_raw
// tables: idempotent run creation at declaration time, file recording at
// promotion time, and the browse/read API. runs.state is NEVER written here —
// that is the statemachine package's exclusive job (architecture §16).
package run

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/colnio/data-pipelines/internal/db"
	"github.com/colnio/data-pipelines/internal/domain"
)

// ErrNotFound is returned when a run does not exist.
var ErrNotFound = errors.New("run not found")

// Repo is the data-access layer for runs and their immediate children.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo constructs a run repository over the given pool.
func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// CreateParams carries the fields needed to declare a run, derived from a
// validated manifest plus its computed manifest hash.
type CreateParams struct {
	RunID            string
	ManifestHash     string
	AgentID          string
	MeasurementType  string
	CompletionSource domain.CompletionSource
	SampleID         *string
	DeviceID         *string
	ContactConfigID  *string
	MeasPath         string
	OperatorComment  string
	DeclaredBy       string
	DeclaredAt       time.Time
	ConditionLabels  []string
	SchemaVersion    int
	// CanonicalManifest is the deterministic JSON stored in manifests.raw_json.
	CanonicalManifest json.RawMessage
}

// Create declares a run in state 'declared' together with its manifest row and
// condition-label links, idempotently. If a run with the same id already
// exists it is returned unchanged with created=false (a duplicate manifest POST
// or a reconciliation re-drive is harmless). The manifest row is upserted on
// (run_id, manifest_hash) so a re-declared identical manifest conflicts
// harmlessly; a NEW manifest hash for the same run is recorded alongside the
// old one (instrument-supersedes-operator is then visible and auditable, §14).
func (r *Repo) Create(ctx context.Context, p CreateParams) (run domain.Run, created bool, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Run{}, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	tag, err := tx.Exec(ctx, `
		INSERT INTO runs (
			id, manifest_hash, agent_id, measurement_type, completion_source,
			sample_id, device_id, contact_config_id, meas_path, operator_comment,
			state, declared_by, declared_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'declared',$11,$12)
		ON CONFLICT (id) DO NOTHING`,
		p.RunID, p.ManifestHash, p.AgentID, p.MeasurementType, string(p.CompletionSource),
		p.SampleID, p.DeviceID, p.ContactConfigID, p.MeasPath, p.OperatorComment,
		p.DeclaredBy, p.DeclaredAt,
	)
	if err != nil {
		return domain.Run{}, false, fmt.Errorf("insert run: %w", err)
	}
	created = tag.RowsAffected() == 1

	// Record the manifest (idempotent on (run_id, manifest_hash)).
	if _, err := tx.Exec(ctx, `
		INSERT INTO manifests (run_id, manifest_hash, schema_version, completion_source, declared_by, declared_at, raw_json)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (run_id, manifest_hash) DO NOTHING`,
		p.RunID, p.ManifestHash, p.SchemaVersion, string(p.CompletionSource),
		p.DeclaredBy, p.DeclaredAt, []byte(p.CanonicalManifest),
	); err != nil {
		return domain.Run{}, false, fmt.Errorf("insert manifest: %w", err)
	}

	if created {
		for _, label := range p.ConditionLabels {
			var labelID int64
			if err := tx.QueryRow(ctx, `
				INSERT INTO condition_labels (canonical_name) VALUES ($1)
				ON CONFLICT (canonical_name) DO UPDATE SET canonical_name = EXCLUDED.canonical_name
				RETURNING id`, label).Scan(&labelID); err != nil {
				return domain.Run{}, false, fmt.Errorf("upsert condition label: %w", err)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO run_condition_labels (run_id, condition_label_id) VALUES ($1,$2)
				ON CONFLICT DO NOTHING`, p.RunID, labelID); err != nil {
				return domain.Run{}, false, fmt.Errorf("link condition label: %w", err)
			}
		}
	}

	run, err = getRunTx(ctx, tx, p.RunID)
	if err != nil {
		return domain.Run{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Run{}, false, err
	}
	return run, created, nil
}

// Manifest reconstructs the most recently recorded manifest for a run from its
// stored canonical JSON. The pipeline needs the file list, archive name, and
// instrument_json reference to drive transfer/verification.
func (r *Repo) Manifest(ctx context.Context, runID string) (domain.Manifest, error) {
	var raw []byte
	err := r.pool.QueryRow(ctx, `
		SELECT raw_json FROM manifests WHERE run_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`,
		runID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Manifest{}, ErrNotFound
	}
	if err != nil {
		return domain.Manifest{}, err
	}
	var m domain.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return domain.Manifest{}, fmt.Errorf("decode stored manifest: %w", err)
	}
	return m, nil
}

// InsertFiles records verified raw files for a run. Idempotent on (run_id,
// name): re-running the promote step does not duplicate rows. Accepts any
// db.DBTX so it can run inside the same transaction as the promote transition.
func (r *Repo) InsertFiles(ctx context.Context, q db.DBTX, runID string, files []domain.RunFile) error {
	for _, f := range files {
		if _, err := q.Exec(ctx, `
			INSERT INTO run_files (run_id, name, bytes, sha256) VALUES ($1,$2,$3,$4)
			ON CONFLICT (run_id, name) DO NOTHING`,
			runID, f.Name, f.Bytes, f.SHA256); err != nil {
			return fmt.Errorf("insert run_file %q: %w", f.Name, err)
		}
	}
	return nil
}

// RecordInstrumentJSON stores the per-run machine-generated instrument metadata
// (immutable, hashed). Idempotent on (run_id, filename).
func (r *Repo) RecordInstrumentJSON(ctx context.Context, q db.DBTX, runID, filename, sha256 string, raw json.RawMessage) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`null`)
	}
	_, err := q.Exec(ctx, `
		INSERT INTO instrument_metadata_raw (run_id, filename, sha256, raw_json) VALUES ($1,$2,$3,$4)
		ON CONFLICT (run_id, filename) DO NOTHING`,
		runID, filename, sha256, []byte(raw))
	return err
}

// Get returns a single run by id.
func (r *Repo) Get(ctx context.Context, runID string) (domain.Run, error) {
	return getRunTx(ctx, r.pool, runID)
}

// Files returns the recorded raw files for a run.
func (r *Repo) Files(ctx context.Context, runID string) ([]domain.RunFile, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, run_id, name, bytes, sha256, created_at
		FROM run_files WHERE run_id = $1 ORDER BY name`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.RunFile
	for rows.Next() {
		var f domain.RunFile
		if err := rows.Scan(&f.ID, &f.RunID, &f.Name, &f.Bytes, &f.SHA256, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Transitions returns the audit trail for a run, oldest first.
func (r *Repo) Transitions(ctx context.Context, runID string) ([]domain.RunStateTransition, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, run_id, from_state, to_state, actor_type, actor_id, reason, payload_json, created_at
		FROM run_state_transitions WHERE run_id = $1 ORDER BY created_at, id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.RunStateTransition
	for rows.Next() {
		var t domain.RunStateTransition
		var payload []byte
		if err := rows.Scan(&t.ID, &t.RunID, &t.FromState, &t.ToState, &t.ActorType, &t.ActorID, &t.Reason, &payload, &t.CreatedAt); err != nil {
			return nil, err
		}
		t.Payload = payload
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListFilter narrows a run listing for the browse/search UI (architecture §19).
type ListFilter struct {
	// Existing filters (additive — do not remove).
	State           string
	SampleID        string
	DeviceID        string
	MeasurementType string
	AgentID         string
	Limit           int

	// New filters added for keyset pagination and richer search.
	ConditionLabel   string    // filter by condition_labels.canonical_name
	DeclaredAfter    time.Time // declared_at >= this value (zero = absent)
	DeclaredBefore   time.Time // declared_at <= this value (zero = absent)
	PublicationStatus string   // "published" | "unpublished" | "" (absent)
	Cursor           string    // opaque keyset cursor (base64 of declaredAt|id)
}

// EncodeCursor encodes a declared_at timestamp and a run id into an opaque,
// URL-safe base64 cursor. Format: base64(RFC3339Nano + "|" + id).
func EncodeCursor(declaredAt time.Time, id string) string {
	raw := declaredAt.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor parses a cursor produced by EncodeCursor.
func decodeCursor(cursor string) (declaredAt time.Time, id string, err error) {
	b, decErr := base64.RawURLEncoding.DecodeString(cursor)
	if decErr != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor: %w", decErr)
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("invalid cursor format")
	}
	t, parseErr := time.Parse(time.RFC3339Nano, parts[0])
	if parseErr != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor timestamp: %w", parseErr)
	}
	return t, parts[1], nil
}

// List returns runs newest-first (declared_at DESC, id DESC) matching the
// filter. Returns the slice, an opaque next_cursor (non-empty only when a full
// page was returned), and any error.
func (r *Repo) List(ctx context.Context, f ListFilter) ([]domain.Run, string, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	// Keyset pagination: decode cursor when provided.
	var cursorAt time.Time
	var cursorID string
	if f.Cursor != "" {
		var cerr error
		cursorAt, cursorID, cerr = decodeCursor(f.Cursor)
		if cerr != nil {
			return nil, "", fmt.Errorf("list runs: %w", cerr)
		}
	}

	// Nullable timestamp args: pass nil (pgx treats nil as NULL) for zero times.
	var afterArg, beforeArg interface{}
	if !f.DeclaredAfter.IsZero() {
		afterArg = f.DeclaredAfter
	}
	if !f.DeclaredBefore.IsZero() {
		beforeArg = f.DeclaredBefore
	}

	// Build publication_status conditions inline via string matching on state.
	// We express this as: published → state = 'published';
	//                      unpublished → state <> 'published';
	//                      else → no constraint.
	// We pass the status string and handle it with a CASE expression.
	pubStatus := f.PublicationStatus // "published" | "unpublished" | ""

	// Cursor clause: when a cursor is present, restrict to rows older than the
	// cursor position.
	var cursorAtArg interface{}
	cursorIDArg := cursorID
	if !cursorAt.IsZero() {
		cursorAtArg = cursorAt
	}

	rows, err := r.pool.Query(ctx, `
		SELECT id, manifest_hash, agent_id, measurement_type, completion_source,
		       sample_id, device_id, contact_config_id, meas_path, operator_comment,
		       state, declared_by, declared_at, created_at, updated_at
		FROM runs
		WHERE ($1 = '' OR state = $1)
		  AND ($2 = '' OR sample_id = $2)
		  AND ($3 = '' OR device_id = $3)
		  AND ($4 = '' OR measurement_type = $4)
		  AND ($5 = '' OR agent_id = $5)
		  AND ($6 = '' OR EXISTS (
		        SELECT 1 FROM run_condition_labels rcl
		        JOIN condition_labels cl ON cl.id = rcl.condition_label_id
		        WHERE rcl.run_id = runs.id AND cl.canonical_name = $6
		  ))
		  AND ($7::timestamptz IS NULL OR declared_at >= $7)
		  AND ($8::timestamptz IS NULL OR declared_at <= $8)
		  AND ($9 = '' OR (
		        CASE $9
		          WHEN 'published'   THEN state = 'published'
		          WHEN 'unpublished' THEN state <> 'published'
		          ELSE true
		        END
		  ))
		  AND ($10::timestamptz IS NULL OR (declared_at, id) < ($10, $11))
		ORDER BY declared_at DESC, id DESC
		LIMIT $12`,
		f.State, f.SampleID, f.DeviceID, f.MeasurementType, f.AgentID,
		f.ConditionLabel,
		afterArg, beforeArg,
		pubStatus,
		cursorAtArg, cursorIDArg,
		limit+1,
	)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []domain.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, run)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		nextCursor = EncodeCursor(last.DeclaredAt, last.ID)
	}
	return out, nextCursor, nil
}

// ── internal scan helpers ────────────────────────────────────────────────────

func getRunTx(ctx context.Context, q db.DBTX, runID string) (domain.Run, error) {
	row := q.QueryRow(ctx, `
		SELECT id, manifest_hash, agent_id, measurement_type, completion_source,
		       sample_id, device_id, contact_config_id, meas_path, operator_comment,
		       state, declared_by, declared_at, created_at, updated_at
		FROM runs WHERE id = $1`, runID)
	run, err := scanRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Run{}, ErrNotFound
	}
	return run, err
}

type scannable interface {
	Scan(dest ...any) error
}

func scanRun(row scannable) (domain.Run, error) {
	var r domain.Run
	var cs string
	if err := row.Scan(
		&r.ID, &r.ManifestHash, &r.AgentID, &r.MeasurementType, &cs,
		&r.SampleID, &r.DeviceID, &r.ContactConfigID, &r.MeasPath, &r.OperatorComment,
		&r.State, &r.DeclaredBy, &r.DeclaredAt, &r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return domain.Run{}, err
	}
	r.CompletionSource = domain.CompletionSource(cs)
	return r, nil
}
