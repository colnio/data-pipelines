package domain

import "time"

// ManifestSchemaVersion is the manifest schema version this server understands.
// The agent writes schema_version into every manifest; the server rejects
// versions it does not know how to handle.
const ManifestSchemaVersion = 1

// Manifest is the universal precondition for transfer and the metadata contract
// between a measurement-PC agent and the server (architecture §11).
//
// It carries identity and provenance only. It must NOT copy structural values
// such as oxide thickness or L/W; it references IDs and parameter versions
// instead. Structural truth lives in Postgres.
//
// The manifest is written last and atomically on the measurement PC
// (manifest.json.tmp → fsync → rename), so the server should never observe a
// half-written manifest.
type Manifest struct {
	RunID            string           `json:"run_id"`
	SchemaVersion    int              `json:"schema_version"`
	CompletionSource CompletionSource `json:"completion_source"`
	DeclaredBy       string           `json:"declared_by"`
	DeclaredAt       time.Time        `json:"declared_at"`

	AgentID  string `json:"agent_id"`
	MeasPath string `json:"meas_path"`

	SampleID        string         `json:"sample_id"`
	DeviceID        string         `json:"device_id"`
	ContactConfigID string         `json:"contact_config_id"`
	ParamVersions   map[string]int `json:"param_versions"`

	MeasurementType string   `json:"measurement_type"`
	ConditionLabels []string `json:"condition_labels"`
	OperatorComment string   `json:"operator_comment"`

	InstrumentJSON string `json:"instrument_json"`

	Files   []ManifestFile  `json:"files"`
	Archive ManifestArchive `json:"archive"`
}

// ManifestFile describes one file in the frozen run set. The sha256 here is
// authoritative for the final stored raw data (§13, "two hash layers").
type ManifestFile struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// ManifestArchive describes the packed archive the server pulls. Its sha256
// catches transit corruption early, before per-file verification.
type ManifestArchive struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}
