package platform

// Role-based authorization helpers. These complement RequireScope (scopes.go):
// scopes restrict PAT / internal-token callers, but first-party browser JWT
// sessions bypass scope enforcement (Principal.HasScope returns true). Admin
// surfaces are gated on the user's global_role instead, so these helpers apply
// to humans and tokens alike.

// RequirePrivileged returns a Forbidden error unless the principal holds an
// elevated platform role (admin or pi). Use for read access to admin surfaces.
func RequirePrivileged(p *Principal) error {
	if !p.IsPrivileged() {
		return Forbidden("admin or pi role required")
	}
	return nil
}

// RequireAdmin returns a Forbidden error unless the principal holds the admin
// role. Use for mutations on admin surfaces (role changes, account activation,
// agent registration / key rotation).
func RequireAdmin(p *Principal) error {
	if !p.IsAdmin() {
		return Forbidden("admin role required")
	}
	return nil
}
