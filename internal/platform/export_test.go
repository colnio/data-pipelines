package platform

// ExportRedactLogPath exposes path redaction for tests.
func ExportRedactLogPath(path string) string {
	return redactLogPath(path)
}
