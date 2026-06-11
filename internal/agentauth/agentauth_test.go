package agentauth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/colnio/data-pipelines/internal/agentauth"
	"github.com/colnio/data-pipelines/internal/testsupport"
)

// ---------------------------------------------------------------------------
// Pure path-validation tests (no DB required)
// ---------------------------------------------------------------------------

func TestResolveUnderRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		measPath    string
		roots       []string
		wantOK      bool
		wantMatched string
	}{
		{
			name:        "exact root match",
			measPath:    "/data/runs",
			roots:       []string{"/data/runs"},
			wantOK:      true,
			wantMatched: "/data/runs",
		},
		{
			name:        "subdir match",
			measPath:    "/data/runs/2024/exp01",
			roots:       []string{"/data/runs"},
			wantOK:      true,
			wantMatched: "/data/runs",
		},
		{
			name:     "prefix attack rejected — runs-evil vs runs",
			measPath: "/data/runs-evil",
			roots:    []string{"/data/runs"},
			wantOK:   false,
		},
		{
			name:     "traversal rejected",
			measPath: "/data/runs/../../etc",
			roots:    []string{"/data/runs", "/data", "/"},
			wantOK:   false,
		},
		{
			name:     "relative path rejected",
			measPath: "data/runs/exp01",
			roots:    []string{"/data/runs"},
			wantOK:   false,
		},
		{
			name:     "empty roots rejected",
			measPath: "/data/runs/exp01",
			roots:    []string{},
			wantOK:   false,
		},
		{
			name:        "multiple roots — first does not match second does",
			measPath:    "/lab/meas/run01",
			roots:       []string{"/data/runs", "/lab/meas"},
			wantOK:      true,
			wantMatched: "/lab/meas",
		},
		{
			name:     "path equal to root subdir but shorter than root — no match",
			measPath: "/data",
			roots:    []string{"/data/runs"},
			wantOK:   false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			matched, ok := agentauth.ResolveUnderRoot(tc.measPath, tc.roots)
			if ok != tc.wantOK {
				t.Errorf("ResolveUnderRoot(%q, %v) ok=%v, want %v", tc.measPath, tc.roots, ok, tc.wantOK)
			}
			if ok && matched != tc.wantMatched {
				t.Errorf("ResolveUnderRoot: matched=%q, want %q", matched, tc.wantMatched)
			}
		})
	}
}

func TestValidateMeasPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		measPath string
		roots    []string
		wantErr  bool
	}{
		{
			name:     "allowed subdir",
			measPath: "/data/runs/exp01",
			roots:    []string{"/data/runs"},
			wantErr:  false,
		},
		{
			name:     "exact root allowed",
			measPath: "/data/runs",
			roots:    []string{"/data/runs"},
			wantErr:  false,
		},
		{
			name:     "prefix attack returns ErrPathNotAllowed",
			measPath: "/data/runs-evil",
			roots:    []string{"/data/runs"},
			wantErr:  true,
		},
		{
			name:     "traversal returns ErrPathNotAllowed",
			measPath: "/data/runs/../../etc/passwd",
			roots:    []string{"/data/runs", "/", "/data"},
			wantErr:  true,
		},
		{
			name:     "relative path returns ErrPathNotAllowed",
			measPath: "relative/path",
			roots:    []string{"/data/runs"},
			wantErr:  true,
		},
		{
			name:     "empty roots returns ErrPathNotAllowed",
			measPath: "/data/runs/exp01",
			roots:    []string{},
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := agentauth.ValidateMeasPath(tc.measPath, tc.roots)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !errors.Is(err, agentauth.ErrPathNotAllowed) {
					t.Errorf("expected ErrPathNotAllowed, got %v", err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DB-backed integration tests
// ---------------------------------------------------------------------------

func TestAuthenticate(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()

	testsupport.Truncate(t, pool, "agent_allowed_roots", "agents")

	svc := agentauth.NewService(pool)

	const agentID = "test-agent-01"
	const rawKey = "super-secret-key-abc123"

	hash, err := agentauth.HashKey(rawKey)
	if err != nil {
		t.Fatalf("HashKey: %v", err)
	}

	_, err = pool.Exec(ctx,
		`INSERT INTO agents (id, display_name, key_hash, enabled) VALUES ($1, $2, $3, $4)`,
		agentID, "Test Agent", hash, true,
	)
	if err != nil {
		t.Fatalf("insert agent: %v", err)
	}

	t.Run("correct key succeeds", func(t *testing.T) {
		agent, err := svc.Authenticate(ctx, agentID, rawKey)
		if err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if agent.ID != agentID {
			t.Errorf("agent.ID=%q, want %q", agent.ID, agentID)
		}
		if agent.KeyHash != "" {
			t.Errorf("KeyHash must not be exposed after Authenticate, got %q", agent.KeyHash)
		}
	})

	t.Run("wrong key returns ErrBadKey", func(t *testing.T) {
		_, err := svc.Authenticate(ctx, agentID, "wrong-key")
		if !errors.Is(err, agentauth.ErrBadKey) {
			t.Errorf("expected ErrBadKey, got %v", err)
		}
	})

	t.Run("unknown agent returns ErrAgentUnknown", func(t *testing.T) {
		_, err := svc.Authenticate(ctx, "no-such-agent", rawKey)
		if !errors.Is(err, agentauth.ErrAgentUnknown) {
			t.Errorf("expected ErrAgentUnknown, got %v", err)
		}
	})

	t.Run("disabled agent returns ErrAgentDisabled", func(t *testing.T) {
		const disabledID = "disabled-agent-01"
		disabledHash, _ := agentauth.HashKey("some-key")
		_, err := pool.Exec(ctx,
			`INSERT INTO agents (id, display_name, key_hash, enabled) VALUES ($1, $2, $3, $4)`,
			disabledID, "Disabled Agent", disabledHash, false,
		)
		if err != nil {
			t.Fatalf("insert disabled agent: %v", err)
		}

		_, err = svc.Authenticate(ctx, disabledID, "some-key")
		if !errors.Is(err, agentauth.ErrAgentDisabled) {
			t.Errorf("expected ErrAgentDisabled, got %v", err)
		}
	})
}

func TestValidateMeasPathIntegration(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()

	testsupport.Truncate(t, pool, "agent_allowed_roots", "agents")

	svc := agentauth.NewService(pool)

	const agentID = "path-test-agent-01"

	hash, err := agentauth.HashKey("key-for-path-tests")
	if err != nil {
		t.Fatalf("HashKey: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO agents (id, display_name, key_hash, enabled) VALUES ($1, $2, $3, $4)`,
		agentID, "Path Test Agent", hash, true,
	)
	if err != nil {
		t.Fatalf("insert agent: %v", err)
	}

	// Insert some allowed roots for the agent.
	for _, root := range []string{"/data/runs", "/lab/meas"} {
		_, err = pool.Exec(ctx,
			`INSERT INTO agent_allowed_roots (agent_id, root) VALUES ($1, $2)`,
			agentID, root,
		)
		if err != nil {
			t.Fatalf("insert root %q: %v", root, err)
		}
	}

	t.Run("allowed path succeeds", func(t *testing.T) {
		if err := svc.ValidateMeasPath(ctx, agentID, "/data/runs/exp01"); err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})

	t.Run("second root also allowed", func(t *testing.T) {
		if err := svc.ValidateMeasPath(ctx, agentID, "/lab/meas/run42"); err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})

	t.Run("disallowed path returns ErrPathNotAllowed", func(t *testing.T) {
		err := svc.ValidateMeasPath(ctx, agentID, "/other/path")
		if !errors.Is(err, agentauth.ErrPathNotAllowed) {
			t.Errorf("expected ErrPathNotAllowed, got %v", err)
		}
	})

	t.Run("prefix attack disallowed", func(t *testing.T) {
		err := svc.ValidateMeasPath(ctx, agentID, "/data/runs-evil")
		if !errors.Is(err, agentauth.ErrPathNotAllowed) {
			t.Errorf("expected ErrPathNotAllowed, got %v", err)
		}
	})

	t.Run("agent with no roots configured fails closed", func(t *testing.T) {
		const noRootsAgentID = "no-roots-agent-01"
		noRootsHash, _ := agentauth.HashKey("no-roots-key")
		_, err := pool.Exec(ctx,
			`INSERT INTO agents (id, display_name, key_hash, enabled) VALUES ($1, $2, $3, $4)`,
			noRootsAgentID, "No Roots Agent", noRootsHash, true,
		)
		if err != nil {
			t.Fatalf("insert no-roots agent: %v", err)
		}

		err = svc.ValidateMeasPath(ctx, noRootsAgentID, "/data/runs/exp01")
		if !errors.Is(err, agentauth.ErrPathNotAllowed) {
			t.Errorf("expected ErrPathNotAllowed for agent with no roots, got %v", err)
		}
	})
}
