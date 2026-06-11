package agentauth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/colnio/data-pipelines/internal/domain"
)

// Sentinel errors returned by Authenticate. The HTTP layer maps all of them
// to 401 without leaking which specific condition fired; detailed reasons are
// logged server-side only.
var (
	ErrAgentUnknown  = errors.New("agent unknown")
	ErrAgentDisabled = errors.New("agent disabled")
	ErrBadKey        = errors.New("bad key")
)

// dummyHash is a pre-computed bcrypt hash used as a timing-equaliser when an
// agent ID is not found, to avoid user-enumeration via response latency.
// "dummy-key-never-matches" will never match anything presented by a real agent.
var dummyHash []byte

func init() {
	h, err := bcrypt.GenerateFromPassword([]byte("dummy-key-never-matches"), bcrypt.DefaultCost)
	if err != nil {
		panic("agentauth: failed to generate dummy bcrypt hash: " + err.Error())
	}
	dummyHash = h
}

// HashKey produces a bcrypt hash of rawKey at the default cost. Use this when
// registering a new agent (writing to agents.key_hash) and in tests.
func HashKey(rawKey string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(rawKey), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("bcrypt: %w", err)
	}
	return string(h), nil
}

// Service authenticates agents and validates their measurement paths against
// the allowed roots stored in the database.
type Service struct {
	pool *pgxpool.Pool
}

// NewService returns a new Service backed by the supplied connection pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// Authenticate loads the agent identified by agentID, verifies providedKey
// against the stored bcrypt hash, and returns the agent on success.
//
// Error mapping (all returned as-is; the HTTP layer converts them all to 401):
//   - ErrAgentUnknown  – no row with that id
//   - ErrAgentDisabled – row found but enabled=false
//   - ErrBadKey        – hash comparison failed
//
// Timing oracle mitigation: when the agent is unknown a bcrypt comparison is
// still performed against a dummy hash before ErrAgentUnknown is returned, so
// the latency of a miss equals the latency of a wrong-password response.
//
// On success a best-effort UPDATE sets agents.last_seen_at=now(). Its error
// is intentionally ignored so a transient write failure does not block the
// authentication path.
func (s *Service) Authenticate(ctx context.Context, agentID, providedKey string) (*domain.Agent, error) {
	const query = `
		SELECT id, display_name, key_hash, enabled, last_seen_at, created_at
		FROM agents
		WHERE id = $1`

	var agent domain.Agent
	err := s.pool.QueryRow(ctx, query, agentID).Scan(
		&agent.ID,
		&agent.DisplayName,
		&agent.KeyHash,
		&agent.Enabled,
		&agent.LastSeenAt,
		&agent.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		// Timing equaliser: always do a bcrypt compare before returning.
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(providedKey))
		return nil, ErrAgentUnknown
	}
	if err != nil {
		return nil, fmt.Errorf("load agent: %w", err)
	}

	if !agent.Enabled {
		// Still compare to equalise timing — a disabled agent should not
		// reveal itself faster than an unknown one.
		_ = bcrypt.CompareHashAndPassword([]byte(agent.KeyHash), []byte(providedKey))
		return nil, ErrAgentDisabled
	}

	if err := bcrypt.CompareHashAndPassword([]byte(agent.KeyHash), []byte(providedKey)); err != nil {
		return nil, ErrBadKey
	}

	// Best-effort last_seen_at update; failure is non-fatal.
	_, _ = s.pool.Exec(ctx,
		`UPDATE agents SET last_seen_at = now() WHERE id = $1`, agentID)

	// Clear key_hash before returning — callers must not see or forward it.
	agent.KeyHash = ""
	return &agent, nil
}

// AllowedRoots returns the filesystem roots the agent with agentID is
// permitted to expose, in insertion order. Returns a nil slice (not an error)
// when the agent has no rows in agent_allowed_roots.
func (s *Service) AllowedRoots(ctx context.Context, agentID string) ([]string, error) {
	const query = `
		SELECT root
		FROM agent_allowed_roots
		WHERE agent_id = $1
		ORDER BY id`

	rows, err := s.pool.Query(ctx, query, agentID)
	if err != nil {
		return nil, fmt.Errorf("query allowed roots: %w", err)
	}
	defer rows.Close()

	var roots []string
	for rows.Next() {
		var root string
		if err := rows.Scan(&root); err != nil {
			return nil, fmt.Errorf("scan allowed root: %w", err)
		}
		roots = append(roots, root)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate allowed roots: %w", err)
	}
	return roots, nil
}

// ValidateMeasPath loads the agent's allowed roots from the database and
// delegates to the pure ValidateMeasPath function. If the agent has no roots
// configured the call fails closed (ErrPathNotAllowed).
func (s *Service) ValidateMeasPath(ctx context.Context, agentID, measPath string) error {
	roots, err := s.AllowedRoots(ctx, agentID)
	if err != nil {
		return fmt.Errorf("load roots for agent %q: %w", agentID, err)
	}
	return ValidateMeasPath(measPath, roots)
}
