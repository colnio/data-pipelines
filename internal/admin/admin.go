// Package admin provides the admin panel HTTP API (WS-C): user management,
// agent registration/rotation, and read-only inspection of jobs and audit logs.
package admin

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/colnio/data-pipelines/internal/agentauth"
	"github.com/colnio/data-pipelines/internal/auth"
)

// Service holds the dependencies for the admin API handlers.
type Service struct {
	pool   *pgxpool.Pool
	auth   *auth.Service
	agents *agentauth.Service
}

// NewService constructs a Service wired with the auth and agentauth services.
func NewService(pool *pgxpool.Pool, authSvc *auth.Service, agentSvc *agentauth.Service) *Service {
	return &Service{
		pool:   pool,
		auth:   authSvc,
		agents: agentSvc,
	}
}
