// Command worker runs the background job processor: it claims jobs from the
// durable Postgres queue and drives the transfer/promote pipeline. It shares
// the same database as the API server; LISTEN/NOTIFY is only an optimization,
// polling is authoritative (architecture §15).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	"github.com/colnio/data-pipelines/internal/config"
	"github.com/colnio/data-pipelines/internal/db"
	"github.com/colnio/data-pipelines/internal/domain"
	"github.com/colnio/data-pipelines/internal/ingest"
	"github.com/colnio/data-pipelines/internal/jobs"
	"github.com/colnio/data-pipelines/internal/review"
	"github.com/colnio/data-pipelines/internal/run"
	"github.com/colnio/data-pipelines/internal/transfer"
)

func main() {
	if err := runWorker(); err != nil {
		slog.Error("worker fatal", "err", err)
		os.Exit(1)
	}
}

func runWorker() error {
	_ = godotenv.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	queue := jobs.NewQueue(pool)
	runs := run.NewRepo(pool)

	transport := ingest.NewHTTPTransport(ingest.HTTPTransportConfig{
		BaseURLs:  ingest.ParseBaseURLs(os.Getenv("AGENT_BASE_URLS")),
		ServerKey: os.Getenv("AGENT_SERVER_KEY"),
		MaxBytes:  cfg.MaxArchiveBytes,
		Timeout:   cfg.PullTimeout,
	})

	pipeline := ingest.NewPipeline(pool, runs, transport, queue, ingest.PipelineConfig{
		StagingRoot:     cfg.StagingRoot,
		RawRoot:         cfg.RawRoot(),
		MaxArchiveBytes: cfg.MaxArchiveBytes,
		Unpack: transfer.UnpackOptions{
			MaxTotalBytes: cfg.MaxArchiveBytes,
			MaxFileBytes:  cfg.MaxArchiveBytes,
			MaxEntries:    100000,
		},
		WorkerID: workerID(),
	}, logger)

	// Go worker handlers: transfer pull pipeline + Pipeline-B publish. The
	// Python worker claims parse_run separately; ClaimTypes keeps them disjoint.
	reviewSvc := review.NewService(pool, runs, queue, logger)
	handlers := map[domain.JobType]jobs.Handler{}
	for t, h := range pipeline.Handlers() {
		handlers[t] = h
	}
	for t, h := range reviewSvc.Handlers() {
		handlers[t] = h
	}

	worker := jobs.NewWorker(queue, workerID(), handlers, jobs.WorkerOptions{})
	worker.SetLogger(logger)

	logger.Info("worker starting", "worker_id", workerID(), "raw_root", cfg.RawRoot(), "staging", cfg.StagingRoot)
	return worker.Run(ctx)
}

func workerID() string {
	host, _ := os.Hostname()
	if host == "" {
		host = "worker"
	}
	return host + "-pipeline"
}
