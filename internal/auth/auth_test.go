package auth_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/colnio/data-pipelines/internal/auth"
	"github.com/colnio/data-pipelines/internal/testsupport"
)

func newTestService(t *testing.T) (*auth.Service, func()) {
	t.Helper()
	pool := testsupport.NewPool(t)
	svc, err := auth.NewService(pool, auth.Config{
		JWTSigningKey:  "test-signing-key-not-production",
		AccessTokenTTL: 15 * time.Minute,
		IsProduction:   false,
	}, nil)
	require.NoError(t, err)
	cleanup := func() {
		testsupport.Truncate(t, pool, "users")
	}
	return svc, cleanup
}

// TestRegisterLoginVerify tests the main happy path:
// register → login → VerifyAccessToken → correct Principal returned.
func TestRegisterLoginVerify(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	ctx := context.Background()

	uid, err := svc.Register(ctx, auth.RegisterParams{
		Email:       "alice@example.com",
		Password:    "securepassword1",
		DisplayName: "Alice",
	})
	require.NoError(t, err)
	require.NotEmpty(t, uid)

	token, err := svc.Login(ctx, "alice@example.com", "securepassword1")
	require.NoError(t, err)
	require.NotEmpty(t, token)

	p, err := svc.VerifyAccessToken(ctx, token)
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, uid.String(), p.UserID.String())
	assert.Equal(t, "alice@example.com", p.Email)
	assert.Equal(t, "member", p.GlobalRole)
}

// TestWrongPasswordRejected verifies that a wrong password returns an error.
func TestWrongPasswordRejected(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	ctx := context.Background()
	_, err := svc.Register(ctx, auth.RegisterParams{
		Email:    "bob@example.com",
		Password: "correctpassword",
	})
	require.NoError(t, err)

	_, err = svc.Login(ctx, "bob@example.com", "wrongpassword")
	require.Error(t, err)
}

// TestUnknownEmailRejected verifies that logging in with an unknown email returns an error.
func TestUnknownEmailRejected(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	ctx := context.Background()
	_, err := svc.Login(ctx, "nobody@example.com", "somepassword")
	require.Error(t, err)
}

// TestDisallowedEmailDomainRejected verifies that registration with a domain not
// in AllowedEmailDomains is rejected.
func TestDisallowedEmailDomainRejected(t *testing.T) {
	pool := testsupport.NewPool(t)
	defer testsupport.Truncate(t, pool, "users")
	svc, err := auth.NewService(pool, auth.Config{
		JWTSigningKey:       "test-key",
		AccessTokenTTL:      15 * time.Minute,
		AllowedEmailDomains: []string{"allowed.com"},
		IsProduction:        false,
	}, nil)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = svc.Register(ctx, auth.RegisterParams{
		Email:    "user@disallowed.com",
		Password: "password123",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "domain")
}

// TestAllowedEmailDomainAccepted verifies that an email under an allowed domain is accepted.
func TestAllowedEmailDomainAccepted(t *testing.T) {
	pool := testsupport.NewPool(t)
	defer testsupport.Truncate(t, pool, "users")
	svc, err := auth.NewService(pool, auth.Config{
		JWTSigningKey:       "test-key",
		AccessTokenTTL:      15 * time.Minute,
		AllowedEmailDomains: []string{"allowed.com"},
		IsProduction:        false,
	}, nil)
	require.NoError(t, err)

	ctx := context.Background()
	uid, err := svc.Register(ctx, auth.RegisterParams{
		Email:    "user@allowed.com",
		Password: "password123",
	})
	require.NoError(t, err)
	require.NotEmpty(t, uid.String())
}

// TestDuplicateEmailConflict verifies that registering the same email twice returns a conflict.
func TestDuplicateEmailConflict(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	ctx := context.Background()
	_, err := svc.Register(ctx, auth.RegisterParams{
		Email:    "charlie@example.com",
		Password: "password123",
	})
	require.NoError(t, err)

	_, err = svc.Register(ctx, auth.RegisterParams{
		Email:    "charlie@example.com",
		Password: "different123",
	})
	require.Error(t, err)
}

// TestExpiredTokenRejected verifies that an expired JWT is rejected.
func TestExpiredTokenRejected(t *testing.T) {
	pool := testsupport.NewPool(t)
	defer testsupport.Truncate(t, pool, "users")
	svc, err := auth.NewService(pool, auth.Config{
		JWTSigningKey:  "test-key",
		AccessTokenTTL: -1 * time.Second, // already expired on issue
		IsProduction:   false,
	}, nil)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = svc.Register(ctx, auth.RegisterParams{
		Email:    "expired@example.com",
		Password: "password123",
	})
	require.NoError(t, err)

	token, err := svc.Login(ctx, "expired@example.com", "password123")
	require.NoError(t, err)

	_, err = svc.VerifyAccessToken(ctx, token)
	require.Error(t, err)
}

// TestGarbageTokenRejected verifies that a garbage string is rejected.
func TestGarbageTokenRejected(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	ctx := context.Background()
	_, err := svc.VerifyAccessToken(ctx, "not.a.jwt")
	require.Error(t, err)

	_, err = svc.VerifyAccessToken(ctx, "")
	require.Error(t, err)

	_, err = svc.VerifyAccessToken(ctx, "garbage")
	require.Error(t, err)
}

// TestPendingUserCannotLogin verifies that a pending user is rejected at login.
func TestPendingUserCannotLogin(t *testing.T) {
	pool := testsupport.NewPool(t)
	defer testsupport.Truncate(t, pool, "users")

	// Production mode: new accounts are 'pending'.
	svc, err := auth.NewService(pool, auth.Config{
		JWTSigningKey:  "prod-key",
		AccessTokenTTL: 15 * time.Minute,
		IsProduction:   true,
	}, nil)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = svc.Register(ctx, auth.RegisterParams{
		Email:    "pending@example.com",
		Password: "password123",
	})
	require.NoError(t, err)

	_, err = svc.Login(ctx, "pending@example.com", "password123")
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "active")
}

// TestDisabledUserCannotLogin verifies that a disabled user is rejected at login.
func TestDisabledUserCannotLogin(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	pool := testsupport.NewPool(t)
	ctx := context.Background()

	_, err := svc.Register(ctx, auth.RegisterParams{
		Email:    "disabled@example.com",
		Password: "password123",
	})
	require.NoError(t, err)

	// Manually disable the user.
	_, err = pool.Exec(ctx,
		`UPDATE users SET status = 'disabled' WHERE email = $1`, "disabled@example.com")
	require.NoError(t, err)

	_, err = svc.Login(ctx, "disabled@example.com", "password123")
	require.Error(t, err)
}

// TestProductionEmptyKeyError verifies NewService returns an error when IsProduction=true and key is empty.
func TestProductionEmptyKeyError(t *testing.T) {
	pool := testsupport.NewPool(t)
	_, err := auth.NewService(pool, auth.Config{
		JWTSigningKey: "",
		IsProduction:  true,
	}, nil)
	require.Error(t, err)
}

// TestVerifyPATNotSupported verifies VerifyPAT returns an error.
func TestVerifyPATNotSupported(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	ctx := context.Background()
	_, err := svc.VerifyPAT(ctx, "pat_sometoken")
	require.Error(t, err)
}

// TestVerifyInternalAITokenNotSupported verifies VerifyInternalAIToken returns an error.
func TestVerifyInternalAITokenNotSupported(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	ctx := context.Background()
	_, err := svc.VerifyInternalAIToken(ctx, "iai_sometoken")
	require.Error(t, err)
}

// TestTamperedTokenRejected verifies that a JWT with a tampered signature is rejected.
func TestTamperedTokenRejected(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	ctx := context.Background()
	_, err := svc.Register(ctx, auth.RegisterParams{
		Email:    "tamper@example.com",
		Password: "password123",
	})
	require.NoError(t, err)

	token, err := svc.Login(ctx, "tamper@example.com", "password123")
	require.NoError(t, err)

	// Tamper with the signature (last part).
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)
	tampered := parts[0] + "." + parts[1] + ".invalidsignatureXXX"

	_, err = svc.VerifyAccessToken(ctx, tampered)
	require.Error(t, err)
}

// TestPasswordTooShortRejected verifies that short passwords are rejected at register.
func TestPasswordTooShortRejected(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	ctx := context.Background()
	_, err := svc.Register(ctx, auth.RegisterParams{
		Email:    "short@example.com",
		Password: "short",
	})
	require.Error(t, err)
}

// uniqueEmail generates a unique email to avoid conflicts across test runs.
func uniqueEmail(prefix string) string {
	return fmt.Sprintf("%s+%d@example.com", prefix, time.Now().UnixNano())
}

// TestRegisterLoginVerifyUnique is the same round-trip test but with a unique email
// to avoid state pollution when tests are run multiple times against the same DB.
func TestRegisterLoginVerifyUnique(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	ctx := context.Background()
	email := uniqueEmail("roundtrip")

	uid, err := svc.Register(ctx, auth.RegisterParams{
		Email:       email,
		Password:    "password12345",
		DisplayName: "Round Trip",
	})
	require.NoError(t, err)

	token, err := svc.Login(ctx, email, "password12345")
	require.NoError(t, err)

	p, err := svc.VerifyAccessToken(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, uid.String(), p.UserID.String())
	assert.Equal(t, email, p.Email)
}
