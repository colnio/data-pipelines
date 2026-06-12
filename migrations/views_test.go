package migrations_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/colnio/data-pipelines/internal/testsupport"
)

// TestCatalogViewsAndReadOnlyRole verifies the JupyterHub data-access model
// (architecture §18, §22): the v_* catalog views exist, and the labdata_nb
// notebook user (member of labdata_readonly) can SELECT through the views but is
// denied any access to the base production tables.
func TestCatalogViewsAndReadOnlyRole(t *testing.T) {
	pool := testsupport.NewPool(t) // applies all migrations incl. 00110 views
	ctx := context.Background()

	// 1. Every expected view exists.
	views := []string{
		"v_samples", "v_devices", "v_contact_configs", "v_runs", "v_run_files",
		"v_run_state_transitions", "v_condition_labels", "v_run_condition_labels",
		"v_treatment_events", "v_sample_parameter_versions", "v_contact_geometry_versions",
		"v_processing_parameter_versions", "v_calibration_versions", "v_parser_results",
		"v_analysis_artifacts", "v_review_artifacts", "v_processing_environments",
		"v_published_results", "v_published_artifacts",
	}
	for _, v := range views {
		var reg *string
		if err := pool.QueryRow(ctx, "SELECT to_regclass($1)::text", v).Scan(&reg); err != nil {
			t.Fatalf("to_regclass(%s): %v", v, err)
		}
		if reg == nil {
			t.Errorf("view %s does not exist", v)
		}
	}

	// 2. Apply the read-only role script (the same artifact ops/dev use).
	const nbPass = "nb_test_pass"
	raw, err := os.ReadFile("../deploy/jupyterhub/readonly_role.sql")
	if err != nil {
		t.Fatalf("read readonly_role.sql: %v", err)
	}
	script := strings.ReplaceAll(string(raw), "__NB_PASSWORD__", nbPass)
	if _, err := pool.Exec(ctx, script); err != nil {
		t.Fatalf("apply readonly_role.sql: %v", err)
	}

	// 3. Connect as labdata_nb against the same test database.
	cfg, err := pgx.ParseConfig(testsupport.DSN())
	if err != nil {
		t.Fatal(err)
	}
	cfg.User = "labdata_nb"
	cfg.Password = nbPass
	nbConn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect as labdata_nb: %v", err)
	}
	defer nbConn.Close(ctx)

	// 4. SELECT through a view succeeds.
	var n int
	if err := nbConn.QueryRow(ctx, "SELECT count(*) FROM v_runs").Scan(&n); err != nil {
		t.Errorf("labdata_nb SELECT v_runs should succeed, got: %v", err)
	}

	// 5. SELECT and INSERT on the base table are denied (insufficient_privilege).
	assertDenied(t, nbConn, "SELECT count(*) FROM runs")
	assertDenied(t, nbConn, "INSERT INTO runs(id,manifest_hash,agent_id,completion_source,meas_path,declared_at) VALUES ('x','h','a','operator','/p',now())")
	assertDenied(t, nbConn, "SELECT count(*) FROM users")
}

func assertDenied(t *testing.T, conn *pgx.Conn, sql string) {
	t.Helper()
	_, err := conn.Exec(context.Background(), sql)
	if err == nil {
		t.Errorf("expected permission denied for %q, got nil error", sql)
		return
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Errorf("expected SQLSTATE 42501 (insufficient_privilege) for %q, got: %v", sql, err)
	}
}
