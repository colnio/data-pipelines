package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/colnio/data-pipelines/internal/domain"
	"github.com/colnio/data-pipelines/internal/jobs"
	"github.com/colnio/data-pipelines/internal/run"
	"github.com/colnio/data-pipelines/internal/statemachine"
	"github.com/colnio/data-pipelines/internal/transfer"
)

// AgentTransport pulls a run's packed archive from its measurement-PC agent
// (architecture §13: "Data movement is server pull from the agent"). The
// production implementation does an authenticated HTTP GET to the agent; tests
// supply a fake that copies a local file.
type AgentTransport interface {
	FetchArchive(ctx context.Context, agentID string, r domain.Run, archiveName, destPath string) error
}

// PipelineConfig configures the transfer/promote pipeline.
type PipelineConfig struct {
	StagingRoot     string
	RawRoot         string
	MaxArchiveBytes int64
	Unpack          transfer.UnpackOptions
	WorkerID        string
}

// Pipeline drives a declared run through pull → verify → safe-unpack → promote
// (architecture §13). It is the JobPullRun handler. Every step is a legal,
// audited state transition; failures land the run in a typed failure state and
// return an error so the durable queue retries (or, for unsafe archives, halts).
type Pipeline struct {
	pool      *pgxpool.Pool
	runs      *run.Repo
	transport AgentTransport
	queue     *jobs.Queue // enqueues the parse_run job handed off to the Python worker
	cfg       PipelineConfig
	log       *slog.Logger
}

// NewPipeline constructs the pipeline worker. queue may be nil (then no parse
// job is enqueued after promotion — used by focused tests).
func NewPipeline(pool *pgxpool.Pool, runs *run.Repo, transport AgentTransport, queue *jobs.Queue, cfg PipelineConfig, log *slog.Logger) *Pipeline {
	if cfg.WorkerID == "" {
		cfg.WorkerID = "pipeline"
	}
	return &Pipeline{pool: pool, runs: runs, transport: transport, queue: queue, cfg: cfg, log: log}
}

// Handlers returns the job-type → handler map to register with a jobs.Worker.
func (pl *Pipeline) Handlers() map[domain.JobType]jobs.Handler {
	return map[domain.JobType]jobs.Handler{
		domain.JobPullRun: pl.HandlePullRun,
	}
}

// HandlePullRun executes the full transfer pipeline for one run. Idempotent: a
// retried job whose run is already promoted (or further) is a no-op success.
func (pl *Pipeline) HandlePullRun(ctx context.Context, job domain.Job) (json.RawMessage, error) {
	if job.RunID == nil {
		return nil, fmt.Errorf("pull_run job %d has no run_id", job.ID)
	}
	runID := *job.RunID
	r, err := pl.runs.Get(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("load run %s: %w", runID, err)
	}

	if isPastIngest(r.State) {
		pl.log.Info("pull_run: run already past ingest, no-op", "run_id", runID, "state", r.State)
		return result(map[string]any{"already_done": true, "state": string(r.State)}), nil
	}
	if r.State == domain.StateUnsafeArchive {
		return nil, fmt.Errorf("run %s halted at unsafe_archive; manual intervention required", runID)
	}

	m, err := pl.runs.Manifest(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("load manifest for %s: %w", runID, err)
	}

	// 1. → pulling (tolerant of re-drive from declared/transfer_failed/manifest_mismatch/unpacked/verified)
	if r.State != domain.StatePulling {
		if err := pl.transition(ctx, runID, r.State, domain.StatePulling, "pull pipeline start"); err != nil {
			return nil, err
		}
	}

	stagingRun := filepath.Join(pl.cfg.StagingRoot, sanitizeSeg(runID))
	if err := os.MkdirAll(stagingRun, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir staging: %w", err)
	}
	archivePath := filepath.Join(stagingRun, filepath.Base(m.Archive.Name))

	// 2. pull archive from the agent.
	if err := pl.transport.FetchArchive(ctx, r.AgentID, r, m.Archive.Name, archivePath); err != nil {
		pl.failTo(ctx, runID, domain.StatePulling, domain.StateTransferFailed, "pull failed: "+err.Error())
		return nil, fmt.Errorf("fetch archive: %w", err)
	}
	if pl.cfg.MaxArchiveBytes > 0 {
		if fi, statErr := os.Stat(archivePath); statErr == nil && fi.Size() > pl.cfg.MaxArchiveBytes {
			pl.failTo(ctx, runID, domain.StatePulling, domain.StateTransferFailed, "archive exceeds size cap")
			return nil, fmt.Errorf("archive %d bytes exceeds MaxArchiveBytes %d", fi.Size(), pl.cfg.MaxArchiveBytes)
		}
	}

	// 3. archive hash (catches transit corruption early — architecture §13).
	if err := transfer.VerifyArchiveSHA256(archivePath, m.Archive.SHA256); err != nil {
		pl.failTo(ctx, runID, domain.StatePulling, domain.StateTransferFailed, "archive hash mismatch")
		return nil, fmt.Errorf("verify archive hash: %w", err)
	}

	// 4. safe unpack to staging (never into the raw store).
	unpackDir := filepath.Join(stagingRun, "unpacked")
	_ = os.RemoveAll(unpackDir) // fresh on every (re-)attempt
	extracted, err := transfer.SafeUnpack(archivePath, unpackDir, pl.cfg.Unpack)
	if err != nil {
		if errors.Is(err, transfer.ErrUnsafeArchive) {
			pl.failTo(ctx, runID, domain.StatePulling, domain.StateUnsafeArchive, "unsafe archive: "+err.Error())
		} else {
			pl.failTo(ctx, runID, domain.StatePulling, domain.StateTransferFailed, "unpack failed: "+err.Error())
		}
		return nil, fmt.Errorf("safe unpack: %w", err)
	}
	if err := pl.transition(ctx, runID, domain.StatePulling, domain.StateUnpacked, "safe unpack ok"); err != nil {
		return nil, err
	}

	// 5. per-file + manifest verification (authoritative for stored raw data).
	if err := transfer.VerifyAgainstManifest(extracted, m, false); err != nil {
		pl.failTo(ctx, runID, domain.StateUnpacked, domain.StateManifestMismatch, "manifest verify failed: "+err.Error())
		return nil, fmt.Errorf("verify files against manifest: %w", err)
	}
	if err := pl.transition(ctx, runID, domain.StateUnpacked, domain.StateVerified, "per-file hashes verified"); err != nil {
		return nil, err
	}

	// 6. atomic promote into the immutable raw store ("files first, DB last").
	rawRun := pl.rawRunDir(r, runID)
	if err := os.MkdirAll(filepath.Dir(rawRun), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir raw parent: %w", err)
	}
	if err := transfer.Promote(unpackDir, rawRun); err != nil && !errors.Is(err, transfer.ErrAlreadyPromoted) {
		// Stay at 'verified'; the queue retries and re-drives cleanly.
		return nil, fmt.Errorf("promote: %w", err)
	}

	// 7. DB last: record run_files + instrument metadata and flip → promoted, atomically.
	files := make([]domain.RunFile, 0, len(m.Files))
	byName := make(map[string]transfer.ExtractedFile, len(extracted))
	for _, e := range extracted {
		byName[e.RelPath] = e
	}
	for _, mf := range m.Files {
		files = append(files, domain.RunFile{RunID: runID, Name: mf.Name, Bytes: mf.Bytes, SHA256: mf.SHA256})
	}

	tx, err := pl.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := pl.runs.InsertFiles(ctx, tx, runID, files); err != nil {
		return nil, err
	}
	if m.InstrumentJSON != "" {
		if raw, readErr := os.ReadFile(filepath.Join(rawRun, m.InstrumentJSON)); readErr == nil && json.Valid(raw) {
			_ = pl.runs.RecordInstrumentJSON(ctx, tx, runID, m.InstrumentJSON, byName[m.InstrumentJSON].SHA256, raw)
		}
	}
	if _, err := statemachine.TransitionTx(ctx, tx, statemachine.TransitionParams{
		RunID:        runID,
		ExpectedFrom: domain.StateVerified,
		To:           domain.StatePromoted,
		ActorType:    domain.ActorWorker,
		ActorID:      pl.cfg.WorkerID,
		Reason:       "promoted to immutable raw store",
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	// 8. metadata gate (architecture §10): block scientific interpretation, not
	// raw capture. Incomplete device/contact metadata → needs_metadata holding
	// state (no parse enqueued; resolving metadata later re-drives). Otherwise
	// hand off to Pipeline A by enqueuing a parse_run job that the Python worker
	// claims.
	if r.SampleID == nil || r.DeviceID == nil {
		if err := pl.transition(ctx, runID, domain.StatePromoted, domain.StateNeedsMetadata, "device/contact metadata incomplete"); err != nil {
			pl.log.Warn("could not set needs_metadata", "run_id", runID, "err", err)
		}
	} else if pl.queue != nil {
		payload, _ := json.Marshal(struct {
			RunID string `json:"run_id"`
		}{RunID: runID})
		if _, err := pl.queue.Enqueue(ctx, jobs.EnqueueParams{
			JobType:        domain.JobParseRun,
			RunID:          &runID,
			IdempotencyKey: "parse:" + runID,
			Payload:        payload,
		}); err != nil {
			pl.log.Warn("could not enqueue parse_run", "run_id", runID, "err", err)
		}
	}

	pl.log.Info("run promoted", "run_id", runID, "files", len(files), "raw_path", rawRun)
	return result(map[string]any{"promoted": true, "files": len(files), "raw_path": rawRun}), nil
}

// transition performs a legal, audited state change by the worker actor.
func (pl *Pipeline) transition(ctx context.Context, runID string, from, to domain.RunState, reason string) error {
	_, err := statemachine.Transition(ctx, pl.pool, statemachine.TransitionParams{
		RunID:        runID,
		ExpectedFrom: from,
		To:           to,
		ActorType:    domain.ActorWorker,
		ActorID:      pl.cfg.WorkerID,
		Reason:       reason,
	})
	return err
}

// failTo best-effort moves a run into a failure state. The originating error is
// returned by the caller; a failure here is only logged (the queue still
// retries the job).
func (pl *Pipeline) failTo(ctx context.Context, runID string, from, to domain.RunState, reason string) {
	if _, err := statemachine.Transition(ctx, pl.pool, statemachine.TransitionParams{
		RunID:        runID,
		ExpectedFrom: from,
		To:           to,
		ActorType:    domain.ActorWorker,
		ActorID:      pl.cfg.WorkerID,
		Reason:       reason,
	}); err != nil {
		pl.log.Warn("could not record failure transition", "run_id", runID, "to", to, "err", err)
	}
}

// rawRunDir is raw/<sample>/<device>/<run_id> (architecture §5 layout), with
// _unknown placeholders when metadata is not yet known.
func (pl *Pipeline) rawRunDir(r domain.Run, runID string) string {
	sample := "_unknown"
	if r.SampleID != nil && *r.SampleID != "" {
		sample = sanitizeSeg(*r.SampleID)
	}
	device := "_unknown"
	if r.DeviceID != nil && *r.DeviceID != "" {
		device = sanitizeSeg(*r.DeviceID)
	}
	return filepath.Join(pl.cfg.RawRoot, sample, device, sanitizeSeg(runID))
}

// isPastIngest reports whether a run has already completed the transfer/promote
// phase, so the pull pipeline should treat it as done.
func isPastIngest(s domain.RunState) bool {
	switch s {
	case domain.StatePromoted, domain.StateNeedsMetadata, domain.StateParsing,
		domain.StateValidated, domain.StateProcessing, domain.StateAwaitingReview,
		domain.StateApproved, domain.StatePublishing, domain.StatePublished,
		domain.StateChangesRequested:
		return true
	}
	return false
}

// sanitizeSeg makes an id safe as a single path segment (no separators/traversal).
func sanitizeSeg(s string) string {
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, `\`, "_")
	s = strings.TrimSpace(s)
	if s == "" || s == "." || s == ".." {
		return "_"
	}
	return s
}

func result(v map[string]any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
