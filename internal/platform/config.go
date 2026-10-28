// Package platform holds the shared plumbing: configuration, the database
// pool and transaction helper, embedded migrations and HTTP helpers.
package platform

import (
	"fmt"
	"strconv"
	"time"
	_ "time/tzdata" // the distroless image has no zoneinfo
)

// Config is read from environment variables (A3: no configuration framework).
type Config struct {
	HTTPAddr             string
	DatabaseURL          string // runtime user: no DDL, insert/select only on audit_events
	MigrationDatabaseURL string // owner user: used only to run migrations at startup
	JWTSecret            string // optional; generated and stored on first start when empty (A4)
	SecureCookies        bool   // COOKIE_SECURE: set when served over HTTPS (A4)
	BootstrapAdminEmail  string // the initial Super Admin, created once (A4)
	BootstrapFile        string // where its one-time password is written
	SeedPassword         string // SEED_PASSWORD: shared local-only password of seeded users (A4)
	CompanyTimezone      string // COMPANY_TIMEZONE: the zone that dates such as expiry are in (A6)
	MockToolsURL         string // MOCK_TOOLS_URL: base URL of the Mock Tool Service (A6)
}

// LoadConfig reads the configuration through getenv (os.Getenv in production).
func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{
		HTTPAddr:             getenv("HTTP_ADDR"),
		DatabaseURL:          getenv("DATABASE_URL"),
		MigrationDatabaseURL: getenv("MIGRATION_DATABASE_URL"),
		JWTSecret:            getenv("JWT_SECRET"),
		BootstrapAdminEmail:  getenv("BOOTSTRAP_ADMIN_EMAIL"),
		BootstrapFile:        getenv("BOOTSTRAP_FILE"),
		SeedPassword:         getenv("SEED_PASSWORD"),
		CompanyTimezone:      getenv("COMPANY_TIMEZONE"),
		MockToolsURL:         getenv("MOCK_TOOLS_URL"),
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = ":8080"
	}
	if cfg.BootstrapAdminEmail == "" {
		cfg.BootstrapAdminEmail = "admin@accessflow.local"
	}
	if cfg.BootstrapFile == "" {
		cfg.BootstrapFile = "/var/lib/accessflow/bootstrap-admin.txt"
	}
	if cfg.CompanyTimezone == "" {
		cfg.CompanyTimezone = "Europe/Istanbul"
	}
	if cfg.MockToolsURL == "" {
		cfg.MockToolsURL = "http://mocktools:8081"
	}
	if _, err := time.LoadLocation(cfg.CompanyTimezone); err != nil {
		return Config{}, fmt.Errorf("COMPANY_TIMEZONE: %w", err)
	}
	if v := getenv("COOKIE_SECURE"); v != "" {
		secure, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("COOKIE_SECURE must be true or false: %w", err)
		}
		cfg.SecureCookies = secure
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.MigrationDatabaseURL == "" {
		return Config{}, fmt.Errorf("MIGRATION_DATABASE_URL is required")
	}
	return cfg, nil
}
