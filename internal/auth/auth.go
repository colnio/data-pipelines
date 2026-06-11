// Package auth provides human reviewer authentication: email/password accounts
// and HS256 JWT access tokens. It satisfies platform.TokenVerifier so the
// platform AuthResolver middleware can resolve a Principal from the Authorization
// header.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/colnio/data-pipelines/internal/platform"
)

// Config holds the configuration required to run the auth service. It is
// intentionally decoupled from internal/config so the orchestrator maps its
// own config onto this struct.
type Config struct {
	// JWTSigningKey is the HMAC-SHA256 signing key for access tokens.
	JWTSigningKey string

	// AccessTokenTTL is the lifetime of issued access tokens.
	AccessTokenTTL time.Duration

	// AllowedEmailDomains, when non-empty, restricts registration to addresses
	// ending with one of the listed domains (e.g. "example.com"). An empty
	// slice allows any domain.
	AllowedEmailDomains []string

	// IsProduction, when true, requires a non-empty JWTSigningKey and sets
	// newly registered users to status='pending' instead of 'active'.
	IsProduction bool
}

// Service handles human authentication: registration, login, token issuance,
// and token verification. It implements platform.TokenVerifier.
type Service struct {
	pool *pgxpool.Pool
	cfg  Config
	log  *slog.Logger
}

// NewService constructs a Service. Returns an error if cfg.JWTSigningKey is
// empty and cfg.IsProduction is true.
func NewService(pool *pgxpool.Pool, cfg Config, log *slog.Logger) (*Service, error) {
	if cfg.IsProduction && cfg.JWTSigningKey == "" {
		return nil, errors.New("auth: JWTSigningKey must not be empty in production")
	}
	if cfg.AccessTokenTTL == 0 {
		cfg.AccessTokenTTL = 24 * time.Hour
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{pool: pool, cfg: cfg, log: log}, nil
}

// ─── RegisterParams / Register / Login ───────────────────────────────────────

// RegisterParams carries the fields needed to create a new user account.
type RegisterParams struct {
	Email       string
	Password    string
	DisplayName string
}

// Register creates a new user account and returns its UUID. In development the
// account is immediately active; in production it is set to pending.
func (s *Service) Register(ctx context.Context, p RegisterParams) (uuid.UUID, error) {
	email := strings.TrimSpace(strings.ToLower(p.Email))
	if email == "" {
		return uuid.Nil, platform.BadRequest("auth.email_required", "email is required")
	}
	if len(p.Password) < 8 {
		return uuid.Nil, platform.BadRequest("auth.password_too_short", "password must be at least 8 characters")
	}
	if err := s.checkEmailDomainStr(email); err != nil {
		return uuid.Nil, err
	}
	hash, err := hashPassword(p.Password)
	if err != nil {
		return uuid.Nil, fmt.Errorf("auth: hash password: %w", err)
	}
	status := "active"
	if s.cfg.IsProduction {
		status = "pending"
	}
	var id uuid.UUID
	err = s.pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, display_name, global_role, status)
		 VALUES ($1, $2, $3, 'member', $4)
		 RETURNING id`,
		email, hash, strings.TrimSpace(p.DisplayName), status,
	).Scan(&id)
	if err != nil {
		if isDuplicateKey(err) {
			return uuid.Nil, platform.Conflict("auth.email_taken", "an account with this email already exists")
		}
		return uuid.Nil, fmt.Errorf("auth: insert user: %w", err)
	}
	return id, nil
}

// Login verifies email + password and returns a signed access token.
func (s *Service) Login(ctx context.Context, email, password string) (string, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	var (
		userID       string
		passwordHash string
		globalRole   string
		status       string
	)
	err := s.pool.QueryRow(ctx,
		`SELECT id, password_hash, global_role, status FROM users WHERE email = $1`, email,
	).Scan(&userID, &passwordHash, &globalRole, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = checkPassword("$2a$10$invalidhashpaddingtomatch22chars", password)
		return "", platform.Unauthorized("invalid email or password")
	}
	if err != nil {
		return "", fmt.Errorf("auth: load user: %w", err)
	}
	if err := checkPassword(passwordHash, password); err != nil {
		return "", platform.Unauthorized("invalid email or password")
	}
	if status != "active" {
		return "", platform.Errorf(http.StatusForbidden, "auth.account_not_active", "account is not active")
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		return "", fmt.Errorf("auth: parse user id: %w", err)
	}
	return s.issueAccessToken(uid, email, globalRole)
}

// checkEmailDomainStr is the pure-logic domain check used by both Register and handleRegister.
func (s *Service) checkEmailDomainStr(email string) error {
	if len(s.cfg.AllowedEmailDomains) == 0 {
		return nil
	}
	atIdx := strings.LastIndex(email, "@")
	if atIdx < 0 {
		return platform.BadRequest("auth.invalid_email", "invalid email address")
	}
	domain := email[atIdx+1:]
	for _, allowed := range s.cfg.AllowedEmailDomains {
		if strings.EqualFold(domain, allowed) {
			return nil
		}
	}
	return platform.Errorf(http.StatusUnprocessableEntity, "auth.domain_not_allowed", "email domain is not allowed for registration")
}

// isDuplicateKey returns true when err is a Postgres unique-constraint violation.
func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	type pgErr interface{ SQLState() string }
	var pe pgErr
	if errors.As(err, &pe) {
		return pe.SQLState() == "23505"
	}
	return strings.Contains(err.Error(), "23505")
}

// ─── platform.TokenVerifier ──────────────────────────────────────────────────

// VerifyAccessToken parses and validates a first-party HS256 JWT. On success
// it loads the user from the database (must be status='active') and returns the
// corresponding Principal.
func (s *Service) VerifyAccessToken(ctx context.Context, raw string) (*platform.Principal, error) {
	claims, err := s.parseJWT(raw)
	if err != nil {
		return nil, platform.Unauthorized("invalid or expired token")
	}

	// Verify expiry.
	if claims.Exp != 0 && time.Now().Unix() > claims.Exp {
		return nil, platform.Unauthorized("token expired")
	}

	userID, err := uuid.Parse(claims.Sub)
	if err != nil {
		return nil, platform.Unauthorized("malformed token subject")
	}

	// Load user from DB to confirm they are still active.
	var email, role, status string
	err = s.pool.QueryRow(ctx,
		`SELECT email, global_role, status FROM users WHERE id = $1`, userID,
	).Scan(&email, &role, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, platform.Unauthorized("user not found")
	}
	if err != nil {
		return nil, fmt.Errorf("auth: load user: %w", err)
	}
	if status != "active" {
		return nil, platform.Unauthorized("account is not active")
	}

	return &platform.Principal{
		UserID:     userID,
		Email:      email,
		GlobalRole: role,
	}, nil
}

// VerifyPAT is not yet implemented.
func (s *Service) VerifyPAT(_ context.Context, _ string) (*platform.Principal, error) {
	return nil, platform.Unauthorized("personal access tokens not supported yet")
}

// VerifyInternalAIToken is not yet implemented.
func (s *Service) VerifyInternalAIToken(_ context.Context, _ string) (*platform.Principal, error) {
	return nil, platform.Unauthorized("internal tokens not supported")
}

// ─── token issuance ──────────────────────────────────────────────────────────

// jwtHeader is the fixed base64url-encoded HS256 header.
var jwtHeader = base64url([]byte(`{"alg":"HS256","typ":"JWT"}`))

// jwtClaims holds the subset of JWT claims we issue and verify.
type jwtClaims struct {
	Sub   string `json:"sub"`
	Email string `json:"email"`
	Role  string `json:"role"`
	Exp   int64  `json:"exp"`
	Iat   int64  `json:"iat"`
}

// issueAccessToken creates a signed HS256 JWT for the given user.
func (s *Service) issueAccessToken(userID uuid.UUID, email, role string) (string, error) {
	now := time.Now()
	c := jwtClaims{
		Sub:   userID.String(),
		Email: email,
		Role:  role,
		Iat:   now.Unix(),
		Exp:   now.Add(s.cfg.AccessTokenTTL).Unix(),
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("auth: marshal claims: %w", err)
	}
	headerDotPayload := jwtHeader + "." + base64url(payload)
	sig := s.sign(headerDotPayload)
	return headerDotPayload + "." + sig, nil
}

// sign produces the base64url-encoded HMAC-SHA256 signature over message.
func (s *Service) sign(message string) string {
	mac := hmac.New(sha256.New, []byte(s.cfg.JWTSigningKey))
	mac.Write([]byte(message)) //nolint:errcheck // hmac.Write never errors
	return base64url(mac.Sum(nil))
}

// parseJWT validates the JWT signature and returns the decoded claims. It does
// NOT check expiry — the caller must do that.
func (s *Service) parseJWT(token string) (jwtClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return jwtClaims{}, errors.New("malformed jwt")
	}
	headerDotPayload := parts[0] + "." + parts[1]
	expected := s.sign(headerDotPayload)
	// Constant-time comparison to avoid timing oracle.
	if !hmac.Equal([]byte(parts[2]), []byte(expected)) {
		return jwtClaims{}, errors.New("invalid jwt signature")
	}
	rawPayload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return jwtClaims{}, fmt.Errorf("decode jwt payload: %w", err)
	}
	var c jwtClaims
	if err := json.Unmarshal(rawPayload, &c); err != nil {
		return jwtClaims{}, fmt.Errorf("parse jwt claims: %w", err)
	}
	return c, nil
}

// base64url encodes b with RawURLEncoding (no padding), as per the JWT spec.
func base64url(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// ─── password helpers ────────────────────────────────────────────────────────

func hashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("bcrypt: %w", err)
	}
	return string(h), nil
}

func checkPassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}
