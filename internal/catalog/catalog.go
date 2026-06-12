// Package catalog owns the browse API for the catalog hierarchy:
// samples → devices → contact_configs. All reads go through the read-only
// views (v_samples, v_devices, v_contact_configs) defined in
// migrations/00110_catalog_views.sql. No writes are performed here; the
// catalog is managed via direct DB migrations or a separate admin surface.
package catalog

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielgtaylor/huma/v2"
)

// Service is the catalog module's stateful core, holding the DB pool.
type Service struct {
	pool *pgxpool.Pool
}

// NewService constructs a catalog Service.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// Register wires the catalog browse endpoints onto the huma API.
func Register(api huma.API, svc *Service) {
	registerSamples(api, svc)
	registerDevices(api, svc)
	registerContactConfigs(api, svc)
}

// ── cursor helpers ────────────────────────────────────────────────────────────

// encodeCursor produces an opaque, URL-safe base64 cursor from a created_at
// timestamp and an id string. Format: base64(RFC3339Nano + "|" + id).
func encodeCursor(createdAt time.Time, id string) string {
	raw := createdAt.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor parses a cursor produced by encodeCursor.
func decodeCursor(cursor string) (createdAt time.Time, id string, err error) {
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor: %w", err)
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("invalid cursor format")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor timestamp: %w", err)
	}
	return t, parts[1], nil
}
