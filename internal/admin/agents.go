package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/colnio/data-pipelines/internal/platform"
)

// ── GET /v1/admin/agents ─────────────────────────────────────────────────────

type adminListAgentsInput struct{}

type adminAgentRow struct {
	ID           string     `json:"id"`
	DisplayName  string     `json:"display_name"`
	Enabled      bool       `json:"enabled"`
	AllowedRoots []string   `json:"allowed_roots"`
	LastSeenAt   *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

type adminListAgentsOutput struct {
	Body struct {
		Agents []adminAgentRow `json:"agents"`
	}
}

func (s *Service) handleListAgents(ctx context.Context, _ *adminListAgentsInput) (*adminListAgentsOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequirePrivileged(p); err != nil {
		return nil, err
	}

	rows, err := s.agents.ListAgents(ctx)
	if err != nil {
		return nil, err
	}

	out := &adminListAgentsOutput{}
	out.Body.Agents = make([]adminAgentRow, 0, len(rows))
	for _, r := range rows {
		roots := r.AllowedRoots
		if roots == nil {
			roots = []string{}
		}
		out.Body.Agents = append(out.Body.Agents, adminAgentRow{
			ID:           r.ID,
			DisplayName:  r.DisplayName,
			Enabled:      r.Enabled,
			AllowedRoots: roots,
			LastSeenAt:   r.LastSeenAt,
			CreatedAt:    r.CreatedAt,
		})
	}
	return out, nil
}

// ── POST /v1/admin/agents ────────────────────────────────────────────────────

type adminRegisterAgentInput struct {
	Body struct {
		ID           string   `json:"id" minLength:"1" doc:"Human-meaningful agent identifier"`
		DisplayName  string   `json:"display_name" doc:"Human-readable display name"`
		AllowedRoots []string `json:"allowed_roots" doc:"Filesystem roots the agent may expose"`
	}
}

type adminRegisterAgentOutput struct {
	Body struct {
		ID      string `json:"id"`
		RawKey  string `json:"raw_key" doc:"The raw agent key — shown ONCE; store it securely"`
	}
}

func (s *Service) handleRegisterAgent(ctx context.Context, in *adminRegisterAgentInput) (*adminRegisterAgentOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequireAdmin(p); err != nil {
		return nil, err
	}

	roots := in.Body.AllowedRoots
	if roots == nil {
		roots = []string{}
	}

	rawKey, err := s.agents.RegisterAgent(ctx, in.Body.ID, in.Body.DisplayName, roots)
	if err != nil {
		return nil, err
	}

	out := &adminRegisterAgentOutput{}
	out.Body.ID = in.Body.ID
	out.Body.RawKey = rawKey
	return out, nil
}

// ── POST /v1/admin/agents/{id}/rotate-key ────────────────────────────────────

type adminRotateKeyInput struct {
	ID string `path:"id"`
}

type adminRotateKeyOutput struct {
	Body struct {
		ID     string `json:"id"`
		RawKey string `json:"raw_key" doc:"The new raw agent key — shown ONCE; store it securely"`
	}
}

func (s *Service) handleRotateKey(ctx context.Context, in *adminRotateKeyInput) (*adminRotateKeyOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequireAdmin(p); err != nil {
		return nil, err
	}

	rawKey, err := s.agents.RotateKey(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	out := &adminRotateKeyOutput{}
	out.Body.ID = in.ID
	out.Body.RawKey = rawKey
	return out, nil
}

// ── PATCH /v1/admin/agents/{id} ──────────────────────────────────────────────

type adminPatchAgentInput struct {
	ID   string `path:"id"`
	Body struct {
		Enabled      *bool    `json:"enabled,omitempty" doc:"Enable or disable the agent"`
		AllowedRoots []string `json:"allowed_roots,omitempty" doc:"Replace the full set of allowed roots"`
	}
}

type adminPatchAgentOutput struct {
	Body struct {
		ID      string   `json:"id"`
		Updated []string `json:"updated"`
	}
}

func (s *Service) handlePatchAgent(ctx context.Context, in *adminPatchAgentInput) (*adminPatchAgentOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequireAdmin(p); err != nil {
		return nil, err
	}

	if in.Body.Enabled == nil && in.Body.AllowedRoots == nil {
		return nil, platform.BadRequest("admin.no_fields", "provide at least one of enabled or allowed_roots")
	}

	var updated []string

	if in.Body.Enabled != nil {
		if err := s.agents.SetEnabled(ctx, in.ID, *in.Body.Enabled); err != nil {
			return nil, err
		}
		updated = append(updated, "enabled")
	}
	if in.Body.AllowedRoots != nil {
		if err := s.agents.SetAllowedRoots(ctx, in.ID, in.Body.AllowedRoots); err != nil {
			return nil, err
		}
		updated = append(updated, "allowed_roots")
	}

	out := &adminPatchAgentOutput{}
	out.Body.ID = in.ID
	out.Body.Updated = updated
	return out, nil
}

// registerAgentHandlers wires the agent admin endpoints onto api.
func registerAgentHandlers(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-agents",
		Method:      http.MethodGet,
		Path:        "/v1/admin/agents",
		Summary:     "List agents",
		Description: "Returns all agents with their allowed roots. Requires admin or pi role.",
		Tags:        []string{"admin"},
	}, svc.handleListAgents)

	huma.Register(api, huma.Operation{
		OperationID:   "admin-register-agent",
		Method:        http.MethodPost,
		Path:          "/v1/admin/agents",
		Summary:       "Register a new agent",
		Description:   "Creates a new agent and returns its raw key (shown ONCE). Requires admin role.",
		Tags:          []string{"admin"},
		DefaultStatus: http.StatusCreated,
	}, svc.handleRegisterAgent)

	huma.Register(api, huma.Operation{
		OperationID: "admin-rotate-agent-key",
		Method:      http.MethodPost,
		Path:        "/v1/admin/agents/{id}/rotate-key",
		Summary:     "Rotate an agent key",
		Description: "Generates and stores a new key for the agent; returns the raw key once. Requires admin role.",
		Tags:        []string{"admin"},
	}, svc.handleRotateKey)

	huma.Register(api, huma.Operation{
		OperationID: "admin-patch-agent",
		Method:      http.MethodPatch,
		Path:        "/v1/admin/agents/{id}",
		Summary:     "Update agent enabled flag or allowed roots",
		Description: "Enables/disables an agent and/or replaces its allowed roots. Requires admin role.",
		Tags:        []string{"admin"},
	}, svc.handlePatchAgent)
}
