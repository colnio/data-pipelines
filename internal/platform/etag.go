package platform

import (
	"fmt"
	"strings"
)

// FormatETag returns a strong ETag header value for an opaque version token
// (e.g. a page's current revision id). Modules emit this on resources that
// support optimistic concurrency and compare it against If-Match on writes.
func FormatETag(version string) string {
	return fmt.Sprintf("%q", version)
}

// ETagMatches reports whether the client's If-Match header satisfies the
// current ETag. "*" matches any existing resource. Both strong and weakly
// quoted forms are accepted leniently.
func ETagMatches(ifMatch, currentVersion string) bool {
	ifMatch = strings.TrimSpace(ifMatch)
	if ifMatch == "" {
		return false
	}
	if ifMatch == "*" {
		return true
	}
	want := FormatETag(currentVersion)
	for _, part := range strings.Split(ifMatch, ",") {
		p := strings.TrimSpace(part)
		p = strings.TrimPrefix(p, "W/")
		if p == want {
			return true
		}
	}
	return false
}

// PreconditionFailed is the canonical 412 error carrying the current resource
// state so the client can show a conflict/merge UI (spec §7.1.3).
func PreconditionFailed(current any) *ErrorModel {
	return Errorf(412, "precondition_failed", "resource has changed; reload and retry").
		WithDetails(map[string]any{"current": current})
}
