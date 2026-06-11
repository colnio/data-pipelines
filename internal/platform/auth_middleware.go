package platform

import (
	"net/http"
	"strings"
)

// AuthResolver is a chi middleware that resolves the Authorization bearer token
// into a Principal and stores it on the request context. It NEVER rejects a
// missing token — public endpoints (login, healthz, openapi) must work
// unauthenticated, and protected handlers enforce auth by reading the
// principal. It DOES reject a token that is present but invalid with 401, so
// callers get a clear signal rather than silent anonymous access.
//
// Token kind is chosen by prefix: pat_ → PAT, iai_ → internal LLM token,
// otherwise a first-party access JWT.
func AuthResolver(v TokenVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if v == nil {
				next.ServeHTTP(w, r)
				return
			}
			raw := bearer(r)
			if raw == "" {
				next.ServeHTTP(w, r)
				return
			}
			ctx := r.Context()
			var (
				p   *Principal
				err error
			)
			switch {
			case strings.HasPrefix(raw, "pat_"):
				p, err = v.VerifyPAT(ctx, raw)
			case strings.HasPrefix(raw, "iai_"):
				p, err = v.VerifyInternalAIToken(ctx, raw)
			default:
				p, err = v.VerifyAccessToken(ctx, raw)
			}
			if err != nil || p == nil {
				writeError(w, Unauthorized("invalid or expired token"))
				return
			}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(ctx, p)))
		})
	}
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	const pfx = "Bearer "
	if len(h) > len(pfx) && strings.EqualFold(h[:len(pfx)], pfx) {
		return strings.TrimSpace(h[len(pfx):])
	}
	return ""
}
