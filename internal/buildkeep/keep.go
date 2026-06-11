// Package buildkeep pins build-time dependencies that are referenced by code
// added in parallel by subagents, so `go mod tidy` does not drop them before
// that code lands. The orchestrator deletes this package once every dependency
// is referenced directly by real code.
package buildkeep

import (
	_ "github.com/klauspost/compress/zstd"   // internal/transfer archive decompression
	_ "github.com/stretchr/testify/assert"   // module + worker tests
	_ "github.com/stretchr/testify/require"  // module + worker tests
	_ "golang.org/x/crypto/bcrypt"           // agent shared-key + human password hashing
)
