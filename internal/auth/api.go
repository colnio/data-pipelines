package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/colnio/data-pipelines/internal/platform"
)

// Register wires the auth HTTP endpoints onto the huma API.
func Register(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID:   "auth-register",
		Method:        http.MethodPost,
		Path:          "/v1/auth/register",
		Summary:       "Register a new user account",
		Description:   "Creates a new human reviewer account. In development the account is immediately active; in production it requires admin activation.",
		Tags:          []string{"auth"},
		DefaultStatus: http.StatusCreated,
	}, svc.handleRegister)

	huma.Register(api, huma.Operation{
		OperationID: "auth-login",
		Method:      http.MethodPost,
		Path:        "/v1/auth/login",
		Summary:     "Obtain an access token",
		Description: "Verifies email and password; returns a short-lived JWT access token.",
		Tags:        []string{"auth"},
	}, svc.handleLogin)

	huma.Register(api, huma.Operation{
		OperationID: "auth-me",
		Method:      http.MethodGet,
		Path:        "/v1/auth/me",
		Summary:     "Current user profile",
		Description: "Returns the profile of the currently authenticated user.",
		Tags:        []string{"auth"},
	}, svc.handleMe)
}

// ─── register ────────────────────────────────────────────────────────────────

type authRegisterInput struct {
	Body struct {
		Email       string `json:"email" minLength:"3" doc:"User email address"`
		Password    string `json:"password" minLength:"8" doc:"Password (min 8 characters)"`
		DisplayName string `json:"display_name" doc:"Human-readable display name"`
	}
}

type authRegisterOutput struct {
	Body struct {
		UserID string `json:"user_id"`
	}
}

func (s *Service) handleRegister(ctx context.Context, in *authRegisterInput) (*authRegisterOutput, error) {
	email := strings.TrimSpace(strings.ToLower(in.Body.Email))
	password := in.Body.Password
	displayName := strings.TrimSpace(in.Body.DisplayName)

	if email == "" {
		return nil, platform.BadRequest("auth.email_required", "email is required")
	}
	if len(password) < 8 {
		return nil, platform.BadRequest("auth.password_too_short", "password must be at least 8 characters")
	}

	// Domain allowlist check.
	if err := s.checkEmailDomainStr(email); err != nil {
		return nil, err
	}

	hash, err := hashPassword(password)
	if err != nil {
		return nil, platform.Errorf(http.StatusInternalServerError, "internal_error", "failed to hash password")
	}

	// New accounts are 'active' in development, 'pending' in production.
	status := "active"
	if s.cfg.IsProduction {
		status = "pending"
	}

	var userID string
	err = s.pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, display_name, global_role, status)
		 VALUES ($1, $2, $3, 'member', $4)
		 RETURNING id`,
		email, hash, displayName, status,
	).Scan(&userID)
	if err != nil {
		if isDuplicateKey(err) {
			return nil, platform.Conflict("auth.email_taken", "an account with this email already exists")
		}
		return nil, platform.Errorf(http.StatusInternalServerError, "internal_error", "failed to create account")
	}

	s.log.Info("user registered", "user_id", userID, "email", email, "status", status)
	out := &authRegisterOutput{}
	out.Body.UserID = userID
	return out, nil
}

// ─── login ───────────────────────────────────────────────────────────────────

type authLoginInput struct {
	Body struct {
		Email    string `json:"email" doc:"User email address"`
		Password string `json:"password" doc:"Password"`
	}
}

type authLoginOutput struct {
	Body struct {
		AccessToken string      `json:"access_token"`
		TokenType   string      `json:"token_type"`
		ExpiresIn   int64       `json:"expires_in" doc:"Seconds until the token expires"`
		User        userProfile `json:"user"`
	}
}

type userProfile struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	GlobalRole  string `json:"global_role"`
}

func (s *Service) handleLogin(ctx context.Context, in *authLoginInput) (*authLoginOutput, error) {
	email := strings.TrimSpace(strings.ToLower(in.Body.Email))
	password := in.Body.Password

	var (
		userID       string
		passwordHash string
		displayName  string
		globalRole   string
		status       string
	)
	err := s.pool.QueryRow(ctx,
		`SELECT id, password_hash, display_name, global_role, status FROM users WHERE email = $1`,
		email,
	).Scan(&userID, &passwordHash, &displayName, &globalRole, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		// Timing equaliser: always do a bcrypt compare before returning.
		_ = checkPassword("$2a$10$invalidhashpaddingtomatch22chars", password)
		return nil, platform.Unauthorized("invalid email or password")
	}
	if err != nil {
		return nil, platform.Errorf(http.StatusInternalServerError, "internal_error", "database error")
	}

	if err := checkPassword(passwordHash, password); err != nil {
		return nil, platform.Unauthorized("invalid email or password")
	}

	if status != "active" {
		return nil, platform.Errorf(http.StatusForbidden, "auth.account_not_active", "account is not active")
	}

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, platform.Errorf(http.StatusInternalServerError, "internal_error", "invalid user id")
	}

	token, err := s.issueAccessToken(userUUID, email, globalRole)
	if err != nil {
		return nil, platform.Errorf(http.StatusInternalServerError, "internal_error", "failed to issue token")
	}

	out := &authLoginOutput{}
	out.Body.AccessToken = token
	out.Body.TokenType = "bearer"
	out.Body.ExpiresIn = int64(s.cfg.AccessTokenTTL / time.Second)
	out.Body.User = userProfile{
		ID:          userID,
		Email:       email,
		DisplayName: displayName,
		GlobalRole:  globalRole,
	}
	return out, nil
}

// ─── me ──────────────────────────────────────────────────────────────────────

type authMeInput struct{}

type authMeOutput struct {
	Body struct {
		User userProfile `json:"user"`
	}
}

func (s *Service) handleMe(ctx context.Context, _ *authMeInput) (*authMeOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}

	var displayName string
	err := s.pool.QueryRow(ctx,
		`SELECT display_name FROM users WHERE id = $1`, p.UserID,
	).Scan(&displayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, platform.Unauthorized("user not found")
	}
	if err != nil {
		return nil, platform.Errorf(http.StatusInternalServerError, "internal_error", "database error")
	}

	out := &authMeOutput{}
	out.Body.User = userProfile{
		ID:          p.UserID.String(),
		Email:       p.Email,
		DisplayName: displayName,
		GlobalRole:  p.GlobalRole,
	}
	return out, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────
