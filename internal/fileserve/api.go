package fileserve

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/colnio/data-pipelines/internal/platform"
	"github.com/colnio/data-pipelines/internal/run"
)

// artifactRow holds a row from analysis_artifacts.
// Note: analysis_artifacts is not owned by any Go module; this is a
// read-only direct query, acceptable per AGENTS.md §1 note in the task.
type artifactRow struct {
	ID              int64
	RunID           string
	AnalysisVersion int
	Kind            string
	Path            string
	SHA256          string
}

// artifactItem is the JSON shape returned in the list endpoint.
type artifactItem struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	Filename string `json:"filename"`
	SHA256   string `json:"sha256"`
	URL      string `json:"url"`
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr writes an ErrorModel-shaped JSON error.
func writeErr(w http.ResponseWriter, e *platform.ErrorModel) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.GetStatus())
	_ = json.NewEncoder(w).Encode(e)
}

// handleListArtifacts handles GET /v1/runs/{id}/artifacts.
// Requires an authenticated principal with ScopeReadRuns.
// Returns a JSON list of artifacts with pre-signed URLs valid for 5 minutes.
func (s *Service) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")

	p, ok := platform.PrincipalFrom(r.Context())
	if !ok {
		writeErr(w, platform.Unauthorized("authentication required"))
		return
	}
	if err := platform.RequireScope(p, platform.ScopeReadRuns); err != nil {
		if em, ok := err.(*platform.ErrorModel); ok {
			writeErr(w, em)
		} else {
			writeErr(w, platform.Forbidden(err.Error()))
		}
		return
	}

	artifacts, err := s.queryArtifacts(r.Context(), runID)
	if err != nil {
		writeErr(w, platform.Errorf(http.StatusInternalServerError, "internal_error", "failed to query artifacts"))
		return
	}

	exp := time.Now().Unix() + 300 // 5 minutes
	items := make([]artifactItem, 0, len(artifacts))
	for _, a := range artifacts {
		idStr := strconv.FormatInt(a.ID, 10)
		sig := s.sign("artifact", runID, idStr, exp)
		url := fmt.Sprintf("/v1/runs/%s/artifacts/%s?exp=%d&sig=%s", runID, idStr, exp, sig)
		items = append(items, artifactItem{
			ID:       a.ID,
			Kind:     a.Kind,
			Filename: filepath.Base(a.Path),
			SHA256:   a.SHA256,
			URL:      url,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"artifacts": items})
}

// handleServeArtifact handles GET /v1/runs/{id}/artifacts/{artifact_id}.
// Auth = valid Bearer principal OR valid signed query (?exp=...&sig=...).
func (s *Service) handleServeArtifact(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	artifactIDStr := chi.URLParam(r, "artifact_id")

	if !s.checkDualAuth(r, "artifact", runID, artifactIDStr) {
		writeErr(w, platform.Unauthorized("authentication required: provide Bearer token or valid signed URL"))
		return
	}

	artifactID, err := strconv.ParseInt(artifactIDStr, 10, 64)
	if err != nil {
		writeErr(w, platform.BadRequest("fileserve.invalid_artifact_id", "invalid artifact id"))
		return
	}

	artifact, err := s.queryArtifactByID(r.Context(), artifactID)
	if err != nil {
		writeErr(w, platform.NotFound("fileserve.artifact_not_found", "artifact not found"))
		return
	}
	if artifact.RunID != runID {
		writeErr(w, platform.Forbidden("artifact does not belong to this run"))
		return
	}

	clean := filepath.Clean(artifact.Path)
	if !strings.HasPrefix(clean, s.cfg.ProcessedRoot()) && !strings.HasPrefix(clean, s.cfg.PublishedRoot()) {
		writeErr(w, platform.Forbidden("artifact path is outside allowed roots"))
		return
	}

	fi, err := os.Stat(clean)
	if os.IsNotExist(err) || (err == nil && fi.IsDir()) {
		writeErr(w, platform.NotFound("fileserve.artifact_file_not_found", "artifact file not found on disk"))
		return
	}
	if err != nil {
		writeErr(w, platform.Errorf(http.StatusInternalServerError, "internal_error", "cannot stat artifact file"))
		return
	}

	setContentHeaders(w, filepath.Base(clean))

	f, err := os.Open(clean)
	if err != nil {
		writeErr(w, platform.Errorf(http.StatusInternalServerError, "internal_error", "cannot open artifact file"))
		return
	}
	defer f.Close()
	http.ServeContent(w, r, filepath.Base(clean), fi.ModTime(), f)
}

// handleServeFile handles GET /v1/runs/{id}/files/{file_id}.
// Auth = valid Bearer principal OR valid signed query (?exp=...&sig=...).
func (s *Service) handleServeFile(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	fileIDStr := chi.URLParam(r, "file_id")

	if !s.checkDualAuth(r, "file", runID, fileIDStr) {
		writeErr(w, platform.Unauthorized("authentication required: provide Bearer token or valid signed URL"))
		return
	}

	fileID, err := strconv.ParseInt(fileIDStr, 10, 64)
	if err != nil {
		writeErr(w, platform.BadRequest("fileserve.invalid_file_id", "invalid file id"))
		return
	}

	runData, err := s.runs.Get(r.Context(), runID)
	if err == run.ErrNotFound {
		writeErr(w, platform.NotFound("fileserve.run_not_found", "run not found"))
		return
	}
	if err != nil {
		writeErr(w, platform.Errorf(http.StatusInternalServerError, "internal_error", "failed to get run"))
		return
	}

	files, err := s.runs.Files(r.Context(), runID)
	if err != nil {
		writeErr(w, platform.Errorf(http.StatusInternalServerError, "internal_error", "failed to get run files"))
		return
	}

	var found *struct {
		name string
	}
	for _, f := range files {
		if f.ID == fileID {
			found = &struct{ name string }{name: f.Name}
			break
		}
	}
	if found == nil {
		writeErr(w, platform.NotFound("fileserve.file_not_found", "file not found in run"))
		return
	}

	sampleID := derefString(runData.SampleID)
	deviceID := derefString(runData.DeviceID)

	// Reject filenames that contain path separators or are absolute; filepath.Join
	// would otherwise resolve them and potentially escape the raw root.
	if strings.ContainsAny(found.name, "/\\") || filepath.IsAbs(found.name) {
		writeErr(w, platform.Forbidden("file name contains path separators"))
		return
	}

	physPath := filepath.Join(s.cfg.RawRoot(), sampleID, deviceID, runID, found.name)
	clean := filepath.Clean(physPath)
	if !strings.HasPrefix(clean, s.cfg.RawRoot()) {
		writeErr(w, platform.Forbidden("file path is outside raw root"))
		return
	}

	fi, err := os.Stat(clean)
	if os.IsNotExist(err) || (err == nil && fi.IsDir()) {
		writeErr(w, platform.NotFound("fileserve.file_not_found_on_disk", "run file not found on disk"))
		return
	}
	if err != nil {
		writeErr(w, platform.Errorf(http.StatusInternalServerError, "internal_error", "cannot stat run file"))
		return
	}

	setContentHeaders(w, filepath.Base(clean))

	f, err := os.Open(clean)
	if err != nil {
		writeErr(w, platform.Errorf(http.StatusInternalServerError, "internal_error", "cannot open run file"))
		return
	}
	defer f.Close()
	http.ServeContent(w, r, filepath.Base(clean), fi.ModTime(), f)
}

// checkDualAuth returns true if the request carries a valid Bearer principal
// OR a valid signed URL (?exp=...&sig=...).
func (s *Service) checkDualAuth(r *http.Request, scope, runID, id string) bool {
	// Try Bearer principal first.
	if _, ok := platform.PrincipalFrom(r.Context()); ok {
		return true
	}
	// Try signed URL.
	expStr := r.URL.Query().Get("exp")
	sig := r.URL.Query().Get("sig")
	if expStr == "" || sig == "" {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return false
	}
	return s.verify(scope, runID, id, exp, sig)
}

// queryArtifacts fetches all analysis_artifacts rows for the given run.
// analysis_artifacts has no Go module owner; direct read-only query is
// acceptable (AGENTS.md §1 note in task spec).
func (s *Service) queryArtifacts(ctx context.Context, runID string) ([]artifactRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, run_id, analysis_version, COALESCE(kind,''), COALESCE(path,''), COALESCE(sha256,'')
		FROM analysis_artifacts
		WHERE run_id = $1
		ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []artifactRow
	for rows.Next() {
		var a artifactRow
		if err := rows.Scan(&a.ID, &a.RunID, &a.AnalysisVersion, &a.Kind, &a.Path, &a.SHA256); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// queryArtifactByID fetches a single analysis_artifact by primary key.
func (s *Service) queryArtifactByID(ctx context.Context, id int64) (artifactRow, error) {
	var a artifactRow
	err := s.pool.QueryRow(ctx, `
		SELECT id, run_id, analysis_version, COALESCE(kind,''), COALESCE(path,''), COALESCE(sha256,'')
		FROM analysis_artifacts
		WHERE id = $1`, id).
		Scan(&a.ID, &a.RunID, &a.AnalysisVersion, &a.Kind, &a.Path, &a.SHA256)
	return a, err
}

// setContentHeaders sets Content-Type and Content-Disposition based on file extension.
func setContentHeaders(w http.ResponseWriter, name string) {
	ext := strings.ToLower(filepath.Ext(name))
	var ct string
	var inline bool
	switch ext {
	case ".png":
		ct = "image/png"
		inline = true
	case ".csv":
		ct = "text/csv; charset=utf-8"
		inline = true
	case ".ipynb":
		ct = "application/x-ipynb+json"
		inline = true
	case ".json":
		ct = "application/json"
		inline = true
	default:
		ct = mime.TypeByExtension(ext)
		if ct == "" {
			ct = "application/octet-stream"
		}
	}
	w.Header().Set("Content-Type", ct)
	if inline {
		w.Header().Set("Content-Disposition", "inline")
	}
}

// derefString dereferences a *string safely, returning "" if nil.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
