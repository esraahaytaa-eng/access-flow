// Package platform holds the shared plumbing: configuration, the database
// pool and transaction helper, embedded migrations and HTTP helpers.
package platform

import (
	"errors"
	"fmt"
	"net/url"
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
	Demo                 bool   // ACCESSFLOW_DEMO: local demo accounts and seed (A4, D12); fails closed outside the local setup
	DemoPasswords        string // DEMO_PASSWORDS: "account=password,..." for the documented demo accounts
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
		DemoPasswords:        getenv("DEMO_PASSWORDS"),
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
	if v := getenv("ACCESSFLOW_DEMO"); v != "" {
		demo, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("ACCESSFLOW_DEMO must be true or false: %w", err)
		}
		cfg.Demo = demo
	}
	if cfg.Demo {
		if err := checkLocal(cfg, getenv("ACCESSFLOW_ENV")); err != nil {
			return Config{}, fmt.Errorf("demo mode refused: %w", err)
		}
	}
	return cfg, nil
}

// checkLocal lets demo mode (known passwords) run only in the local Compose setup (A4 note
// 2026-10-29): every check must pass, so a real deployment fails closed.
func checkLocal(cfg Config, env string) error {
	switch {
	case env != "local":
		return errors.New("ACCESSFLOW_ENV must be local")
	case cfg.SecureCookies:
		return errors.New("COOKIE_SECURE is set")
	case cfg.JWTSecret != "":
		return errors.New("JWT_SECRET is set")
	case cfg.DemoPasswords == "":
		return errors.New("DEMO_PASSWORDS is required")
	}
	for _, u := range []string{cfg.DatabaseURL, cfg.MigrationDatabaseURL} {
		// No query: pgx would let a host parameter there override the URL's host.
		parsed, err := url.Parse(u)
		if err != nil || parsed.Hostname() != "postgres" || parsed.RawQuery != "" {
			return errors.New("the databases must be the Compose postgres service")
		}
	}
	return nil
}
