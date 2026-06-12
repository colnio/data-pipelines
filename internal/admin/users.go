package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/colnio/data-pipelines/internal/platform"
)

// ── GET /v1/admin/users ───────────────────────────────────────────────────────

type adminListUsersInput struct {
	Status string `query:"status" doc:"Filter by status: pending, active, or disabled"`
	Role   string `query:"role" doc:"Filter by global_role: admin, pi, or member"`
	Limit  int    `query:"limit" doc:"Max results (1–500, default 50)"`
}

type adminUserRow struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	GlobalRole  string    `json:"global_role"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type adminListUsersOutput struct {
	Body struct {
		Users []adminUserRow `json:"users"`
	}
}

func (s *Service) handleListUsers(ctx context.Context, in *adminListUsersInput) (*adminListUsersOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequirePrivileged(p); err != nil {
		return nil, err
	}

	rows, err := s.auth.ListUsers(ctx, in.Status, in.Role, in.Limit)
	if err != nil {
		return nil, err
	}

	out := &adminListUsersOutput{}
	out.Body.Users = make([]adminUserRow, 0, len(rows))
	for _, r := range rows {
		out.Body.Users = append(out.Body.Users, adminUserRow{
			ID:          r.ID,
			Email:       r.Email,
			DisplayName: r.DisplayName,
			GlobalRole:  r.GlobalRole,
			Status:      r.Status,
			CreatedAt:   r.CreatedAt,
		})
	}
	return out, nil
}

// ── PATCH /v1/admin/users/{id} ────────────────────────────────────────────────

type adminPatchUserInput struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Status     string `json:"status,omitempty" doc:"New status: pending, active, or disabled"`
		GlobalRole string `json:"global_role,omitempty" doc:"New global_role: admin, pi, or member"`
	}
}

type adminPatchUserOutput struct {
	Body struct {
		ID      uuid.UUID `json:"id"`
		Updated []string  `json:"updated"`
	}
}

func (s *Service) handlePatchUser(ctx context.Context, in *adminPatchUserInput) (*adminPatchUserOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequireAdmin(p); err != nil {
		return nil, err
	}

	if in.Body.Status == "" && in.Body.GlobalRole == "" {
		return nil, platform.BadRequest("admin.no_fields", "provide at least one of status or global_role")
	}

	var updated []string

	if in.Body.Status != "" {
		if err := s.auth.SetUserStatus(ctx, in.ID, in.Body.Status); err != nil {
			return nil, err
		}
		updated = append(updated, "status")
	}
	if in.Body.GlobalRole != "" {
		if err := s.auth.SetUserRole(ctx, in.ID, in.Body.GlobalRole); err != nil {
			return nil, err
		}
		updated = append(updated, "global_role")
	}

	out := &adminPatchUserOutput{}
	out.Body.ID = in.ID
	out.Body.Updated = updated
	return out, nil
}

// registerUserHandlers wires the user admin endpoints onto api.
func registerUserHandlers(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-users",
		Method:      http.MethodGet,
		Path:        "/v1/admin/users",
		Summary:     "List users",
		Description: "Returns all users, optionally filtered by status and/or role. Requires admin or pi role.",
		Tags:        []string{"admin"},
	}, svc.handleListUsers)

	huma.Register(api, huma.Operation{
		OperationID: "admin-patch-user",
		Method:      http.MethodPatch,
		Path:        "/v1/admin/users/{id}",
		Summary:     "Update user status or role",
		Description: "Changes a user's status and/or global_role. Requires admin role.",
		Tags:        []string{"admin"},
	}, svc.handlePatchUser)
}
