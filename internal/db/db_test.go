package db

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestApplyPoolEnv(t *testing.T) {
	t.Setenv("DB_MAX_CONNS", "12")
	t.Setenv("DB_HEALTH_CHECK_PERIOD", "1m")

	cfg, err := pgxpool.ParseConfig("postgres://unused")
	if err != nil {
		t.Fatal(err)
	}
	applyPoolEnv(cfg)

	if cfg.MaxConns != 12 {
		t.Errorf("MaxConns: got %d want 12", cfg.MaxConns)
	}
	if cfg.HealthCheckPeriod != time.Minute {
		t.Errorf("HealthCheckPeriod: got %v want 1m", cfg.HealthCheckPeriod)
	}
}

func TestApplyPoolEnv_InvalidIgnored(t *testing.T) {
	t.Setenv("DB_MAX_CONNS", "not-a-number")
	t.Setenv("DB_HEALTH_CHECK_PERIOD", "nope")

	cfg, err := pgxpool.ParseConfig("postgres://unused")
	if err != nil {
		t.Fatal(err)
	}
	defaultMax := cfg.MaxConns
	applyPoolEnv(cfg)

	if cfg.MaxConns != defaultMax {
		t.Errorf("invalid DB_MAX_CONNS should be ignored, got MaxConns=%d", cfg.MaxConns)
	}
}
