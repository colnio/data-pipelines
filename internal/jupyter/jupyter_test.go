package jupyter_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/colnio/data-pipelines/internal/config"
	"github.com/colnio/data-pipelines/internal/jupyter"
	"github.com/colnio/data-pipelines/internal/platform"
)

// ── normalizeUsername ─────────────────────────────────────────────────────────

func TestNormalizeUsername(t *testing.T) {
	t.Parallel()

	tests := []struct {
		email string
		want  string
	}{
		// Docs from the Python authenticator + derived cases.
		{"jane.doe@nus.edu.sg", "jane_doe"},   // '.' → '_'
		{"Jane.Doe@nus.edu.sg", "jane_doe"},   // uppercase lowered; '.' → '_'
		{"user+tag@example.com", "user_tag"},  // '+' → '_'
		{"alice@lab.local", "alice"},          // no replacement needed
		{"bob123@example.com", "bob123"},      // digits allowed
		{"a_b-c@x.com", "a_b-c"},             // underscore and hyphen preserved
		{"UPPER@x.com", "upper"},             // full uppercase
		{"user.name+filter@DOMAIN.COM", "user_name_filter"}, // mixed
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.email, func(t *testing.T) {
			t.Parallel()
			got := jupyter.NormalizeUsername(tc.email)
			assert.Equal(t, tc.want, got, "NormalizeUsername(%q)", tc.email)
		})
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// principalCtx stores a principal into a context so PrincipalFrom succeeds.
func principalCtx(p *platform.Principal) context.Context {
	return platform.WithPrincipal(context.Background(), p)
}

// scopedPrincipal returns a token-scoped principal (scope enforcement enabled).
func scopedPrincipal(email string, scopes ...string) *platform.Principal {
	tokenID := uuid.New()
	return &platform.Principal{
		UserID:     uuid.New(),
		Email:      email,
		GlobalRole: "pi",
		ViaTokenID: &tokenID,
		Scopes:     scopes,
	}
}

// minimalCfg returns a config with only the Jupyter fields set.
func minimalCfg(hubURL, adminToken string) *config.Config {
	return &config.Config{
		JupyterHubURL:        hubURL,
		JupyterHubAdminToken: adminToken,
	}
}

// ── GET /v1/jupyter/info ──────────────────────────────────────────────────────

func TestInfo_ReachableHub(t *testing.T) {
	// Stand-in Hub that returns 200 on GET /hub/api.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hub/api" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer stub.Close()

	cfg := minimalCfg(stub.URL, "admin-token")
	svc := jupyter.NewService(cfg, discardLogger())
	_, api := humatest.New(t)
	jupyter.Register(api, svc)

	p := scopedPrincipal("alice@nus.edu.sg", platform.ScopeReadCatalog)
	resp := api.GetCtx(principalCtx(p), "/v1/jupyter/info")
	require.Equal(t, http.StatusOK, resp.Code, "body: %s", resp.Body)

	var body struct {
		HubURL    string `json:"hub_url"`
		Reachable bool   `json:"reachable"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	assert.True(t, body.Reachable)
	assert.Equal(t, stub.URL, body.HubURL)
}

func TestInfo_UnreachableHub(t *testing.T) {
	// Point to a port that should immediately refuse.
	cfg := minimalCfg("http://127.0.0.1:19999", "some-token")
	svc := jupyter.NewService(cfg, discardLogger())
	_, api := humatest.New(t)
	jupyter.Register(api, svc)

	p := scopedPrincipal("alice@nus.edu.sg", platform.ScopeReadCatalog)
	resp := api.GetCtx(principalCtx(p), "/v1/jupyter/info")
	// Must still return 200 — reachable is false, not an error.
	require.Equal(t, http.StatusOK, resp.Code, "body: %s", resp.Body)

	var body struct {
		Reachable bool `json:"reachable"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	assert.False(t, body.Reachable)
}

func TestInfo_MissingScope(t *testing.T) {
	cfg := minimalCfg("http://localhost:8000", "tok")
	svc := jupyter.NewService(cfg, discardLogger())
	_, api := humatest.New(t)
	jupyter.Register(api, svc)

	// ScopeWriteAdmin does not include ScopeReadCatalog.
	p := scopedPrincipal("alice@nus.edu.sg", platform.ScopeWriteAdmin)
	resp := api.GetCtx(principalCtx(p), "/v1/jupyter/info")
	assert.Equal(t, http.StatusForbidden, resp.Code)
}

// ── POST /v1/jupyter/connection ───────────────────────────────────────────────

func TestConnection_HappyPath(t *testing.T) {
	const fakeToken = "hub-token-abc123"
	email := "Jane.Doe@nus.edu.sg"
	wantUsername := "jane_doe"

	var capturedPath string
	var capturedAuth string

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"` + fakeToken + `"}`))
	}))
	defer stub.Close()

	cfg := minimalCfg(stub.URL, "super-admin-token")
	svc := jupyter.NewService(cfg, discardLogger())
	_, api := humatest.New(t)
	jupyter.Register(api, svc)

	p := scopedPrincipal(email, platform.ScopeReadCatalog)
	resp := api.PostCtx(principalCtx(p), "/v1/jupyter/connection", map[string]any{})
	require.Equal(t, http.StatusOK, resp.Code, "body: %s", resp.Body)

	// Verify the Hub was called correctly.
	assert.Equal(t, "/hub/api/users/"+wantUsername+"/tokens", capturedPath,
		"Hub token endpoint should include normalized username")
	assert.Equal(t, "token super-admin-token", capturedAuth,
		"Admin auth header should be forwarded")

	// Verify the response shape.
	var body struct {
		ServerURL       string `json:"server_url"`
		Token           string `json:"token"`
		VSCodeServerURI string `json:"vscode_server_uri"`
		HubURL          string `json:"hub_url"`
		Username        string `json:"username"`
		ExpiresAt       string `json:"expires_at"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	assert.Equal(t, fakeToken, body.Token)
	assert.Equal(t, wantUsername, body.Username)
	assert.Equal(t, stub.URL, body.HubURL)
	assert.Equal(t, stub.URL+"/user/"+wantUsername+"/", body.ServerURL)
	assert.True(t, strings.Contains(body.VSCodeServerURI, "?token="+fakeToken),
		"vscode_server_uri must contain ?token=<token>, got: %s", body.VSCodeServerURI)
	assert.NotEmpty(t, body.ExpiresAt)
}

func TestConnection_NotConfigured_NoURL(t *testing.T) {
	cfg := minimalCfg("", "some-token")
	svc := jupyter.NewService(cfg, discardLogger())
	_, api := humatest.New(t)
	jupyter.Register(api, svc)

	p := scopedPrincipal("alice@nus.edu.sg", platform.ScopeReadCatalog)
	resp := api.PostCtx(principalCtx(p), "/v1/jupyter/connection", map[string]any{})
	assert.Equal(t, http.StatusServiceUnavailable, resp.Code, "body: %s", resp.Body)

	var errBody struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &errBody))
	assert.Equal(t, "jupyter.not_configured", errBody.Code)
}

func TestConnection_NotConfigured_NoToken(t *testing.T) {
	cfg := minimalCfg("http://localhost:8000", "")
	svc := jupyter.NewService(cfg, discardLogger())
	_, api := humatest.New(t)
	jupyter.Register(api, svc)

	p := scopedPrincipal("alice@nus.edu.sg", platform.ScopeReadCatalog)
	resp := api.PostCtx(principalCtx(p), "/v1/jupyter/connection", map[string]any{})
	assert.Equal(t, http.StatusServiceUnavailable, resp.Code, "body: %s", resp.Body)

	var errBody struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &errBody))
	assert.Equal(t, "jupyter.not_configured", errBody.Code)
}

func TestConnection_HubError(t *testing.T) {
	// Hub returns 500.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`internal error`))
	}))
	defer stub.Close()

	cfg := minimalCfg(stub.URL, "admin-token")
	svc := jupyter.NewService(cfg, discardLogger())
	_, api := humatest.New(t)
	jupyter.Register(api, svc)

	p := scopedPrincipal("alice@nus.edu.sg", platform.ScopeReadCatalog)
	resp := api.PostCtx(principalCtx(p), "/v1/jupyter/connection", map[string]any{})
	assert.Equal(t, http.StatusBadGateway, resp.Code, "body: %s", resp.Body)

	var errBody struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &errBody))
	assert.Equal(t, "jupyter.hub_error", errBody.Code)
}

func TestConnection_MissingScope(t *testing.T) {
	cfg := minimalCfg("http://localhost:8000", "token")
	svc := jupyter.NewService(cfg, discardLogger())
	_, api := humatest.New(t)
	jupyter.Register(api, svc)

	p := scopedPrincipal("alice@nus.edu.sg", platform.ScopeWriteAdmin)
	resp := api.PostCtx(principalCtx(p), "/v1/jupyter/connection", map[string]any{})
	assert.Equal(t, http.StatusForbidden, resp.Code)
}
