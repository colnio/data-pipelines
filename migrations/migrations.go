// Package migrations embeds the goose SQL migration files and exposes them as
// an fs.FS for the boot-time migration runner.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
