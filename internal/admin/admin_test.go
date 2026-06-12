package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/colnio/data-pipelines/internal/admin"
	"github.com/colnio/data-pipelines/internal/agentauth"
	"github.com/colnio/data-pipelines/internal/auth"
	"github.com/colnio/data-pipelines/internal/platform"
	"github.com/colnio/data-pipelines/internal/testsupport"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func setup(t *testing.T) (*pgxpool.Pool, humatest.TestAPI) {
	t.Helper()
	pool := testsupport.NewPool(t)
	truncate(t, pool)

	authSvc, err := auth.NewService(pool, auth.Config{
		JWTSigningKey: "test-key",
		IsProduction:  false,
	}, nil)
	require.NoError(t, err)

	agentSvc := agentauth.NewService(pool)
	svc := admin.NewService(pool, authSvc, agentSvc)

	_, api := humatest.New(t)
	admin.Register(api, svc)

	return pool, api
}

func truncate(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	testsupport.Truncate(t, pool,
		"review_decisions",
		"review_artifacts",
		"run_state_transitions",
		"run_files",
		"manifests",
		"jobs",
		"runs",
		"agent_allowed_roots",
		"agents",
		"users",
	)
}

func adminPrincipalCtx() context.Context {
	return platform.WithPrincipal(context.Background(), &platform.Principal{
		UserID:     uuid.New(),
		Email:      "admin@lab.example",
		GlobalRole: "admin",
	})
}

func memberPrincipalCtx() context.Context {
	return platform.WithPrincipal(context.Background(), &platform.Principal{
		UserID:     uuid.New(),
		Email:      "member@lab.example",
		GlobalRole: "member",
	})
}

func piPrincipalCtx() context.Context {
	return platform.WithPrincipal(context.Background(), &platform.Principal{
		UserID:     uuid.New(),
		Email:      "pi@lab.example",
		GlobalRole: "pi",
	})
}

// ── User tests ─────────────────────────────────────────────────────────────────

func TestListUsers_HappyPath(t *testing.T) {
	pool, api := setup(t)
	ctx := context.Background()

	authSvc, err := auth.NewService(pool, auth.Config{IsProduction: false}, nil)
	require.NoError(t, err)

	uid, err := authSvc.Register(ctx, auth.RegisterParams{
		Email:       "alice@lab.example",
		Password:    "password123",
		DisplayName: "Alice",
	})
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, uid)

	resp := api.GetCtx(adminPrincipalCtx(), "/v1/admin/users")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	var out struct {
		Users []struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"users"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	require.Len(t, out.Users, 1)
	assert.Equal(t, "alice@lab.example", out.Users[0].Email)
}

func TestListUsers_FilterByStatus(t *testing.T) {
	pool, api := setup(t)
	ctx := context.Background()

	authSvc, err := auth.NewService(pool, auth.Config{IsProduction: false}, nil)
	require.NoError(t, err)
	_, err = authSvc.Register(ctx, auth.RegisterParams{Email: "bob@lab.example", Password: "password123", DisplayName: "Bob"})
	require.NoError(t, err)

	// Should appear in active filter.
	resp := api.GetCtx(adminPrincipalCtx(), "/v1/admin/users?status=active")
	require.Equal(t, http.StatusOK, resp.Code)
	var out struct {
		Users []struct{ Email string `json:"email"` } `json:"users"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	assert.Len(t, out.Users, 1)

	// Should NOT appear in pending filter.
	resp2 := api.GetCtx(adminPrincipalCtx(), "/v1/admin/users?status=pending")
	require.Equal(t, http.StatusOK, resp2.Code)
	var out2 struct {
		Users []struct{ Email string `json:"email"` } `json:"users"`
	}
	require.NoError(t, json.Unmarshal(resp2.Body.Bytes(), &out2))
	assert.Len(t, out2.Users, 0)
}

func TestPatchUser_ChangeRole(t *testing.T) {
	pool, api := setup(t)
	ctx := context.Background()

	authSvc, err := auth.NewService(pool, auth.Config{IsProduction: false}, nil)
	require.NoError(t, err)
	uid, err := authSvc.Register(ctx, auth.RegisterParams{Email: "carol@lab.example", Password: "password123", DisplayName: "Carol"})
	require.NoError(t, err)

	resp := api.PatchCtx(adminPrincipalCtx(), "/v1/admin/users/"+uid.String(), map[string]any{
		"global_role": "pi",
	})
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	var role string
	require.NoError(t, pool.QueryRow(ctx, `SELECT global_role FROM users WHERE id = $1`, uid).Scan(&role))
	assert.Equal(t, "pi", role)
}

func TestPatchUser_ChangeStatus(t *testing.T) {
	pool, api := setup(t)
	ctx := context.Background()

	authSvc, err := auth.NewService(pool, auth.Config{IsProduction: false}, nil)
	require.NoError(t, err)
	uid, err := authSvc.Register(ctx, auth.RegisterParams{Email: "dave@lab.example", Password: "password123", DisplayName: "Dave"})
	require.NoError(t, err)

	resp := api.PatchCtx(adminPrincipalCtx(), "/v1/admin/users/"+uid.String(), map[string]any{
		"status": "disabled",
	})
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	var status string
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM users WHERE id = $1`, uid).Scan(&status))
	assert.Equal(t, "disabled", status)
}

// ── Role gating tests ──────────────────────────────────────────────────────────

func TestListUsers_MemberForbidden(t *testing.T) {
	_, api := setup(t)

	resp := api.GetCtx(memberPrincipalCtx(), "/v1/admin/users")
	assert.Equal(t, http.StatusForbidden, resp.Code,
		"member should be forbidden from listing users")
}

func TestListUsers_PIAllowed(t *testing.T) {
	_, api := setup(t)

	resp := api.GetCtx(piPrincipalCtx(), "/v1/admin/users")
	assert.Equal(t, http.StatusOK, resp.Code,
		"pi should be allowed to list users")
}

func TestPatchUser_MemberForbidden(t *testing.T) {
	_, api := setup(t)

	resp := api.PatchCtx(memberPrincipalCtx(), "/v1/admin/users/"+uuid.New().String(), map[string]any{
		"status": "active",
	})
	assert.Equal(t, http.StatusForbidden, resp.Code,
		"member should be forbidden from patching users")
}

func TestPatchUser_PIForbidden(t *testing.T) {
	_, api := setup(t)

	// PI can read but NOT mutate — mutations require admin.
	resp := api.PatchCtx(piPrincipalCtx(), "/v1/admin/users/"+uuid.New().String(), map[string]any{
		"status": "active",
	})
	assert.Equal(t, http.StatusForbidden, resp.Code,
		"pi should be forbidden from patching users (admin-only mutation)")
}

// ── Agent tests ────────────────────────────────────────────────────────────────

func TestRegisterAgent_HappyPath(t *testing.T) {
	pool, api := setup(t)
	ctx := context.Background()

	resp := api.PostCtx(adminPrincipalCtx(), "/v1/admin/agents", map[string]any{
		"id":            "measpc-test-01",
		"display_name":  "Test PC 01",
		"allowed_roots": []string{"/data/lab", "/data/scratch"},
	})
	require.Equal(t, http.StatusCreated, resp.Code, resp.Body.String())

	var out struct {
		ID     string `json:"id"`
		RawKey string `json:"raw_key"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	assert.Equal(t, "measpc-test-01", out.ID)
	assert.True(t, strings.HasPrefix(out.RawKey, "ak_"),
		"raw_key must start with 'ak_', got: %s", out.RawKey)
	assert.Len(t, out.RawKey, 3+64, // "ak_" + 64 hex chars
		"raw_key must be ak_ + 64 hex chars (32 random bytes), got length %d", len(out.RawKey))

	// Agent appears in list.
	listResp := api.GetCtx(adminPrincipalCtx(), "/v1/admin/agents")
	require.Equal(t, http.StatusOK, listResp.Code)
	var listOut struct {
		Agents []struct {
			ID           string   `json:"id"`
			AllowedRoots []string `json:"allowed_roots"`
		} `json:"agents"`
	}
	require.NoError(t, json.Unmarshal(listResp.Body.Bytes(), &listOut))
	require.Len(t, listOut.Agents, 1)
	assert.Equal(t, "measpc-test-01", listOut.Agents[0].ID)
	assert.ElementsMatch(t, []string{"/data/lab", "/data/scratch"}, listOut.Agents[0].AllowedRoots)

	// Key can actually authenticate the agent.
	agentSvc := agentauth.NewService(pool)
	agent, err := agentSvc.Authenticate(ctx, "measpc-test-01", out.RawKey)
	require.NoError(t, err)
	assert.Equal(t, "measpc-test-01", agent.ID)
}

func TestRotateKey_HappyPath(t *testing.T) {
	pool, api := setup(t)
	ctx := context.Background()

	// Register agent first.
	regResp := api.PostCtx(adminPrincipalCtx(), "/v1/admin/agents", map[string]any{
		"id":            "measpc-rotate-01",
		"display_name":  "Rotate Test",
		"allowed_roots": []string{},
	})
	require.Equal(t, http.StatusCreated, regResp.Code, regResp.Body.String())
	var regOut struct{ RawKey string `json:"raw_key"` }
	require.NoError(t, json.Unmarshal(regResp.Body.Bytes(), &regOut))
	oldKey := regOut.RawKey

	// Rotate key.
	rotResp := api.PostCtx(adminPrincipalCtx(), "/v1/admin/agents/measpc-rotate-01/rotate-key", map[string]any{})
	require.Equal(t, http.StatusOK, rotResp.Code, rotResp.Body.String())
	var rotOut struct{ RawKey string `json:"raw_key"` }
	require.NoError(t, json.Unmarshal(rotResp.Body.Bytes(), &rotOut))
	newKey := rotOut.RawKey

	assert.NotEqual(t, oldKey, newKey, "rotated key must differ from original")
	assert.True(t, strings.HasPrefix(newKey, "ak_"), "new key must start with 'ak_'")

	// Old key no longer works.
	agentSvc := agentauth.NewService(pool)
	_, err := agentSvc.Authenticate(ctx, "measpc-rotate-01", oldKey)
	assert.ErrorIs(t, err, agentauth.ErrBadKey, "old key must be rejected after rotation")

	// New key works.
	agent, err := agentSvc.Authenticate(ctx, "measpc-rotate-01", newKey)
	require.NoError(t, err)
	assert.Equal(t, "measpc-rotate-01", agent.ID)
}

func TestPatchAgent_SetEnabled(t *testing.T) {
	pool, api := setup(t)
	ctx := context.Background()

	regResp := api.PostCtx(adminPrincipalCtx(), "/v1/admin/agents", map[string]any{
		"id":            "measpc-disable-01",
		"display_name":  "Disable Test",
		"allowed_roots": []string{},
	})
	require.Equal(t, http.StatusCreated, regResp.Code)

	patchResp := api.PatchCtx(adminPrincipalCtx(), "/v1/admin/agents/measpc-disable-01", map[string]any{
		"enabled": false,
	})
	require.Equal(t, http.StatusOK, patchResp.Code, patchResp.Body.String())

	var enabled bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT enabled FROM agents WHERE id='measpc-disable-01'`).Scan(&enabled))
	assert.False(t, enabled)
}

func TestRegisterAgent_MemberForbidden(t *testing.T) {
	_, api := setup(t)

	resp := api.PostCtx(memberPrincipalCtx(), "/v1/admin/agents", map[string]any{
		"id":            "should-fail",
		"display_name":  "Should Fail",
		"allowed_roots": []string{},
	})
	assert.Equal(t, http.StatusForbidden, resp.Code)
}

func TestListAgents_MemberForbidden(t *testing.T) {
	_, api := setup(t)

	resp := api.GetCtx(memberPrincipalCtx(), "/v1/admin/agents")
	assert.Equal(t, http.StatusForbidden, resp.Code)
}

// ── Unauthenticated tests ──────────────────────────────────────────────────────

func TestAdminHandlers_Unauthenticated(t *testing.T) {
	_, api := setup(t)
	emptyCtx := context.Background()

	cases := []struct {
		method string
		path   string
		body   any
	}{
		{"GET", "/v1/admin/users", nil},
		{"PATCH", "/v1/admin/users/" + uuid.New().String(), map[string]any{"status": "active"}},
		{"GET", "/v1/admin/agents", nil},
		{"POST", "/v1/admin/agents", map[string]any{"id": "x", "display_name": "x", "allowed_roots": []string{}}},
		{"POST", "/v1/admin/agents/x/rotate-key", map[string]any{}},
		{"PATCH", "/v1/admin/agents/x", map[string]any{"enabled": true}},
		{"GET", "/v1/admin/jobs", nil},
		{"GET", "/v1/admin/audit", nil},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			var code int
			switch tc.method {
			case "GET":
				code = api.GetCtx(emptyCtx, tc.path).Code
			case "POST":
				code = api.PostCtx(emptyCtx, tc.path, tc.body).Code
			case "PATCH":
				code = api.PatchCtx(emptyCtx, tc.path, tc.body).Code
			}
			assert.Equal(t, http.StatusUnauthorized, code,
				"expected 401 for unauthenticated %s %s", tc.method, tc.path)
		})
	}
}
