package ingest_test

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/colnio/data-pipelines/internal/agentauth"
	"github.com/colnio/data-pipelines/internal/domain"
	"github.com/colnio/data-pipelines/internal/ingest"
	"github.com/colnio/data-pipelines/internal/jobs"
	"github.com/colnio/data-pipelines/internal/run"
	"github.com/colnio/data-pipelines/internal/statemachine"
	"github.com/colnio/data-pipelines/internal/testsupport"
	"github.com/colnio/data-pipelines/internal/transfer"
)

// fakeTransport serves a prebuilt archive from memory, simulating the agent.
type fakeTransport struct {
	archive []byte
	calls   int
}

func (f *fakeTransport) FetchArchive(ctx context.Context, agentID string, r domain.Run, archiveName, destPath string) error {
	f.calls++
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(destPath, f.archive, 0o644)
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// buildTar packs the given files into a flat tar archive.
func buildTar(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}))
		_, err := tw.Write(content)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

// TestIngestVertical_EndToEnd drives a run from agent POST through the pull
// pipeline to the immutable raw store and asserts the full audit trail.
func TestIngestVertical_EndToEnd(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	testsupport.Truncate(t, pool, "run_state_transitions", "run_files", "manifests",
		"instrument_metadata_raw", "jobs", "run_condition_labels", "runs",
		"agent_allowed_roots", "agents")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// ── Register an agent with a shared key + allowed root ────────────────────
	const agentID = "measpc-probestation-01"
	const agentKey = "super-secret-agent-key"
	keyHash, err := agentauth.HashKey(agentKey)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO agents (id, key_hash, enabled) VALUES ($1,$2,true)`, agentID, keyHash)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO agent_allowed_roots (agent_id, root) VALUES ($1,$2)`, agentID, "/data/runs")
	require.NoError(t, err)

	// ── Build the run's files, the manifest, and the archive ──────────────────
	ivData := []byte("V,I\n0.0,1.2e-9\n0.1,3.4e-9\n")
	paramsJSON := []byte(`{"sweep":"transfer","vds":0.1,"instrument":"keithley-4200"}`)
	files := map[string][]byte{"iv_sweep.data": ivData, "params_run.json": paramsJSON}
	archive := buildTar(t, files)

	m := domain.Manifest{
		RunID:            "run-9D66P1-0001",
		SchemaVersion:    domain.ManifestSchemaVersion,
		CompletionSource: domain.SourceInstrument,
		DeclaredBy:       "instrument-software-1",
		DeclaredAt:       time.Now().UTC(),
		AgentID:          agentID,
		MeasPath:         "/data/runs/9D66P1/2026-06-11/gr_mob_31_f1",
		SampleID:         "9D66P1",
		DeviceID:         "gr_mob_31_f1",
		ContactConfigID:  "gr_mob_31_f1__sd_b1b2",
		ParamVersions:    map[string]int{"sample_stack": 3, "contact_geometry": 2},
		MeasurementType:  "fet_transfer_4probe",
		ConditionLabels:  []string{"after_uv"},
		InstrumentJSON:   "params_run.json",
		Files: []domain.ManifestFile{
			{Name: "iv_sweep.data", Bytes: int64(len(ivData)), SHA256: sha(ivData)},
			{Name: "params_run.json", Bytes: int64(len(paramsJSON)), SHA256: sha(paramsJSON)},
		},
		Archive: domain.ManifestArchive{Name: "run.tar", SHA256: sha(archive)},
	}

	// ── Wire the service and POST the manifest through the real huma handler ──
	runs := run.NewRepo(pool)
	queue := jobs.NewQueue(pool)
	svc := ingest.NewService(pool, agentauth.NewService(pool), runs, queue, logger)
	_, api := humatest.New(t)
	ingest.Register(api, svc)

	resp := api.Post("/v1/agents/manifest",
		"X-Agent-Id: "+agentID, "X-Agent-Key: "+agentKey, m)
	require.Equal(t, 200, resp.Code, "body: %s", resp.Body.String())

	var out struct {
		RunID   string `json:"run_id"`
		State   string `json:"state"`
		Created bool   `json:"created"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	assert.Equal(t, m.RunID, out.RunID)
	assert.Equal(t, "declared", out.State)
	assert.True(t, out.Created)

	// Exactly one pull job was enqueued.
	var pullJobs int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE run_id=$1 AND job_type='pull_run'`, m.RunID).Scan(&pullJobs))
	assert.Equal(t, 1, pullJobs)

	// ── Idempotency: re-POST the same manifest ────────────────────────────────
	resp2 := api.Post("/v1/agents/manifest", "X-Agent-Id: "+agentID, "X-Agent-Key: "+agentKey, m)
	require.Equal(t, 200, resp2.Code)
	var out2 struct {
		Created bool `json:"created"`
	}
	require.NoError(t, json.Unmarshal(resp2.Body.Bytes(), &out2))
	assert.False(t, out2.Created, "second POST must not re-create the run")
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE run_id=$1 AND job_type='pull_run'`, m.RunID).Scan(&pullJobs))
	assert.Equal(t, 1, pullJobs, "second POST must not enqueue a duplicate pull job")

	// ── Run the pull pipeline with a fake transport serving the archive ──────
	base := t.TempDir()
	rawRoot := filepath.Join(base, "raw")
	t.Cleanup(func() { // raw files become read-only; restore perms so cleanup can delete
		_ = filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
			if err == nil {
				_ = os.Chmod(p, 0o755)
			}
			return nil
		})
	})

	ft := &fakeTransport{archive: archive}
	pipeline := ingest.NewPipeline(pool, runs, ft, queue, ingest.PipelineConfig{
		StagingRoot: filepath.Join(base, "staging"),
		RawRoot:     rawRoot,
		Unpack:      transfer.UnpackOptions{MaxTotalBytes: 1 << 20, MaxFileBytes: 1 << 20, MaxEntries: 100},
		WorkerID:    "test-worker",
	}, logger)

	job := domain.Job{ID: 1, JobType: domain.JobPullRun, RunID: &m.RunID}
	res, err := pipeline.HandlePullRun(ctx, job)
	require.NoError(t, err, "pipeline should promote the run")
	assert.Contains(t, string(res), "promoted")
	assert.Equal(t, 1, ft.calls)

	// ── Assertions: state, files, immutability, audit ────────────────────────
	state, err := statemachine.CurrentState(ctx, pool, m.RunID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatePromoted, state, "sample+device present → stays promoted")

	runFiles, err := runs.Files(ctx, m.RunID)
	require.NoError(t, err)
	require.Len(t, runFiles, 2)
	byName := map[string]domain.RunFile{}
	for _, f := range runFiles {
		byName[f.Name] = f
	}
	assert.Equal(t, sha(ivData), byName["iv_sweep.data"].SHA256)
	assert.Equal(t, int64(len(ivData)), byName["iv_sweep.data"].Bytes)

	// Raw files exist, are byte-identical, and are read-only (immutable §5).
	rawIV := filepath.Join(rawRoot, "9D66P1", "gr_mob_31_f1", m.RunID, "iv_sweep.data")
	got, err := os.ReadFile(rawIV)
	require.NoError(t, err)
	assert.Equal(t, ivData, got)
	fi, err := os.Stat(rawIV)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o444), fi.Mode().Perm(), "raw files must be read-only after promotion")

	// Instrument JSON recorded.
	var instrCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM instrument_metadata_raw WHERE run_id=$1`, m.RunID).Scan(&instrCount))
	assert.Equal(t, 1, instrCount)

	// Audit trail covers the full transfer path in order.
	transitions, err := runs.Transitions(ctx, m.RunID)
	require.NoError(t, err)
	var path []string
	for _, tr := range transitions {
		path = append(path, string(tr.ToState))
	}
	assert.Equal(t, []string{"pulling", "unpacked", "verified", "promoted"}, path)

	// A parse_run job was handed off to Pipeline A (the Python worker).
	var parseJobs int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE run_id=$1 AND job_type='parse_run'`, m.RunID).Scan(&parseJobs))
	assert.Equal(t, 1, parseJobs)

	// ── Pipeline is idempotent: a retried job is a no-op success ──────────────
	res2, err := pipeline.HandlePullRun(ctx, job)
	require.NoError(t, err)
	assert.Contains(t, string(res2), "already_done")
}

// TestIngest_BadArchiveHash_TransferFailed asserts a corrupt archive lands the
// run in transfer_failed and returns an error (so the queue retries).
func TestIngest_BadArchiveHash_TransferFailed(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	testsupport.Truncate(t, pool, "run_state_transitions", "run_files", "manifests", "jobs", "runs", "agent_allowed_roots", "agents")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runs := run.NewRepo(pool)

	_, err := pool.Exec(ctx, `INSERT INTO agents (id, key_hash, enabled) VALUES ('a1','x',true)`)
	require.NoError(t, err)

	content := []byte("data")
	archive := buildTar(t, map[string][]byte{"f.data": content})
	m := domain.Manifest{
		RunID: "run-bad-1", SchemaVersion: 1, CompletionSource: domain.SourceInstrument,
		DeclaredBy: "x", DeclaredAt: time.Now().UTC(), AgentID: "a1",
		MeasPath: "/data/runs/x", SampleID: "S1", DeviceID: "D1", MeasurementType: "t",
		Files:   []domain.ManifestFile{{Name: "f.data", Bytes: int64(len(content)), SHA256: sha(content)}},
		Archive: domain.ManifestArchive{Name: "run.tar", SHA256: sha([]byte("WRONG"))},
	}
	canonical, _ := json.Marshal(m)
	_, _, err = runs.Create(ctx, run.CreateParams{
		RunID: m.RunID, ManifestHash: "h", AgentID: "a1", MeasurementType: "t",
		CompletionSource: m.CompletionSource, MeasPath: m.MeasPath,
		SampleID: &m.SampleID, DeviceID: &m.DeviceID, DeclaredBy: "x", DeclaredAt: m.DeclaredAt,
		SchemaVersion: 1, CanonicalManifest: canonical,
	})
	require.NoError(t, err)

	pipeline := ingest.NewPipeline(pool, runs, &fakeTransport{archive: archive}, nil, ingest.PipelineConfig{
		StagingRoot: filepath.Join(t.TempDir(), "staging"), RawRoot: filepath.Join(t.TempDir(), "raw"),
		Unpack: transfer.UnpackOptions{MaxTotalBytes: 1 << 20, MaxFileBytes: 1 << 20, MaxEntries: 100},
	}, logger)

	_, err = pipeline.HandlePullRun(ctx, domain.Job{ID: 1, JobType: domain.JobPullRun, RunID: &m.RunID})
	require.Error(t, err)
	state, _ := statemachine.CurrentState(ctx, pool, m.RunID)
	assert.Equal(t, domain.StateTransferFailed, state)
}
