package platform

import (
	"context"
	"log/slog"
)

// TokenVerifier resolves a bearer credential into a Principal. The auth module's
// *Service satisfies this; platform depends on the interface (not auth) to avoid
// an import cycle. Each method gets the raw credential string and returns the
// resolved principal or an error.
type TokenVerifier interface {
	VerifyAccessToken(ctx context.Context, raw string) (*Principal, error)
	VerifyPAT(ctx context.Context, raw string) (*Principal, error)
	VerifyInternalAIToken(ctx context.Context, raw string) (*Principal, error)
}

// IdempotencyStore persists write responses keyed by (principal, Idempotency-Key)
// so a retried request replays the original response instead of re-executing.
type IdempotencyStore interface {
	Lookup(ctx context.Context, principalKey, key string) (status int, body []byte, found bool, err error)
	Save(ctx context.Context, principalKey, key string, status int, body []byte) error
}

// ServerDeps are passed to New so the full middleware chain is installed before
// huma mounts routes. All fields except Logger may be nil (degraded mode).
type ServerDeps struct {
	Logger      *slog.Logger
	WebOrigin   string
	Verifier    TokenVerifier
	Idempotency IdempotencyStore

	// Production disables public /docs and /openapi (interactive spec).
	Production bool

	// RateLimiter and AuthIPRateLimiter are optional; nil uses in-memory backends.
	RateLimiter     *RateLimiter
	AuthIPRateLimiter *IPRateLimiter

	// PerMinute is the default per-token rate limit when RateLimiter is nil.
	// Zero disables limiting.
	PerMinute int

	// AuthIPPerMinute limits POST /v1/auth/login and /v1/auth/register per client IP
	// when AuthIPRateLimiter is nil. Zero disables limiting.
	AuthIPPerMinute int
}
