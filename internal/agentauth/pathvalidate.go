// Package agentauth authenticates measurement-PC agents and validates that a
// manifest meas_path resolves under one of the agent's allowed filesystem
// roots (architecture §4, §13 step 3).
package agentauth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrPathNotAllowed is returned by ValidateMeasPath when the supplied path is
// not absolute, contains a traversal element, or does not resolve under any of
// the agent's allowed roots. It is exported so callers can test with
// errors.Is.
var ErrPathNotAllowed = errors.New("meas_path not allowed")

// ResolveUnderRoot checks whether measPath resolves under one of the supplied
// roots. measPath must be absolute and must not contain any ".." element.
//
// Matching rules (all after filepath.Clean):
//   - exact match: cleanedMeasPath == cleanedRoot
//   - subdirectory: cleanedMeasPath has prefix cleanedRoot + string(os.PathSeparator)
//
// The second rule intentionally rejects prefix-attacks such as
// "/data/runs-evil" matching root "/data/runs" — the separator prevents that.
//
// Returns the matched root and ok=true on success, or ("", false) otherwise.
func ResolveUnderRoot(measPath string, roots []string) (matchedRoot string, ok bool) {
	if !filepath.IsAbs(measPath) {
		return "", false
	}
	// Reject any path containing a ".." element before cleaning so that
	// symbolic ".." components cannot slip through os.PathSeparator tricks.
	for _, elem := range strings.Split(measPath, string(os.PathSeparator)) {
		if elem == ".." {
			return "", false
		}
	}
	cleaned := filepath.Clean(measPath)
	for _, root := range roots {
		cr := filepath.Clean(root)
		if cleaned == cr {
			return root, true
		}
		if strings.HasPrefix(cleaned, cr+string(os.PathSeparator)) {
			return root, true
		}
	}
	return "", false
}

// ValidateMeasPath returns ErrPathNotAllowed (wrapped, errors.Is-able) when:
//   - measPath is not absolute
//   - measPath contains a ".." traversal element
//   - roots is empty (fail closed: no roots configured means no access)
//   - measPath does not resolve under any of the provided roots
func ValidateMeasPath(measPath string, roots []string) error {
	if len(roots) == 0 {
		return fmt.Errorf("%w: no allowed roots configured", ErrPathNotAllowed)
	}
	if !filepath.IsAbs(measPath) {
		return fmt.Errorf("%w: path is not absolute", ErrPathNotAllowed)
	}
	_, ok := ResolveUnderRoot(measPath, roots)
	if !ok {
		return fmt.Errorf("%w: %q does not resolve under any allowed root", ErrPathNotAllowed, measPath)
	}
	return nil
}
