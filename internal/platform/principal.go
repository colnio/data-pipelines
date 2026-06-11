package platform

import (
	"context"

	"github.com/google/uuid"
)

// Principal is the authenticated caller resolved by the auth middleware and
// read by every handler. It is the single source of "who is acting" and how.
// The same struct represents a human (JWT), an external agent (PAT), and the
// in-app LLM (internal token) — only the Via* fields differ. This keeps one
// HTTP code path for all three, per spec §6.3 / §7.6.
//
// GlobalRole carries the user's platform-level role ("admin", "pi", or
// "member"). Use IsAdmin(), IsPI(), and IsPrivileged() rather than comparing
// the string directly — those methods are nil-safe.
type Principal struct {
	UserID     uuid.UUID
	Email      string
	GlobalRole string // "admin" | "pi" | "member" | "" (legacy/unknown)

	// Exactly one of the Via* fields is set for non-interactive callers; both
	// nil means a first-party browser session (JWT).
	ViaTokenID          *uuid.UUID // PAT id, when called with Authorization: Bearer pat_...
	ViaAIConversationID *uuid.UUID // LLM conversation/run id, when called with iai_... token

	// Scopes is the effective capability set already intersected with the
	// owner's resolved permissions (empty slice => full first-party session).
	Scopes []string
}

// IsAdmin reports whether the principal holds the "admin" global role.
func (p *Principal) IsAdmin() bool { return p != nil && p.GlobalRole == "admin" }

// IsPI reports whether the principal holds the "pi" global role.
func (p *Principal) IsPI() bool { return p != nil && p.GlobalRole == "pi" }

// IsPrivileged reports whether the principal is admin or PI — i.e. holds
// elevated platform-level access beyond a regular member.
func (p *Principal) IsPrivileged() bool { return p.IsAdmin() || p.IsPI() }

// HasScope reports whether the principal carries the given scope.
//
// Rules:
//   - A first-party browser session (both Via* fields nil) is unrestricted and
//     always returns true.
//   - Token callers (PAT or internal LLM) are enforced against their Scopes list;
//     an empty list grants nothing.
func (p *Principal) HasScope(scope string) bool {
	if p == nil {
		return false
	}
	if p.ViaTokenID == nil && p.ViaAIConversationID == nil {
		return true // interactive human session is not scope-limited
	}
	for _, s := range p.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// IsSessionOnly reports whether the principal is a first-party browser session
// (JWT), not a PAT or internal-LLM token.
func (p *Principal) IsSessionOnly() bool {
	return p != nil && p.ViaTokenID == nil && p.ViaAIConversationID == nil
}

type principalCtxKey struct{}

// WithPrincipal stores the resolved principal on the context.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

// PrincipalFrom returns the principal previously stored on the context.
func PrincipalFrom(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalCtxKey{}).(*Principal)
	return p, ok && p != nil
}
