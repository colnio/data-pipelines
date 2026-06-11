// Package manifest provides validation and canonical hashing of the manifest
// contract (architecture §11, §13).
//
// This is a pure package: it depends only on internal/domain and the standard
// library. It has no DB or platform imports.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/colnio/data-pipelines/internal/domain"
)

// runIDRe is the allowed character set for run_id values.
var runIDRe = regexp.MustCompile(`^[A-Za-z0-9._:\-]{1,128}$`)

// sha256Re matches a lowercase 64-character hex string.
var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidationError wraps a list of per-field problems and implements the error
// interface. Callers (e.g. the API layer) can inspect Fields to surface detail.
type ValidationError struct {
	Fields []FieldError
}

// FieldError describes a single validation problem.
type FieldError struct {
	Field   string
	Message string
}

// Error implements the error interface by joining all field problems.
func (e *ValidationError) Error() string {
	msgs := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		msgs[i] = fmt.Sprintf("%s: %s", f.Field, f.Message)
	}
	return "manifest validation errors: " + strings.Join(msgs, "; ")
}

// FieldErrors returns the per-field problem list so API handlers can map them
// to structured HTTP error bodies.
func (e *ValidationError) FieldErrors() []FieldError {
	return e.Fields
}

// Validate is equivalent to ValidateAt(m, time.Now()).
func Validate(m domain.Manifest) error {
	return ValidateAt(m, time.Now())
}

// ValidateAt validates m relative to now. It accumulates ALL problems into a
// single *ValidationError (rather than stopping at the first failure) so
// callers receive a complete picture of what needs to be fixed.
//
// Rules:
//   - SchemaVersion must equal domain.ManifestSchemaVersion.
//   - RunID must be non-empty and match ^[A-Za-z0-9._:-]{1,128}$.
//   - CompletionSource must be domain.SourceInstrument or domain.SourceOperator.
//   - DeclaredBy, AgentID, and MeasurementType must be non-empty.
//   - DeclaredAt must be non-zero and not more than 24h in the future relative to now.
//   - MeasPath must be non-empty and absolute (starts with '/').
//   - Files must be non-empty; each file must have a non-empty Name with no '/'
//     or '\' and not equal to '.' or '..'; Bytes >= 0; SHA256 matches the
//     lowercase 64-hex pattern. File Names must be unique.
//   - InstrumentJSON, if non-empty, must match one of the Files' Name values.
//   - Archive.Name must be non-empty; Archive.SHA256 must match the sha256 pattern.
//   - ConditionLabels entries, if any, must be non-empty.
//   - ParamVersions values, if any, must be >= 1.
func ValidateAt(m domain.Manifest, now time.Time) error {
	var errs []FieldError

	add := func(field, msg string) {
		errs = append(errs, FieldError{Field: field, Message: msg})
	}

	// schema_version
	if m.SchemaVersion != domain.ManifestSchemaVersion {
		add("schema_version", fmt.Sprintf("must be %d, got %d", domain.ManifestSchemaVersion, m.SchemaVersion))
	}

	// run_id
	if m.RunID == "" {
		add("run_id", "must not be empty")
	} else if !runIDRe.MatchString(m.RunID) {
		add("run_id", "must match ^[A-Za-z0-9._:-]{1,128}$")
	}

	// completion_source
	if m.CompletionSource != domain.SourceInstrument && m.CompletionSource != domain.SourceOperator {
		add("completion_source", fmt.Sprintf("must be %q or %q, got %q", domain.SourceInstrument, domain.SourceOperator, m.CompletionSource))
	}

	// required string fields
	if m.DeclaredBy == "" {
		add("declared_by", "must not be empty")
	}
	if m.AgentID == "" {
		add("agent_id", "must not be empty")
	}
	if m.MeasurementType == "" {
		add("measurement_type", "must not be empty")
	}

	// declared_at
	if m.DeclaredAt.IsZero() {
		add("declared_at", "must not be zero")
	} else if m.DeclaredAt.After(now.Add(24 * time.Hour)) {
		add("declared_at", "must not be more than 24h in the future")
	}

	// meas_path
	if m.MeasPath == "" {
		add("meas_path", "must not be empty")
	} else if !strings.HasPrefix(m.MeasPath, "/") {
		add("meas_path", "must be an absolute path (start with '/')")
	}

	// files
	if len(m.Files) == 0 {
		add("files", "must contain at least one file")
	} else {
		seen := make(map[string]bool, len(m.Files))
		for i, f := range m.Files {
			prefix := fmt.Sprintf("files[%d]", i)
			if f.Name == "" {
				add(prefix+".name", "must not be empty")
			} else {
				if strings.ContainsAny(f.Name, "/\\") {
					add(prefix+".name", "must not contain '/' or '\\'")
				}
				if f.Name == "." || f.Name == ".." {
					add(prefix+".name", "must not be '.' or '..'")
				}
				if seen[f.Name] {
					add(prefix+".name", fmt.Sprintf("duplicate file name %q", f.Name))
				}
				seen[f.Name] = true
			}
			if f.Bytes < 0 {
				add(prefix+".bytes", "must be >= 0")
			}
			if !sha256Re.MatchString(f.SHA256) {
				add(prefix+".sha256", "must be a lowercase 64-character hex string")
			}
		}
	}

	// instrument_json
	if m.InstrumentJSON != "" {
		found := false
		for _, f := range m.Files {
			if f.Name == m.InstrumentJSON {
				found = true
				break
			}
		}
		if !found {
			add("instrument_json", fmt.Sprintf("%q is not listed in files", m.InstrumentJSON))
		}
	}

	// archive
	if m.Archive.Name == "" {
		add("archive.name", "must not be empty")
	}
	if !sha256Re.MatchString(m.Archive.SHA256) {
		add("archive.sha256", "must be a lowercase 64-character hex string")
	}

	// condition_labels
	for i, lbl := range m.ConditionLabels {
		if lbl == "" {
			add(fmt.Sprintf("condition_labels[%d]", i), "must not be empty")
		}
	}

	// param_versions
	for k, v := range m.ParamVersions {
		if v < 1 {
			add(fmt.Sprintf("param_versions[%q]", k), "must be >= 1")
		}
	}

	if len(errs) > 0 {
		return &ValidationError{Fields: errs}
	}
	return nil
}

// CanonicalJSON returns deterministic JSON bytes for the same logical manifest.
//
// Go's encoding/json marshals struct fields in their declaration order and map
// keys in sorted order. Slice order (Files, ConditionLabels) is preserved as
// declared — callers must provide the intended canonical ordering. The result
// is therefore byte-for-byte reproducible for structurally equal manifests.
func CanonicalJSON(m domain.Manifest) ([]byte, error) {
	return json.Marshal(m)
}

// Hash returns the lowercase hex sha256 of CanonicalJSON(m). This value is the
// idempotency manifest_hash stored in runs.manifest_hash.
func Hash(m domain.Manifest) (string, error) {
	b, err := CanonicalJSON(m)
	if err != nil {
		return "", fmt.Errorf("manifest.Hash: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
