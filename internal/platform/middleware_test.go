package platform_test

import (
	"testing"

	"github.com/colnio/data-pipelines/internal/platform"
)

func TestRedactLogPath_CalendarToken(t *testing.T) {
	t.Parallel()
	in := "/v1/cal/550e8400-e29b-41d4-a716-446655440000/secret-token-value.ics"
	got := platform.ExportRedactLogPath(in)
	want := "/v1/cal/550e8400-e29b-41d4-a716-446655440000/[redacted]"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRedactLogPath_OtherPathsUnchanged(t *testing.T) {
	t.Parallel()
	path := "/v1/projects/abc/events"
	if platform.ExportRedactLogPath(path) != path {
		t.Fatalf("unexpected redaction of %q", path)
	}
}
