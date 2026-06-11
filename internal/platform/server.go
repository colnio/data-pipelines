// Package platform wires the HTTP server: chi router, huma API, the middleware
// chain, and the structured error envelope. Domain modules register their
// routes against the *huma.API returned here.
package platform

import (
	"fmt"
	"hash/fnv"
	"log/slog"
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Server bundles the chi router and the huma API so cmd/api can register
// module routes and start listening.
type Server struct {
	Router chi.Router
	API    huma.API
	Logger *slog.Logger
}

// New constructs the server with the full middleware chain and a huma API that
// emits OpenAPI 3.1 and uses the structured error envelope. Middleware is
// installed here (before huma mounts routes) in the order:
// requestID → logging → recover → CORS → auth → rate-limit → idempotency.
func New(deps *ServerDeps) *Server {
	logger := deps.Logger
	// Route framework errors through our envelope.
	huma.NewError = humaNewError

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(RequestLogger(logger))
	r.Use(middleware.Recoverer)
	r.Use(CORS(deps.WebOrigin))
	ipRL := deps.AuthIPRateLimiter
	if ipRL == nil {
		ipRL = NewIPRateLimiter(deps.AuthIPPerMinute)
	}
	r.Use(ipRL.LoginRegisterMiddleware())
	r.Use(AuthResolver(deps.Verifier))
	rl := deps.RateLimiter
	if rl == nil {
		rl = NewRateLimiter(deps.PerMinute)
	}
	r.Use(rl.Middleware())
	r.Use(Idempotency(deps.Idempotency))

	config := huma.DefaultConfig("Lab Data Management API", "1.0.0")
	if !deps.Production {
		config.DocsPath = "/docs"
		config.OpenAPIPath = "/openapi"
	}
	config.Info.Description = "REST API for the lab data management system: measurement-run " +
		"ingest, the durable jobs queue, the run state machine, review/publish, and the " +
		"browse/search catalog. Agents POST manifests; reviewers approve in the UI.\n\n" +
		"## Authentication\n\n" +
		"Human endpoints require a JWT access token issued by `POST /v1/auth/login`, passed as " +
		"`Authorization: Bearer <token>`. Measurement-PC agents authenticate to the ingest " +
		"endpoints with a per-agent shared key (HMAC), not a JWT. " +
		"Long-lived integrations may use a Personal Access Token (PAT).\n\n" +
		"## Authorization\n\n" +
		"Human access is role-based (admin / pi / member) plus PAT scopes " +
		"(`read:catalog`, `write:reviews`, …). Every mutation and every run state transition " +
		"is recorded in an immutable audit trail.\n\n" +
		"## Pagination\n\n" +
		"List endpoints that support pagination use an opaque cursor scheme: the response " +
		"includes a `next_cursor` field (empty string when no further pages exist). " +
		"Pass `cursor=<value>` on the next request to advance the page. " +
		"The `limit` parameter controls page size (defaults vary per endpoint; max 200).\n\n" +
		"## Error envelope\n\n" +
		"All error responses share the shape `{\"code\": \"...\", \"message\": \"...\", " +
		"\"details\": {...}}`. `code` is a stable machine-readable string " +
		"(e.g. `run.not_found`, `precondition_failed`), `message` is human-readable, " +
		"and `details` is optional structured context. The HTTP status carries the class."
	config.Servers = []*huma.Server{{URL: "/"}}

	// Modules are authored independently and frequently reuse generic input
	// type names (createInput, linkSampleInput, ...). huma derives OpenAPI
	// component names from Go type names, so two different anonymous request
	// bodies with the same enclosing type name would collide. Disambiguate
	// anonymous structs by their structural signature so parallel modules never
	// clash. Named types keep their clean names.
	if config.Components == nil {
		config.Components = &huma.Components{}
	}
	config.Components.Schemas = huma.NewMapRegistry("#/components/schemas/", disambiguatingSchemaNamer)

	api := humachi.New(r, config)

	s := &Server{Router: r, API: api, Logger: logger}
	s.registerSystemRoutes()
	return s
}

func (s *Server) registerSystemRoutes() {
	// Liveness probe (plain handler, outside huma so it has no auth).
	// huma serves the spec at /openapi.json and /openapi.yaml automatically.
	s.Router.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
}

// disambiguatingSchemaNamer behaves like huma.DefaultSchemaNamer but, for
// unnamed (anonymous) structs — typically inline request/response bodies —
// appends a short hash of the type's full structural signature. Two distinct
// bodies that would otherwise share a hint-derived name (e.g. two modules each
// with a linkSampleInput) get distinct component names; structurally identical
// bodies still share one name.
func disambiguatingSchemaNamer(t reflect.Type, hint string) string {
	base := huma.DefaultSchemaNamer(t, hint)
	dt := t
	for dt.Kind() == reflect.Ptr {
		dt = dt.Elem()
	}
	if dt.Kind() == reflect.Struct && dt.Name() == "" {
		h := fnv.New32a()
		_, _ = h.Write([]byte(dt.String()))
		base = fmt.Sprintf("%s_%x", base, h.Sum32())
	}
	return base
}

// MountLLMSTxt serves a static /llms.txt agent-discovery stub. Track F4 will
// generate this from the OpenAPI spec; for now it points agents at the docs.
func (s *Server) MountLLMSTxt(body string) {
	s.Router.Get("/llms.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(body))
	})
}
