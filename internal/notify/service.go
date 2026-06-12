package notify

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/colnio/data-pipelines/internal/config"
	"github.com/colnio/data-pipelines/internal/jobs"
)

// Service is the notify module's stateful core. It is the sole writer of the
// notifications table and the notification_config table.
type Service struct {
	pool            *pgxpool.Pool
	cfg             *config.Config
	queue           *jobs.Queue
	log             *slog.Logger
	telegramBaseURL string // default "https://api.telegram.org"; overridable in tests
}

// NewService constructs a Service. The Telegram base URL defaults to
// "https://api.telegram.org" and can be overridden via s.telegramBaseURL for
// tests.
func NewService(pool *pgxpool.Pool, cfg *config.Config, queue *jobs.Queue, logger *slog.Logger) *Service {
	return &Service{
		pool:            pool,
		cfg:             cfg,
		queue:           queue,
		log:             logger,
		telegramBaseURL: "https://api.telegram.org",
	}
}

// NewServiceWithBase constructs a Service with an overridden Telegram base URL.
// This is intended for tests that spin up a local httptest.Server.
func NewServiceWithBase(pool *pgxpool.Pool, cfg *config.Config, queue *jobs.Queue, logger *slog.Logger, telegramBaseURL string) *Service {
	s := NewService(pool, cfg, queue, logger)
	s.telegramBaseURL = telegramBaseURL
	return s
}
