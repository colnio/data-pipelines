package catalog

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/colnio/data-pipelines/internal/domain"
)

// ── Samples ───────────────────────────────────────────────────────────────────

// ListSamples returns a page of samples from v_samples ordered by
// (created_at ASC, id ASC), with keyset pagination via cursor.
func (s *Service) ListSamples(ctx context.Context, limit int, cursor string) ([]domain.Sample, string, error) {
	limit = clampLimit(limit)

	var rows pgx.Rows
	var err error
	if cursor == "" {
		rows, err = s.pool.Query(ctx, `
			SELECT id, material_stack, dielectric, fabrication_batch, params_json, notes, created_at
			FROM v_samples
			ORDER BY created_at ASC, id ASC
			LIMIT $1`, limit+1)
	} else {
		at, id, cerr := decodeCursor(cursor)
		if cerr != nil {
			return nil, "", cerr
		}
		rows, err = s.pool.Query(ctx, `
			SELECT id, material_stack, dielectric, fabrication_batch, params_json, notes, created_at
			FROM v_samples
			WHERE (created_at, id) > ($1, $2)
			ORDER BY created_at ASC, id ASC
			LIMIT $3`, at, id, limit+1)
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var out []domain.Sample
	for rows.Next() {
		var r domain.Sample
		var dielectric, params []byte
		if err := rows.Scan(&r.ID, &r.MaterialStack, &dielectric, &r.FabricationBatch,
			&params, &r.Notes, &r.CreatedAt); err != nil {
			return nil, "", err
		}
		r.Dielectric = dielectric
		r.ParamsJSON = params
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		nextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	return out, nextCursor, nil
}

// GetSample returns a single sample by id from v_samples.
func (s *Service) GetSample(ctx context.Context, id string) (domain.Sample, error) {
	var r domain.Sample
	var dielectric, params []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, material_stack, dielectric, fabrication_batch, params_json, notes, created_at
		FROM v_samples WHERE id = $1`, id).
		Scan(&r.ID, &r.MaterialStack, &dielectric, &r.FabricationBatch, &params, &r.Notes, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Sample{}, errNotFound("sample", id)
	}
	if err != nil {
		return domain.Sample{}, err
	}
	r.Dielectric = dielectric
	r.ParamsJSON = params
	return r, nil
}

// ── Devices ───────────────────────────────────────────────────────────────────

// ListDevices returns a page of devices, optionally filtered by sample_id.
func (s *Service) ListDevices(ctx context.Context, sampleID string, limit int, cursor string) ([]domain.Device, string, error) {
	limit = clampLimit(limit)

	var rows pgx.Rows
	var err error
	if cursor == "" {
		rows, err = s.pool.Query(ctx, `
			SELECT id, sample_id, device_class, fabrication_id, lifecycle_state, notes, created_at
			FROM v_devices
			WHERE ($1 = '' OR sample_id = $1)
			ORDER BY created_at ASC, id ASC
			LIMIT $2`, sampleID, limit+1)
	} else {
		at, id, cerr := decodeCursor(cursor)
		if cerr != nil {
			return nil, "", cerr
		}
		rows, err = s.pool.Query(ctx, `
			SELECT id, sample_id, device_class, fabrication_id, lifecycle_state, notes, created_at
			FROM v_devices
			WHERE ($1 = '' OR sample_id = $1)
			  AND (created_at, id) > ($2, $3)
			ORDER BY created_at ASC, id ASC
			LIMIT $4`, sampleID, at, id, limit+1)
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var out []domain.Device
	for rows.Next() {
		var r domain.Device
		if err := rows.Scan(&r.ID, &r.SampleID, &r.DeviceClass, &r.FabricationID,
			&r.LifecycleState, &r.Notes, &r.CreatedAt); err != nil {
			return nil, "", err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		nextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	return out, nextCursor, nil
}

// GetDevice returns a single device by id from v_devices.
func (s *Service) GetDevice(ctx context.Context, id string) (domain.Device, error) {
	var r domain.Device
	err := s.pool.QueryRow(ctx, `
		SELECT id, sample_id, device_class, fabrication_id, lifecycle_state, notes, created_at
		FROM v_devices WHERE id = $1`, id).
		Scan(&r.ID, &r.SampleID, &r.DeviceClass, &r.FabricationID, &r.LifecycleState, &r.Notes, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Device{}, errNotFound("device", id)
	}
	if err != nil {
		return domain.Device{}, err
	}
	return r, nil
}

// ── ContactConfigs ────────────────────────────────────────────────────────────

// ListContactConfigs returns a page of contact configs, optionally filtered by device_id.
func (s *Service) ListContactConfigs(ctx context.Context, deviceID string, limit int, cursor string) ([]domain.ContactConfig, string, error) {
	limit = clampLimit(limit)

	var rows pgx.Rows
	var err error
	if cursor == "" {
		rows, err = s.pool.Query(ctx, `
			SELECT id, device_id, terminal_roles_json, is_default, notes, created_at
			FROM v_contact_configs
			WHERE ($1 = '' OR device_id = $1)
			ORDER BY created_at ASC, id ASC
			LIMIT $2`, deviceID, limit+1)
	} else {
		at, id, cerr := decodeCursor(cursor)
		if cerr != nil {
			return nil, "", cerr
		}
		rows, err = s.pool.Query(ctx, `
			SELECT id, device_id, terminal_roles_json, is_default, notes, created_at
			FROM v_contact_configs
			WHERE ($1 = '' OR device_id = $1)
			  AND (created_at, id) > ($2, $3)
			ORDER BY created_at ASC, id ASC
			LIMIT $4`, deviceID, at, id, limit+1)
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var out []domain.ContactConfig
	for rows.Next() {
		var r domain.ContactConfig
		var termRoles []byte
		if err := rows.Scan(&r.ID, &r.DeviceID, &termRoles, &r.IsDefault, &r.Notes, &r.CreatedAt); err != nil {
			return nil, "", err
		}
		r.TerminalRolesJSON = termRoles
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		nextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	return out, nextCursor, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func clampLimit(n int) int {
	if n <= 0 || n > 200 {
		return 50
	}
	return n
}

// notFoundErr is a sentinel used by the catalog repo so api.go can convert it
// to a platform.NotFound response.
type notFoundErr struct {
	kind string
	id   string
}

func (e *notFoundErr) Error() string { return e.kind + " not found: " + e.id }

func errNotFound(kind, id string) error { return &notFoundErr{kind: kind, id: id} }
