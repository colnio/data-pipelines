package agentauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/colnio/data-pipelines/internal/platform"
)

// AgentAdminRow is the admin view of an agent, including its allowed roots.
type AgentAdminRow struct {
	ID           string    `json:"id"`
	DisplayName  string    `json:"display_name"`
	Enabled      bool      `json:"enabled"`
	AllowedRoots []string  `json:"allowed_roots"`
	LastSeenAt   *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// ListAgents returns all agents with their allowed roots.
func (s *Service) ListAgents(ctx context.Context) ([]AgentAdminRow, error) {
	const agentQuery = `
		SELECT id, display_name, enabled, last_seen_at, created_at
		FROM agents
		ORDER BY created_at DESC`

	rows, err := s.pool.Query(ctx, agentQuery)
	if err != nil {
		return nil, fmt.Errorf("agentauth admin: list agents: %w", err)
	}
	defer rows.Close()

	var agents []AgentAdminRow
	for rows.Next() {
		var a AgentAdminRow
		if err := rows.Scan(&a.ID, &a.DisplayName, &a.Enabled, &a.LastSeenAt, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("agentauth admin: scan agent: %w", err)
		}
		agents = append(agents, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("agentauth admin: iterate agents: %w", err)
	}

	// Load allowed roots for each agent.
	for i := range agents {
		roots, err := s.AllowedRoots(ctx, agents[i].ID)
		if err != nil {
			return nil, fmt.Errorf("agentauth admin: load roots for %q: %w", agents[i].ID, err)
		}
		if roots == nil {
			roots = []string{}
		}
		agents[i].AllowedRoots = roots
	}
	return agents, nil
}

// RegisterAgent creates a new agent with the given id, display name, and
// allowed roots. It returns the raw key (prefixed "ak_") which is shown exactly
// ONCE — the stored value is a bcrypt hash.
func (s *Service) RegisterAgent(ctx context.Context, id, displayName string, roots []string) (rawKey string, err error) {
	if id == "" {
		return "", platform.BadRequest("admin.agent_id_required", "agent id is required")
	}

	// Generate "ak_" + 32 random bytes as hex (64 hex chars = 256-bit key).
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("agentauth admin: generate key: %w", err)
	}
	rawKey = "ak_" + hex.EncodeToString(buf)

	keyHash, err := HashKey(rawKey)
	if err != nil {
		return "", fmt.Errorf("agentauth admin: hash key: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("agentauth admin: begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	_, err = tx.Exec(ctx,
		`INSERT INTO agents (id, display_name, key_hash, enabled) VALUES ($1, $2, $3, true)`,
		id, displayName, keyHash)
	if err != nil {
		if isDuplicateKey(err) {
			return "", platform.Conflict("admin.agent_id_taken", "an agent with this id already exists")
		}
		return "", fmt.Errorf("agentauth admin: insert agent: %w", err)
	}

	for _, root := range roots {
		_, err = tx.Exec(ctx,
			`INSERT INTO agent_allowed_roots (agent_id, root) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			id, root)
		if err != nil {
			return "", fmt.Errorf("agentauth admin: insert root %q: %w", root, err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("agentauth admin: commit: %w", err)
	}
	return rawKey, nil
}

// RotateKey generates a new raw key for the agent, re-hashes it, and persists
// the hash. Returns the new raw key which is shown exactly ONCE.
func (s *Service) RotateKey(ctx context.Context, id string) (rawKey string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("agentauth admin: generate key: %w", err)
	}
	rawKey = "ak_" + hex.EncodeToString(buf)

	keyHash, err := HashKey(rawKey)
	if err != nil {
		return "", fmt.Errorf("agentauth admin: hash key: %w", err)
	}

	ct, err := s.pool.Exec(ctx,
		`UPDATE agents SET key_hash = $1 WHERE id = $2`,
		keyHash, id)
	if err != nil {
		return "", fmt.Errorf("agentauth admin: rotate key: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return "", platform.NotFound("admin.agent_not_found", "agent not found")
	}
	return rawKey, nil
}

// SetEnabled enables or disables an agent.
func (s *Service) SetEnabled(ctx context.Context, id string, enabled bool) error {
	ct, err := s.pool.Exec(ctx,
		`UPDATE agents SET enabled = $1 WHERE id = $2`,
		enabled, id)
	if err != nil {
		return fmt.Errorf("agentauth admin: set enabled: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return platform.NotFound("admin.agent_not_found", "agent not found")
	}
	return nil
}

// SetAllowedRoots replaces the full set of allowed roots for an agent.
// It deletes all existing rows and inserts the new set inside a transaction.
func (s *Service) SetAllowedRoots(ctx context.Context, id string, roots []string) error {
	// Confirm agent exists.
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agents WHERE id = $1)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("agentauth admin: check agent: %w", err)
	}
	if !exists {
		return platform.NotFound("admin.agent_not_found", "agent not found")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("agentauth admin: begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	_, err = tx.Exec(ctx, `DELETE FROM agent_allowed_roots WHERE agent_id = $1`, id)
	if err != nil {
		return fmt.Errorf("agentauth admin: delete roots: %w", err)
	}

	for _, root := range roots {
		_, err = tx.Exec(ctx,
			`INSERT INTO agent_allowed_roots (agent_id, root) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			id, root)
		if err != nil {
			return fmt.Errorf("agentauth admin: insert root %q: %w", root, err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("agentauth admin: commit: %w", err)
	}
	return nil
}

// isDuplicateKey returns true when err is a Postgres unique-constraint violation
// (SQLSTATE 23505).
func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	type sqlStater interface{ SQLState() string }
	if e, ok := err.(sqlStater); ok {
		return e.SQLState() == "23505"
	}
	// Fallback: scan the error string for the SQLSTATE code.
	s := err.Error()
	for i := 0; i+4 < len(s); i++ {
		if s[i:i+5] == "23505" {
			return true
		}
	}
	return false
}
