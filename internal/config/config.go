// Package config loads runtime configuration from environment variables.
// In production the same loader reads a sealed .env; locally it reads .env.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Config holds all runtime configuration for the lab-data server. Every
// external dependency is reached through an endpoint/path override so
// application code is identical in dev and prod.
type Config struct {
	Env  string // "development" | "production"
	Port string

	DatabaseURL string

	// WebOrigin is the frontend origin for CORS / redirects.
	WebOrigin string

	// PublicBaseURL is the URL the outside world uses to reach this API.
	PublicBaseURL string

	// ── Lab-data storage roots (architecture §5) ───────────────────────────
	// LabDataRoot is the base of the on-disk store: raw/, processed/,
	// published/, manifests/, scratch/, notebooks/, and the ingest staging
	// area all live under it. Raw is immutable and write-once after promotion.
	LabDataRoot string
	// StagingRoot is where pulled archives are unpacked and verified before
	// atomic promotion into the immutable raw store. Defaults to
	// <LabDataRoot>/staging.
	StagingRoot string

	// ── Auth signing + cookies (human reviewers) ───────────────────────────
	JWTSigningKey   string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	CookieDomain    string
	CookieSecure    bool

	// AllowedEmailDomains restricts human self-registration.
	AllowedEmailDomains []string

	// ── Agent transfer (architecture §13) ──────────────────────────────────
	// MaxConcurrentPulls caps how many run archives the transfer worker pulls
	// at once, so a burst of declarations cannot saturate the LAN link.
	MaxConcurrentPulls int
	// PullTimeout bounds a single archive pull from an agent.
	PullTimeout time.Duration
	// MaxArchiveBytes rejects archives larger than this (zip/tar-bomb guard).
	MaxArchiveBytes int64

	// ── Notifications ──────────────────────────────────────────────────────
	SMTPHost     string
	SMTPPort     string
	SMTPFrom     string
	TelegramBotToken string

	// LabTimezone is the wall-clock zone for digests/reconciliation schedules.
	LabTimezone string
}

// Load reads configuration from the environment. Missing required values for
// the selected environment cause an error.
func Load() (*Config, error) {
	c := &Config{
		Env:                getenv("APP_ENV", "development"),
		Port:               getenv("PORT", "8080"),
		DatabaseURL:        getenv("DATABASE_URL", "postgres://lab:lab@localhost:5432/lab?sslmode=disable"),
		WebOrigin:          getenv("WEB_ORIGIN", "http://localhost:5173"),
		PublicBaseURL:      getenv("PUBLIC_BASE_URL", ""),
		LabDataRoot:        getenv("LABDATA_ROOT", "/srv/labdata"),
		StagingRoot:        getenv("LABDATA_STAGING_ROOT", ""),
		JWTSigningKey:      getenv("JWT_SIGNING_KEY", defaultJWTSigningKey),
		AccessTokenTTL:     getdur("ACCESS_TOKEN_TTL", 60*time.Minute),
		RefreshTokenTTL:    getdur("REFRESH_TOKEN_TTL", 720*time.Hour),
		CookieDomain:       getenv("COOKIE_DOMAIN", "localhost"),
		CookieSecure:       getbool("COOKIE_SECURE", false),
		MaxConcurrentPulls: getint("MAX_CONCURRENT_PULLS", 2),
		PullTimeout:        getdur("PULL_TIMEOUT", 30*time.Minute),
		MaxArchiveBytes:    getint64("MAX_ARCHIVE_BYTES", 50<<30), // 50 GiB
		SMTPHost:           getenv("SMTP_HOST", "localhost"),
		SMTPPort:           getenv("SMTP_PORT", "1025"),
		SMTPFrom:           getenv("SMTP_FROM", "no-reply@lab.local"),
		TelegramBotToken:   getenv("TELEGRAM_BOT_TOKEN", ""),
		LabTimezone:        getenv("LAB_TIMEZONE", "Asia/Singapore"),
	}
	c.AllowedEmailDomains = splitCSV(getenv("ALLOWED_EMAIL_DOMAINS", "nus.edu.sg,u.nus.edu"))

	if c.StagingRoot == "" {
		c.StagingRoot = filepath.Join(c.LabDataRoot, "staging")
	}
	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if err := c.validateProduction(); err != nil {
		return nil, err
	}
	if c.IsProduction() && c.AccessTokenTTL < 24*time.Hour {
		c.AccessTokenTTL = 24 * time.Hour
	}
	return c, nil
}

const (
	defaultJWTSigningKey = "dev-insecure-jwt-key-change-me"
	minSigningKeyBytes   = 32
)

func (c *Config) validateProduction() error {
	if !c.IsProduction() {
		return nil
	}
	if len(c.JWTSigningKey) < minSigningKeyBytes || c.JWTSigningKey == defaultJWTSigningKey {
		return fmt.Errorf("production requires JWT_SIGNING_KEY of at least %d bytes (not the dev default)", minSigningKeyBytes)
	}
	if !c.CookieSecure {
		return fmt.Errorf("production requires COOKIE_SECURE=true")
	}
	return nil
}

// IsProduction reports whether the server runs in production mode.
func (c *Config) IsProduction() bool { return c.Env == "production" }

// RawRoot, StagingDir, etc. return canonical subpaths of the data store.
func (c *Config) RawRoot() string       { return filepath.Join(c.LabDataRoot, "raw") }
func (c *Config) ProcessedRoot() string { return filepath.Join(c.LabDataRoot, "processed") }
func (c *Config) PublishedRoot() string { return filepath.Join(c.LabDataRoot, "published") }
func (c *Config) ManifestsRoot() string { return filepath.Join(c.LabDataRoot, "manifests") }

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getint(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getint64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func getbool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func getdur(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
