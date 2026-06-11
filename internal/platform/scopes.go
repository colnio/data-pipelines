package platform

import "fmt"

// Scope constants define the capability taxonomy used by PATs and internal
// tokens. JWT browser sessions bypass scope enforcement (HasScope always true).
// PAT and internal callers are limited to their Scopes list.
//
// These are the lab-data domains: the catalog of runs/samples/devices, review
// actions, agent management, and admin/audit.
const (
	ScopeReadCatalog  = "read:catalog" // browse/search runs, samples, devices
	ScopeReadRuns     = "read:runs"
	ScopeWriteRuns    = "write:runs" // operator finalize/correct actions
	ScopeReadSamples  = "read:samples"
	ScopeWriteSamples = "write:samples"
	ScopeReadDevices  = "read:devices"
	ScopeWriteDevices = "write:devices"

	ScopeReadReviews  = "read:reviews"
	ScopeWriteReviews = "write:reviews" // approve / request changes / corrections

	ScopeReadAgents  = "read:agents"
	ScopeWriteAgents = "write:agents" // register agents, manage allowed roots

	ScopeReadJobs = "read:jobs" // inspect the durable queue

	ScopeReadAudit = "read:audit"

	ScopeReadAdmin  = "read:admin"
	ScopeWriteAdmin = "write:admin"

	ScopeManageTokens = "manage:tokens"
	ScopeWriteProfile = "write:profile"
)

// allScopes is the PAT scope allowlist (every Scope* constant above).
var allScopes = []string{
	ScopeReadCatalog,
	ScopeReadRuns, ScopeWriteRuns,
	ScopeReadSamples, ScopeWriteSamples,
	ScopeReadDevices, ScopeWriteDevices,
	ScopeReadReviews, ScopeWriteReviews,
	ScopeReadAgents, ScopeWriteAgents,
	ScopeReadJobs,
	ScopeReadAudit,
	ScopeReadAdmin, ScopeWriteAdmin,
	ScopeManageTokens, ScopeWriteProfile,
}

var allowedScopeSet map[string]struct{}

func init() {
	allowedScopeSet = make(map[string]struct{}, len(allScopes))
	for _, s := range allScopes {
		allowedScopeSet[s] = struct{}{}
	}
}

// IsAllowedScope reports whether s is a known PAT scope string.
func IsAllowedScope(s string) bool {
	_, ok := allowedScopeSet[s]
	return ok
}

// ValidatePATScopes ensures every scope is on the allowlist and at least one is present.
func ValidatePATScopes(scopes []string) error {
	if len(scopes) == 0 {
		return BadRequest("token.scopes_required", "at least one scope is required")
	}
	for _, s := range scopes {
		if !IsAllowedScope(s) {
			return BadRequest("token.invalid_scope", fmt.Sprintf("unknown scope %q", s))
		}
	}
	return nil
}

// RequireScope returns a Forbidden error if the principal does not carry the
// given scope. It delegates to Principal.HasScope.
func RequireScope(p *Principal, scope string) error {
	if !p.HasScope(scope) {
		return Forbidden("token missing scope " + scope)
	}
	return nil
}
