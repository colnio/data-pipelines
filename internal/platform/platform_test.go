package platform_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/colnio/data-pipelines/internal/platform"
)

// ─── Principal.HasScope ───────────────────────────────────────────────────────

// TestHasScope_JWTSession verifies that a first-party browser session (both
// Via* fields nil) is unrestricted — HasScope always returns true.
func TestHasScope_JWTSession(t *testing.T) {
	p := &platform.Principal{UserID: uuid.New(), Email: "user@example.com"}
	// Via* fields are nil by default — first-party JWT session.
	scopes := []string{
		platform.ScopeReadRuns,
		platform.ScopeWriteRuns,
		platform.ScopeReadAudit,
		platform.ScopeWriteAdmin,
		"nonexistent:scope",
	}
	for _, s := range scopes {
		if !p.HasScope(s) {
			t.Errorf("JWT session: HasScope(%q) = false, want true", s)
		}
	}
}

// TestRequireScope_JWTSession verifies that RequireScope returns nil for a
// first-party browser session for any scope.
func TestRequireScope_JWTSession(t *testing.T) {
	p := &platform.Principal{UserID: uuid.New(), Email: "user@example.com"}
	if err := platform.RequireScope(p, platform.ScopeWriteReviews); err != nil {
		t.Errorf("JWT session: RequireScope returned non-nil: %v", err)
	}
	if err := platform.RequireScope(p, "anything"); err != nil {
		t.Errorf("JWT session: RequireScope('anything') = %v, want nil", err)
	}
}

// TestHasScope_EmptyTokenScopes verifies that token callers with nil/empty scopes
// are denied every capability.
func TestHasScope_EmptyTokenScopes(t *testing.T) {
	tokenID := uuid.New()
	p := &platform.Principal{
		UserID:     uuid.New(),
		Email:      "bot@example.com",
		ViaTokenID: &tokenID,
		Scopes:     nil,
	}
	for _, s := range []string{platform.ScopeReadRuns, platform.ScopeWriteRuns, platform.ScopeReadAudit} {
		if p.HasScope(s) {
			t.Errorf("empty-scoped PAT: HasScope(%q) = true, want false", s)
		}
	}

	p.Scopes = []string{}
	if p.HasScope(platform.ScopeWriteAdmin) {
		t.Error("empty-scoped PAT: HasScope should return false")
	}
}

// TestHasScope_ScopedToken verifies that a token caller with a non-empty Scopes
// list is enforced: only listed scopes are granted.
func TestHasScope_ScopedToken(t *testing.T) {
	tokenID := uuid.New()
	p := &platform.Principal{
		UserID:     uuid.New(),
		Email:      "bot@example.com",
		ViaTokenID: &tokenID,
		Scopes:     []string{platform.ScopeReadAudit},
	}

	if !p.HasScope(platform.ScopeReadAudit) {
		t.Error("HasScope('read:audit') = false, want true")
	}
	if p.HasScope(platform.ScopeReadRuns) {
		t.Error("HasScope('read:projects') = true, want false for scoped token")
	}
}

// TestRequireScope_ScopedToken_Forbidden verifies that RequireScope returns a
// Forbidden error when the scoped token lacks the required scope.
func TestRequireScope_ScopedToken_Forbidden(t *testing.T) {
	tokenID := uuid.New()
	p := &platform.Principal{
		UserID:     uuid.New(),
		Email:      "bot@example.com",
		ViaTokenID: &tokenID,
		Scopes:     []string{platform.ScopeReadAudit},
	}

	err := platform.RequireScope(p, platform.ScopeReadRuns)
	if err == nil {
		t.Fatal("RequireScope should return Forbidden for missing scope")
	}
	em, ok := err.(*platform.ErrorModel)
	if !ok {
		t.Fatalf("expected *platform.ErrorModel, got %T: %v", err, err)
	}
	if em.GetStatus() != http.StatusForbidden {
		t.Errorf("expected 403, got %d", em.GetStatus())
	}
	if em.Code != "forbidden" {
		t.Errorf("expected code 'forbidden', got %q", em.Code)
	}
}

// TestHasScope_AICallerEnforced verifies that an internal-LLM caller
// (ViaAIConversationID set) with a non-empty scope list is enforced.
func TestHasScope_AICallerEnforced(t *testing.T) {
	convID := uuid.New()
	p := &platform.Principal{
		UserID:              uuid.New(),
		ViaAIConversationID: &convID,
		Scopes:              []string{platform.ScopeReadSamples, platform.ScopeReadDevices},
	}

	if !p.HasScope(platform.ScopeReadSamples) {
		t.Error("LLM caller: HasScope('read:samples') should be true")
	}
	if p.HasScope(platform.ScopeWriteRuns) {
		t.Error("LLM caller: HasScope('write:projects') should be false — scope not in list")
	}
}

// TestHasScope_NilPrincipal verifies that a nil principal always returns false.
func TestHasScope_NilPrincipal(t *testing.T) {
	var p *platform.Principal
	if p.HasScope("any:scope") {
		t.Error("nil principal: HasScope should return false")
	}
}

// ─── RateLimiter.Middleware ───────────────────────────────────────────────────

// TestRateLimiter_JWTSessionLimited verifies that first-party JWT sessions are
// now rate-limited (Finding 14: they were previously excluded from limiting).
// We use a limit of 2 and drive 3 requests; the 3rd must return 429.
func TestRateLimiter_JWTSessionLimited(t *testing.T) {
	const limit = 2
	rl := platform.NewRateLimiter(limit)

	// Dummy handler that always 200s.
	dummy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.Middleware()(dummy)

	userID := uuid.New()
	p := &platform.Principal{
		UserID: userID,
		Email:  "jwt@example.com",
		// ViaTokenID and ViaAIConversationID are nil — first-party JWT.
	}

	makeReq := func() *http.Response {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req = req.WithContext(platform.WithPrincipal(req.Context(), p))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Result()
	}

	// First limit requests should succeed.
	for i := 0; i < limit; i++ {
		resp := makeReq()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i+1, resp.StatusCode)
		}
	}

	// The next request must be rate-limited.
	resp := makeReq()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected 429 after exceeding limit, got %d", resp.StatusCode)
	}
}

// TestRateLimiter_TokenLimited verifies that PAT callers are also limited.
func TestRateLimiter_TokenLimited(t *testing.T) {
	const limit = 1
	rl := platform.NewRateLimiter(limit)

	dummy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.Middleware()(dummy)

	tokenID := uuid.New()
	p := &platform.Principal{
		UserID:     uuid.New(),
		ViaTokenID: &tokenID,
	}

	makeReq := func() int {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req = req.WithContext(platform.WithPrincipal(req.Context(), p))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}

	if code := makeReq(); code != http.StatusOK {
		t.Errorf("first request: expected 200, got %d", code)
	}
	if code := makeReq(); code != http.StatusTooManyRequests {
		t.Errorf("second request: expected 429, got %d", code)
	}
}

// TestRateLimiter_Disabled verifies that a zero limit disables rate limiting.
func TestRateLimiter_Disabled(t *testing.T) {
	rl := platform.NewRateLimiter(0) // disabled

	dummy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.Middleware()(dummy)

	p := &platform.Principal{UserID: uuid.New(), Email: "user@example.com"}
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req = req.WithContext(platform.WithPrincipal(req.Context(), p))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("disabled limiter: request %d got %d, expected 200", i+1, w.Code)
		}
	}
}

// TestIPRateLimiter_LoginRegister limits POST login/register by client IP.
func TestIPRateLimiter_LoginRegister(t *testing.T) {
	rl := platform.NewIPRateLimiter(2)
	handler := rl.LoginRegisterMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	try := func(path string) int {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.RemoteAddr = "203.0.113.1:12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	if try("/v1/auth/login") != http.StatusOK {
		t.Fatal("first login should pass")
	}
	if try("/v1/auth/login") != http.StatusOK {
		t.Fatal("second login should pass")
	}
	if try("/v1/auth/login") != http.StatusTooManyRequests {
		t.Fatal("third login should be rate limited")
	}
	if try("/v1/auth/me") != http.StatusOK {
		t.Fatal("other paths should not be limited")
	}
}

// TestRateLimiter_UnauthenticatedPassthrough verifies that unauthenticated
// requests (no principal in context) are not limited.
func TestRateLimiter_UnauthenticatedPassthrough(t *testing.T) {
	rl := platform.NewRateLimiter(1)

	dummy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.Middleware()(dummy)

	// Send multiple requests without a principal.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("unauthenticated request %d: expected 200, got %d", i+1, w.Code)
		}
	}
}
