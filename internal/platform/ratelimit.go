package platform

import (
	"net"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RateLimiter applies a per-token sliding-window limit. Only token-based
// callers (PAT / internal LLM) are limited; first-party browser sessions are
// not. When constructed with a Postgres pool, counters survive restarts and
// are shared across replicas.
type RateLimiter struct {
	perMinute int
	backend   rateLimitBackend
}

// NewRateLimiter builds an in-memory limiter allowing perMinute requests per
// key per rolling 60s window. perMinute <= 0 disables limiting.
func NewRateLimiter(perMinute int) *RateLimiter {
	return &RateLimiter{perMinute: perMinute, backend: newMemRateLimitBackend()}
}

// NewRateLimiterFromPool builds a Postgres-backed limiter.
func NewRateLimiterFromPool(pool *pgxpool.Pool, perMinute int) *RateLimiter {
	return &RateLimiter{perMinute: perMinute, backend: newPgRateLimitBackend(pool)}
}

// Middleware enforces the limit keyed by the principal's token id.
func (rl *RateLimiter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rl == nil || rl.perMinute <= 0 {
				next.ServeHTTP(w, r)
				return
			}
			p, ok := PrincipalFrom(r.Context())
			key := ""
			if ok {
				switch {
				case p.ViaTokenID != nil:
					key = "pat:" + p.ViaTokenID.String()
				case p.ViaAIConversationID != nil:
					key = "iai:" + p.ViaAIConversationID.String()
				default:
					key = "user:" + p.UserID.String()
				}
			}
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}
			allowed, err := rl.backend.allow(r.Context(), key, rl.perMinute)
			if err != nil {
				writeError(w, Errorf(http.StatusServiceUnavailable, "rate_limit_unavailable", "rate limiter unavailable"))
				return
			}
			if !allowed {
				writeError(w, Errorf(http.StatusTooManyRequests, "rate_limited", "rate limit exceeded"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// IPRateLimiter applies a per-client-IP sliding-window limit for unauthenticated
// auth endpoints (login/register).
type IPRateLimiter struct {
	perMinute int
	backend   rateLimitBackend
}

// NewIPRateLimiter builds an in-memory IP-keyed limiter.
func NewIPRateLimiter(perMinute int) *IPRateLimiter {
	return &IPRateLimiter{perMinute: perMinute, backend: newMemRateLimitBackend()}
}

// NewIPRateLimiterFromPool builds a Postgres-backed IP limiter.
func NewIPRateLimiterFromPool(pool *pgxpool.Pool, perMinute int) *IPRateLimiter {
	return &IPRateLimiter{perMinute: perMinute, backend: newPgRateLimitBackend(pool)}
}

// LoginRegisterMiddleware rate-limits POST /v1/auth/login and /v1/auth/register.
func (rl *IPRateLimiter) LoginRegisterMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rl == nil || rl.perMinute <= 0 {
				next.ServeHTTP(w, r)
				return
			}
			if r.Method != http.MethodPost {
				next.ServeHTTP(w, r)
				return
			}
			switch r.URL.Path {
			case "/v1/auth/login", "/v1/auth/register":
			default:
				next.ServeHTTP(w, r)
				return
			}
			ip := clientIP(r)
			if ip == "" {
				next.ServeHTTP(w, r)
				return
			}
			allowed, err := rl.backend.allow(r.Context(), "ip:"+ip, rl.perMinute)
			if err != nil {
				writeError(w, Errorf(http.StatusServiceUnavailable, "rate_limit_unavailable", "rate limiter unavailable"))
				return
			}
			if !allowed {
				writeError(w, Errorf(http.StatusTooManyRequests, "rate_limited", "rate limit exceeded"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP returns the best-effort client address for rate limiting.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			xff = strings.TrimSpace(xff[:i])
		} else {
			xff = strings.TrimSpace(xff)
		}
		if xff != "" {
			return xff
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
