package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/colnio/data-pipelines/internal/platform"
)

// UserAdminRow is a minimal user record returned by admin list queries.
type UserAdminRow struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	GlobalRole  string    `json:"global_role"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

var validStatuses = map[string]bool{"pending": true, "active": true, "disabled": true}
var validRoles = map[string]bool{"admin": true, "pi": true, "member": true}

// ListUsers returns all users, optionally filtered by status and/or role.
// limit <= 0 defaults to 50; capped at 500.
func (s *Service) ListUsers(ctx context.Context, status, role string, limit int) ([]UserAdminRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	query := `SELECT id, email, display_name, global_role, status, created_at FROM users WHERE 1=1`
	args := []any{}
	n := 1

	if status != "" {
		if !validStatuses[status] {
			return nil, platform.BadRequest("admin.invalid_status", "status must be pending, active, or disabled")
		}
		query += fmt.Sprintf(" AND status = $%d", n)
		args = append(args, status)
		n++
	}
	if role != "" {
		if !validRoles[role] {
			return nil, platform.BadRequest("admin.invalid_role", "role must be admin, pi, or member")
		}
		query += fmt.Sprintf(" AND global_role = $%d", n)
		args = append(args, role)
		n++
	}
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", n)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("auth admin: list users: %w", err)
	}
	defer rows.Close()

	var users []UserAdminRow
	for rows.Next() {
		var u UserAdminRow
		if err := rows.Scan(&u.ID, &u.Email, &u.DisplayName, &u.GlobalRole, &u.Status, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("auth admin: scan user: %w", err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth admin: iterate users: %w", err)
	}
	return users, nil
}

// SetUserStatus changes the status of the user with the given id.
// status must be one of: pending, active, disabled.
func (s *Service) SetUserStatus(ctx context.Context, id uuid.UUID, status string) error {
	if !validStatuses[status] {
		return platform.BadRequest("admin.invalid_status", "status must be pending, active, or disabled")
	}
	ct, err := s.pool.Exec(ctx,
		`UPDATE users SET status = $1 WHERE id = $2`,
		status, id)
	if err != nil {
		return fmt.Errorf("auth admin: set user status: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return platform.NotFound("admin.user_not_found", "user not found")
	}
	return nil
}

// SetUserRole changes the global_role of the user with the given id.
// role must be one of: admin, pi, member.
func (s *Service) SetUserRole(ctx context.Context, id uuid.UUID, role string) error {
	if !validRoles[role] {
		return platform.BadRequest("admin.invalid_role", "role must be admin, pi, or member")
	}
	ct, err := s.pool.Exec(ctx,
		`UPDATE users SET global_role = $1 WHERE id = $2`,
		role, id)
	if err != nil {
		return fmt.Errorf("auth admin: set user role: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return platform.NotFound("admin.user_not_found", "user not found")
	}
	return nil
}
