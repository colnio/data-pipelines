package migrations_test

import (
	"context"
	"testing"

	"github.com/colnio/data-pipelines/internal/testsupport"
)

// TestSpineMigratesAndHasTables verifies that the full migration set applies
// cleanly and that the expected spine tables exist in the test database.
func TestSpineMigratesAndHasTables(t *testing.T) {
	pool := testsupport.NewPool(t)

	tables := []string{
		"samples",
		"devices",
		"contact_configs",
		"sample_parameter_versions",
		"condition_labels",
		"parser_results",
		"review_decisions",
		"published_results",
		"processing_environments",
		"notifications",
		"users",
	}

	ctx := context.Background()
	for _, tbl := range tables {
		t.Run(tbl, func(t *testing.T) {
			var oid *string
			err := pool.QueryRow(ctx,
				"SELECT to_regclass($1)::text", tbl,
			).Scan(&oid)
			if err != nil {
				t.Fatalf("query to_regclass(%q): %v", tbl, err)
			}
			if oid == nil {
				t.Fatalf("table %q does not exist (to_regclass returned NULL)", tbl)
			}
		})
	}
}
