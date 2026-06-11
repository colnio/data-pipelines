package manifest_test

import (
	"strings"
	"testing"
	"time"

	"github.com/colnio/data-pipelines/internal/domain"
	"github.com/colnio/data-pipelines/internal/manifest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validSHA256 is a valid lowercase 64-hex string for use in tests.
const validSHA256 = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

// baseTime is a stable reference time used in all ValidateAt calls so tests
// are not sensitive to the real wall clock.
var baseTime = time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

// validManifest returns a manifest that passes all validations.
func validManifest() domain.Manifest {
	return domain.Manifest{
		SchemaVersion:    domain.ManifestSchemaVersion,
		RunID:            "run-001",
		CompletionSource: domain.SourceInstrument,
		DeclaredBy:       "operator-alice",
		AgentID:          "measpc-01",
		MeasurementType:  "fet_transfer_4probe",
		DeclaredAt:       baseTime.Add(-1 * time.Hour),
		MeasPath:         "/data/runs/9D66P1",
		Files: []domain.ManifestFile{
			{Name: "iv_sweep.data", Bytes: 18234, SHA256: validSHA256},
			{Name: "params_run.json", Bytes: 4096, SHA256: validSHA256},
		},
		Archive:         domain.ManifestArchive{Name: "run.tar.zst", SHA256: validSHA256},
		ConditionLabels: []string{"after_uv"},
		ParamVersions:   map[string]int{"sample_stack": 3, "contact_geometry": 2},
		InstrumentJSON:  "params_run.json",
	}
}

// TestValidManifest ensures a well-formed manifest produces no errors.
func TestValidManifest(t *testing.T) {
	m := validManifest()
	err := manifest.ValidateAt(m, baseTime)
	require.NoError(t, err)
}

// TestValidManifestOperatorSource checks that SourceOperator is also accepted.
func TestValidManifestOperatorSource(t *testing.T) {
	m := validManifest()
	m.CompletionSource = domain.SourceOperator
	err := manifest.ValidateAt(m, baseTime)
	require.NoError(t, err)
}

// TestValidate_UsesNow ensures Validate (no time arg) does not reject a
// manifest with a recent DeclaredAt.
func TestValidate_UsesNow(t *testing.T) {
	m := validManifest()
	m.DeclaredAt = time.Now().Add(-5 * time.Minute)
	err := manifest.Validate(m)
	require.NoError(t, err)
}

// assertFieldError checks that err is a *ValidationError containing a FieldError
// for the given field whose message contains wantMsgSubstr.
func assertFieldError(t *testing.T, err error, field, wantMsgSubstr string) {
	t.Helper()
	require.Error(t, err)
	var ve *manifest.ValidationError
	require.ErrorAs(t, err, &ve)
	for _, fe := range ve.FieldErrors() {
		if fe.Field == field && strings.Contains(fe.Message, wantMsgSubstr) {
			return
		}
	}
	t.Errorf("expected FieldError{field=%q, msg contains %q}, got: %v", field, wantMsgSubstr, err)
}

// --- Table-driven invalid cases ---

func TestInvalid_BadSchemaVersion(t *testing.T) {
	m := validManifest()
	m.SchemaVersion = 99
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "schema_version", "must be")
}

func TestInvalid_EmptyRunID(t *testing.T) {
	m := validManifest()
	m.RunID = ""
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "run_id", "must not be empty")
}

func TestInvalid_BadRunIDCharset(t *testing.T) {
	m := validManifest()
	m.RunID = "run id with spaces!"
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "run_id", "must match")
}

func TestInvalid_RunIDTooLong(t *testing.T) {
	m := validManifest()
	m.RunID = strings.Repeat("a", 129)
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "run_id", "must match")
}

func TestInvalid_BadCompletionSource(t *testing.T) {
	m := validManifest()
	m.CompletionSource = "robot"
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "completion_source", "must be")
}

func TestInvalid_EmptyDeclaredBy(t *testing.T) {
	m := validManifest()
	m.DeclaredBy = ""
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "declared_by", "must not be empty")
}

func TestInvalid_EmptyAgentID(t *testing.T) {
	m := validManifest()
	m.AgentID = ""
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "agent_id", "must not be empty")
}

func TestInvalid_EmptyMeasurementType(t *testing.T) {
	m := validManifest()
	m.MeasurementType = ""
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "measurement_type", "must not be empty")
}

func TestInvalid_ZeroDeclaredAt(t *testing.T) {
	m := validManifest()
	m.DeclaredAt = time.Time{}
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "declared_at", "must not be zero")
}

func TestInvalid_FarFutureDeclaredAt(t *testing.T) {
	m := validManifest()
	m.DeclaredAt = baseTime.Add(25 * time.Hour)
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "declared_at", "24h")
}

func TestInvalid_RelativeMeasPath(t *testing.T) {
	m := validManifest()
	m.MeasPath = "data/runs/9D66P1"
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "meas_path", "absolute")
}

func TestInvalid_EmptyMeasPath(t *testing.T) {
	m := validManifest()
	m.MeasPath = ""
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "meas_path", "must not be empty")
}

func TestInvalid_EmptyFiles(t *testing.T) {
	m := validManifest()
	m.Files = nil
	m.InstrumentJSON = "" // avoid secondary error
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "files", "at least one")
}

func TestInvalid_FileNameWithSlash(t *testing.T) {
	m := validManifest()
	m.Files = []domain.ManifestFile{
		{Name: "sub/file.dat", Bytes: 0, SHA256: validSHA256},
	}
	m.InstrumentJSON = ""
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "files[0].name", "'/'")
}

func TestInvalid_FileNameWithBackslash(t *testing.T) {
	m := validManifest()
	m.Files = []domain.ManifestFile{
		{Name: `sub\file.dat`, Bytes: 0, SHA256: validSHA256},
	}
	m.InstrumentJSON = ""
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "files[0].name", `'\'`)
}

func TestInvalid_FileNameDotDot(t *testing.T) {
	m := validManifest()
	m.Files = []domain.ManifestFile{
		{Name: "..", Bytes: 0, SHA256: validSHA256},
	}
	m.InstrumentJSON = ""
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "files[0].name", "'..'")
}

func TestInvalid_FileNameDot(t *testing.T) {
	m := validManifest()
	m.Files = []domain.ManifestFile{
		{Name: ".", Bytes: 0, SHA256: validSHA256},
	}
	m.InstrumentJSON = ""
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "files[0].name", "'.'")
}

func TestInvalid_NegativeBytes(t *testing.T) {
	m := validManifest()
	m.Files = []domain.ManifestFile{
		{Name: "iv_sweep.data", Bytes: -1, SHA256: validSHA256},
	}
	m.InstrumentJSON = ""
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "files[0].bytes", ">= 0")
}

func TestInvalid_UppercaseSHA256(t *testing.T) {
	m := validManifest()
	m.Files = []domain.ManifestFile{
		{Name: "iv_sweep.data", Bytes: 0, SHA256: strings.ToUpper(validSHA256)},
	}
	m.InstrumentJSON = ""
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "files[0].sha256", "lowercase")
}

func TestInvalid_ShortSHA256(t *testing.T) {
	m := validManifest()
	m.Files = []domain.ManifestFile{
		{Name: "iv_sweep.data", Bytes: 0, SHA256: "abc123"},
	}
	m.InstrumentJSON = ""
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "files[0].sha256", "lowercase")
}

func TestInvalid_LongSHA256(t *testing.T) {
	m := validManifest()
	m.Files = []domain.ManifestFile{
		{Name: "iv_sweep.data", Bytes: 0, SHA256: validSHA256 + "00"},
	}
	m.InstrumentJSON = ""
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "files[0].sha256", "lowercase")
}

func TestInvalid_DuplicateFileNames(t *testing.T) {
	m := validManifest()
	m.Files = []domain.ManifestFile{
		{Name: "iv_sweep.data", Bytes: 0, SHA256: validSHA256},
		{Name: "iv_sweep.data", Bytes: 100, SHA256: validSHA256},
	}
	m.InstrumentJSON = ""
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "files[1].name", "duplicate")
}

func TestInvalid_InstrumentJSONNotInFiles(t *testing.T) {
	m := validManifest()
	m.InstrumentJSON = "nonexistent.json"
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "instrument_json", "not listed in files")
}

func TestInvalid_EmptyArchiveName(t *testing.T) {
	m := validManifest()
	m.Archive.Name = ""
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "archive.name", "must not be empty")
}

func TestInvalid_BadArchiveSHA256(t *testing.T) {
	m := validManifest()
	m.Archive.SHA256 = "BADHASH"
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "archive.sha256", "lowercase")
}

func TestInvalid_ParamVersionZero(t *testing.T) {
	m := validManifest()
	m.ParamVersions = map[string]int{"sample_stack": 0}
	err := manifest.ValidateAt(m, baseTime)
	require.Error(t, err)
	var ve *manifest.ValidationError
	require.ErrorAs(t, err, &ve)
	found := false
	for _, fe := range ve.FieldErrors() {
		if strings.Contains(fe.Field, "param_versions") && strings.Contains(fe.Message, ">= 1") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected param_versions >= 1 error, got: %v", err)
}

func TestInvalid_EmptyConditionLabel(t *testing.T) {
	m := validManifest()
	m.ConditionLabels = []string{"good_label", ""}
	assertFieldError(t, manifest.ValidateAt(m, baseTime), "condition_labels[1]", "must not be empty")
}

// --- Hash determinism tests ---

func TestHash_SameManifestSameHash(t *testing.T) {
	m := validManifest()
	h1, err := manifest.Hash(m)
	require.NoError(t, err)
	h2, err := manifest.Hash(m)
	require.NoError(t, err)
	assert.Equal(t, h1, h2, "same manifest must produce the same hash")
}

func TestHash_ReorderingFilesChangesHash(t *testing.T) {
	m1 := validManifest()
	// m1.Files = [iv_sweep.data, params_run.json]

	m2 := validManifest()
	m2.Files = []domain.ManifestFile{
		{Name: "params_run.json", Bytes: 4096, SHA256: validSHA256},
		{Name: "iv_sweep.data", Bytes: 18234, SHA256: validSHA256},
	}
	m2.InstrumentJSON = "params_run.json"

	h1, err := manifest.Hash(m1)
	require.NoError(t, err)
	h2, err := manifest.Hash(m2)
	require.NoError(t, err)

	assert.NotEqual(t, h1, h2, "different file order must produce different hashes")
}

// TestHash_MapInsertionOrderIndependent verifies that ParamVersions maps with
// different insertion orders (but same keys+values) produce the same hash.
// Go's encoding/json marshals map keys in sorted order, making this deterministic.
func TestHash_MapInsertionOrderIndependent(t *testing.T) {
	// Build two manifests whose ParamVersions have the same keys+values but
	// were populated in different orders.
	m1 := validManifest()
	m1.ParamVersions = make(map[string]int)
	m1.ParamVersions["sample_stack"] = 3
	m1.ParamVersions["contact_geometry"] = 2
	m1.ParamVersions["device_layer"] = 7

	m2 := validManifest()
	m2.ParamVersions = make(map[string]int)
	m2.ParamVersions["device_layer"] = 7
	m2.ParamVersions["contact_geometry"] = 2
	m2.ParamVersions["sample_stack"] = 3

	h1, err := manifest.Hash(m1)
	require.NoError(t, err)
	h2, err := manifest.Hash(m2)
	require.NoError(t, err)

	assert.Equal(t, h1, h2, "map insertion order must not affect the hash")
}

// TestHash_LowercaseHex verifies the hash is a valid lowercase 64-hex string.
func TestHash_LowercaseHex(t *testing.T) {
	m := validManifest()
	h, err := manifest.Hash(m)
	require.NoError(t, err)
	assert.Len(t, h, 64)
	assert.Equal(t, strings.ToLower(h), h, "hash must be lowercase hex")
}

// TestCanonicalJSON_Deterministic verifies CanonicalJSON returns identical bytes
// on repeated calls for the same manifest.
func TestCanonicalJSON_Deterministic(t *testing.T) {
	m := validManifest()
	b1, err := manifest.CanonicalJSON(m)
	require.NoError(t, err)
	b2, err := manifest.CanonicalJSON(m)
	require.NoError(t, err)
	assert.Equal(t, b1, b2)
}

// TestAccumulatesMultipleErrors verifies that ValidateAt does not short-circuit
// on the first error but collects all of them.
func TestAccumulatesMultipleErrors(t *testing.T) {
	// Deliberately break several fields at once.
	m := domain.Manifest{
		SchemaVersion:    99,          // bad
		RunID:            "",          // bad
		CompletionSource: "x",         // bad
		DeclaredBy:       "",          // bad
		AgentID:          "",          // bad
		MeasurementType:  "",          // bad
		DeclaredAt:       time.Time{}, // bad (zero)
		MeasPath:         "relative",  // bad
		Files:            nil,         // bad
		Archive:          domain.ManifestArchive{Name: "", SHA256: "bad"},
	}
	err := manifest.ValidateAt(m, baseTime)
	require.Error(t, err)
	var ve *manifest.ValidationError
	require.ErrorAs(t, err, &ve)
	// At minimum we should see errors for schema_version, run_id, completion_source,
	// declared_by, agent_id, measurement_type, declared_at, meas_path, files,
	// archive.name, archive.sha256.
	assert.GreaterOrEqual(t, len(ve.FieldErrors()), 9, "expected at least 9 accumulated errors")
}
