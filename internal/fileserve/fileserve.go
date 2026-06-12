// Package fileserve provides secure file serving and preview endpoints for
// analysis artifacts and raw run files (WS-B). Routes are raw chi (not huma)
// because streaming responses and dual-auth (Bearer or signed URL) do not fit
// the huma request/response model cleanly. The three endpoints are:
//
//   GET /v1/runs/{id}/artifacts        — list artifacts with pre-signed URLs
//   GET /v1/runs/{id}/artifacts/{aid}  — stream artifact bytes (dual-auth)
//   GET /v1/runs/{id}/files/{file_id}  — stream raw run file bytes (dual-auth)
//
// The analysis_artifacts table is not owned by any Go module; this package
// performs read-only direct queries against it (noted per AGENTS.md §1).
package fileserve

import (
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/colnio/data-pipelines/internal/config"
	"github.com/colnio/data-pipelines/internal/run"
)

// Service holds the dependencies for the fileserve package.
type Service struct {
	pool *pgxpool.Pool
	runs *run.Repo
	cfg  *config.Config
}

// NewService constructs a Service.
func NewService(pool *pgxpool.Pool, runs *run.Repo, cfg *config.Config) *Service {
	return &Service{pool: pool, runs: runs, cfg: cfg}
}

// Register wires the three file-serving routes onto the provided chi router.
// The router MUST already have the platform AuthResolver middleware installed
// (so PrincipalFrom(ctx) is populated from a Bearer header) — the orchestrator
// mounts fileserve routes on the same main chi.Router that platform.New()
// returns, which already has AuthResolver in its middleware chain.
func Register(r chi.Router, svc *Service) {
	r.Get("/v1/runs/{id}/artifacts", svc.handleListArtifacts)
	r.Get("/v1/runs/{id}/artifacts/{artifact_id}", svc.handleServeArtifact)
	r.Get("/v1/runs/{id}/files/{file_id}", svc.handleServeFile)
}
