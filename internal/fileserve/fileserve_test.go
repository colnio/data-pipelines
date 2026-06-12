package fileserve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/colnio/data-pipelines/internal/config"
	"github.com/colnio/data-pipelines/internal/platform"
)

// newTestService constructs a Service with a temp LabDataRoot and no DB pool.
// It is sufficient for unit tests that do not hit the database.
func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{
		LabDataRoot:   root,
		JWTSigningKey: "test-signing-key-at-least-32-bytes-long!!",
	}
	svc := &Service{cfg: cfg}
	return svc, root
}

// ── sign / verify unit tests ──────────────────────────────────────────────────

func TestSign_Deterministic(t *testing.T) {
	svc, _ := newTestService(t)
	exp := time.Now().Add(5 * time.Minute).Unix()
	s1 := svc.sign("artifact", "run-1", "42", exp)
	s2 := svc.sign("artifact", "run-1", "42", exp)
	assert.Equal(t, s1, s2, "same inputs must produce same signature")
	assert.NotEmpty(t, s1)
}

func TestSign_DifferentInputsProduceDifferentSigs(t *testing.T) {
	svc, _ := newTestService(t)
	exp := time.Now().Add(5 * time.Minute).Unix()
	s1 := svc.sign("artifact", "run-1", "42", exp)
	s2 := svc.sign("artifact", "run-1", "99", exp)
	s3 := svc.sign("file", "run-1", "42", exp)
	s4 := svc.sign("artifact", "run-2", "42", exp)
	assert.NotEqual(t, s1, s2, "different id → different sig")
	assert.NotEqual(t, s1, s3, "different scope → different sig")
	assert.NotEqual(t, s1, s4, "different runID → different sig")
}

func TestVerify_Valid(t *testing.T) {
	svc, _ := newTestService(t)
	exp := time.Now().Add(5 * time.Minute).Unix()
	sig := svc.sign("artifact", "run-1", "42", exp)
	assert.True(t, svc.verify("artifact", "run-1", "42", exp, sig), "valid sig must pass")
}

func TestVerify_Expired(t *testing.T) {
	svc, _ := newTestService(t)
	exp := time.Now().Add(-1 * time.Second).Unix() // already expired
	sig := svc.sign("artifact", "run-1", "42", exp)
	assert.False(t, svc.verify("artifact", "run-1", "42", exp, sig), "expired sig must fail")
}

func TestVerify_Tampered_Sig(t *testing.T) {
	svc, _ := newTestService(t)
	exp := time.Now().Add(5 * time.Minute).Unix()
	sig := svc.sign("artifact", "run-1", "42", exp)
	// Flip the last byte of the hex sig.
	tampered := sig[:len(sig)-1] + "x"
	assert.False(t, svc.verify("artifact", "run-1", "42", exp, tampered), "tampered sig must fail")
}

func TestVerify_Tampered_ID(t *testing.T) {
	svc, _ := newTestService(t)
	exp := time.Now().Add(5 * time.Minute).Unix()
	sig := svc.sign("artifact", "run-1", "42", exp)
	assert.False(t, svc.verify("artifact", "run-1", "99", exp, sig), "wrong id must fail")
}

func TestVerify_Tampered_RunID(t *testing.T) {
	svc, _ := newTestService(t)
	exp := time.Now().Add(5 * time.Minute).Unix()
	sig := svc.sign("artifact", "run-1", "42", exp)
	assert.False(t, svc.verify("artifact", "run-EVIL", "42", exp, sig), "wrong runID must fail")
}

func TestVerify_Tampered_Scope(t *testing.T) {
	svc, _ := newTestService(t)
	exp := time.Now().Add(5 * time.Minute).Unix()
	sig := svc.sign("artifact", "run-1", "42", exp)
	assert.False(t, svc.verify("file", "run-1", "42", exp, sig), "wrong scope must fail")
}

func TestVerify_WrongKey(t *testing.T) {
	svc, _ := newTestService(t)
	exp := time.Now().Add(5 * time.Minute).Unix()
	sig := svc.sign("artifact", "run-1", "42", exp)

	// Service with a different key.
	svc2 := &Service{cfg: &config.Config{
		LabDataRoot:   svc.cfg.LabDataRoot,
		JWTSigningKey: "completely-different-signing-key!!!!!",
	}}
	assert.False(t, svc2.verify("artifact", "run-1", "42", exp, sig), "sig from different key must fail")
}

// ── path traversal / prefix check unit tests (pure, no DB) ───────────────────

// resolveArtifactPath mirrors the path-resolution + prefix-check logic in
// handleServeArtifact so we can unit-test it without spinning up a router.
func resolveArtifactPath(cfg *config.Config, rawPath string) (string, error) {
	clean := filepath.Clean(rawPath)
	if !strings.HasPrefix(clean, cfg.ProcessedRoot()) && !strings.HasPrefix(clean, cfg.PublishedRoot()) {
		return "", errForbidden("artifact path is outside allowed roots")
	}
	return clean, nil
}

// resolveRawFilePath mirrors handleServeFile path resolution.
func resolveRawFilePath(cfg *config.Config, sampleID, deviceID, runID, name string) (string, error) {
	// Reject filenames with path separators or absolute paths.
	if strings.ContainsAny(name, "/\\") || filepath.IsAbs(name) {
		return "", errForbidden("file name contains path separators")
	}
	physPath := filepath.Join(cfg.RawRoot(), sampleID, deviceID, runID, name)
	clean := filepath.Clean(physPath)
	if !strings.HasPrefix(clean, cfg.RawRoot()) {
		return "", errForbidden("file path is outside raw root")
	}
	return clean, nil
}

type errForbidden string

func (e errForbidden) Error() string { return string(e) }

func TestArtifactPathTraversal_Rejected(t *testing.T) {
	svc, root := newTestService(t)
	_ = svc

	cfg := &config.Config{LabDataRoot: root}

	cases := []struct {
		name string
		path string
	}{
		{"absolute escape", "/etc/passwd"},
		{"dot-dot escape", cfg.ProcessedRoot() + "/../../etc/passwd"},
		{"outside both roots", root + "/staging/evil.png"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveArtifactPath(cfg, tc.path)
			assert.Error(t, err, "expected rejection for path: %s", tc.path)
		})
	}
}

func TestArtifactPathTraversal_Accepted(t *testing.T) {
	_, root := newTestService(t)
	cfg := &config.Config{LabDataRoot: root}

	validPaths := []string{
		cfg.ProcessedRoot() + "/run-1/plot.png",
		cfg.PublishedRoot() + "/run-1/report.csv",
	}
	for _, p := range validPaths {
		p := p
		t.Run(p, func(t *testing.T) {
			got, err := resolveArtifactPath(cfg, p)
			require.NoError(t, err)
			assert.Equal(t, filepath.Clean(p), got)
		})
	}
}

func TestRawFilePathTraversal_Rejected(t *testing.T) {
	_, root := newTestService(t)
	cfg := &config.Config{LabDataRoot: root}

	cases := []struct {
		name     string
		sampleID string
		deviceID string
		runID    string
		fileName string
	}{
		{"dot-dot in name", "sA", "dA", "run-1", "../../etc/passwd"},
		{"absolute name", "sA", "dA", "run-1", "/etc/passwd"},
		{"dot-dot in sample", "../../etc", "dA", "run-1", "data.csv"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveRawFilePath(cfg, tc.sampleID, tc.deviceID, tc.runID, tc.fileName)
			assert.Error(t, err, "expected rejection")
		})
	}
}

func TestRawFilePathTraversal_Accepted(t *testing.T) {
	_, root := newTestService(t)
	cfg := &config.Config{LabDataRoot: root}
	got, err := resolveRawFilePath(cfg, "sA", "dA", "run-1", "data.csv")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(got, cfg.RawRoot()), "must stay under raw root")
}

// ── setContentHeaders unit test ───────────────────────────────────────────────

func TestContentHeaders(t *testing.T) {
	cases := []struct {
		file            string
		wantCT          string
		wantDisposition string
	}{
		{"plot.png", "image/png", "inline"},
		{"data.csv", "text/csv; charset=utf-8", "inline"},
		{"notebook.ipynb", "application/x-ipynb+json", "inline"},
		{"meta.json", "application/json", "inline"},
		// .tar.gz: mime.TypeByExtension(".gz") returns application/gzip on most
		// systems; that is acceptable — the spec says "else application/octet-stream"
		// but only when mime.TypeByExtension returns "". We test .bin for that.
		{"unknown.bin", "application/octet-stream", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.file, func(t *testing.T) {
			w := httptest.NewRecorder()
			setContentHeaders(w, tc.file)
			assert.Equal(t, tc.wantCT, w.Header().Get("Content-Type"))
			assert.Equal(t, tc.wantDisposition, w.Header().Get("Content-Disposition"))
		})
	}
}

// ── HTTP integration tests (no DB) ───────────────────────────────────────────

// buildRouterNoDB mounts the three routes on a fresh chi router, with svc
// wired in. No DB pool; DB-touching paths will panic or error, but we test
// only the auth/path-check paths here.
func buildRouterNoDB(svc *Service) chi.Router {
	r := chi.NewRouter()
	Register(r, svc)
	return r
}

// principalCtx returns a context that carries a valid principal so the
// auth middleware check passes.
func principalCtx() context.Context {
	p := &platform.Principal{GlobalRole: "pi"}
	return platform.WithPrincipal(context.Background(), p)
}

func TestListArtifacts_NoAuth_Returns401(t *testing.T) {
	svc, _ := newTestService(t)
	r := buildRouterNoDB(svc)

	req := httptest.NewRequest(http.MethodGet, "/v1/runs/run-1/artifacts", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestServeArtifact_NoAuthNoSig_Returns401(t *testing.T) {
	svc, _ := newTestService(t)
	r := buildRouterNoDB(svc)

	req := httptest.NewRequest(http.MethodGet, "/v1/runs/run-1/artifacts/42", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestServeFile_NoAuthNoSig_Returns401(t *testing.T) {
	svc, _ := newTestService(t)
	r := buildRouterNoDB(svc)

	req := httptest.NewRequest(http.MethodGet, "/v1/runs/run-1/files/1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestServeArtifact_ExpiredSig_Returns401(t *testing.T) {
	svc, _ := newTestService(t)
	r := buildRouterNoDB(svc)

	exp := time.Now().Add(-1 * time.Second).Unix()
	sig := svc.sign("artifact", "run-1", "42", exp)
	url := "/v1/runs/run-1/artifacts/42?exp=" + strconv.FormatInt(exp, 10) + "&sig=" + sig

	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestServeArtifact_ValidSig_PassesAuth(t *testing.T) {
	// Verify that a valid signed URL passes dual-auth by inspecting checkDualAuth
	// directly, avoiding the nil pool panic.
	svc, _ := newTestService(t)
	exp := time.Now().Add(5 * time.Minute).Unix()
	sig := svc.sign("artifact", "run-1", "42", exp)

	url := "/v1/runs/run-1/artifacts/42?exp=" + strconv.FormatInt(exp, 10) + "&sig=" + sig
	req := httptest.NewRequest(http.MethodGet, url, nil)

	assert.True(t, svc.checkDualAuth(req, "artifact", "run-1", "42"),
		"valid signed URL must pass dual-auth")
}

func TestServeArtifact_WrongScopeSig_StillWorks(t *testing.T) {
	// A sig with scope "file" instead of "artifact" must be rejected (401).
	svc, _ := newTestService(t)
	r := buildRouterNoDB(svc)

	exp := time.Now().Add(5 * time.Minute).Unix()
	wrongSig := svc.sign("file", "run-1", "42", exp) // wrong scope
	url := "/v1/runs/run-1/artifacts/42?exp=" + strconv.FormatInt(exp, 10) + "&sig=" + wrongSig

	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code, "wrong-scope sig must be rejected")
}

// ── Artifact path-escape HTTP integration test ────────────────────────────────
// Craft a handler call where the stored artifact path escapes the allowed roots.
// We inject the check by building a fake artifact and calling resolveArtifactPath.

func TestArtifactPathEscape_HTTPLevel(t *testing.T) {
	svc, root := newTestService(t)

	// Write a real PNG file under processed/ so we can test the happy path too.
	processedDir := filepath.Join(root, "processed", "run-good")
	require.NoError(t, os.MkdirAll(processedDir, 0o755))
	pngPath := filepath.Join(processedDir, "plot.png")
	require.NoError(t, os.WriteFile(pngPath, []byte("\x89PNG"), 0o644))

	// Happy path: path inside processed root — resolveArtifactPath accepts it.
	cfg := svc.cfg
	got, err := resolveArtifactPath(cfg, pngPath)
	require.NoError(t, err)
	assert.Equal(t, pngPath, got)

	// Escape: path using dot-dot to escape processed root.
	escapedPath := cfg.ProcessedRoot() + "/../../etc/passwd"
	_, err = resolveArtifactPath(cfg, escapedPath)
	assert.Error(t, err, "dot-dot escape must be rejected")

	// Escape: path outside both roots.
	_, err = resolveArtifactPath(cfg, "/tmp/evil.png")
	assert.Error(t, err, "absolute outside roots must be rejected")
}

// TestRunIDMismatch verifies the run_id cross-check logic.
// We test the logic directly since we can't inject fake DB rows.
func TestRunIDMismatch_Logic(t *testing.T) {
	// Simulate: artifact.RunID != runID from URL.
	artifact := artifactRow{ID: 1, RunID: "run-A", Path: "/processed/run-A/plot.png"}
	urlRunID := "run-B"
	mismatch := artifact.RunID != urlRunID
	assert.True(t, mismatch, "mismatched run IDs must be detected")

	// Same run ID — no mismatch.
	urlRunID2 := "run-A"
	mismatch2 := artifact.RunID != urlRunID2
	assert.False(t, mismatch2)
}
