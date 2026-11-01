// Package platform holds the shared plumbing: configuration, the database
// pool and transaction helper, embedded migrations and HTTP helpers.
package platform

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // the distroless image has no zoneinfo

	"github.com/google/uuid"
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
	MockEmailKeyFile     string // MOCK_EMAIL_KEY_FILE: key encrypting activation links in the Mock Email Outbox (M18)
	LoginRateLimit       int    // LOGIN_RATE_LIMIT: sign-in and activation attempts per client address per minute; 0 = the default (M18)
	// Optional GitHub sandbox adapter (A1 note 2026-11-01): all three or none. Each
	// employee's GitHub username is entered on the request, not configured here.
	GitHubToken      string // GITHUB_TOKEN: fine-grained token limited to the sandbox repository; never logged
	GitHubRepository string // GITHUB_REPOSITORY: owner/name of the sandbox repository
	GitHubToolID     string // GITHUB_TOOL_ID: the AccessFlow tool whose operations go to GitHub
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
		MockEmailKeyFile:     getenv("MOCK_EMAIL_KEY_FILE"),
		GitHubToken:          getenv("GITHUB_TOKEN"),
		GitHubRepository:     getenv("GITHUB_REPOSITORY"),
		GitHubToolID:         getenv("GITHUB_TOOL_ID"),
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
	if cfg.MockEmailKeyFile == "" {
		cfg.MockEmailKeyFile = "/var/lib/accessflow/mock-email.key"
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
	if v := getenv("LOGIN_RATE_LIMIT"); v != "" {
		limit, err := strconv.Atoi(v)
		if err != nil || limit < 1 {
			return Config{}, fmt.Errorf("LOGIN_RATE_LIMIT must be a positive whole number, got %q", v)
		}
		cfg.LoginRateLimit = limit
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
	if err := checkGitHub(cfg); err != nil {
		return Config{}, err
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

// The AccessFlow repository itself is never a sandbox: the adapter refuses to start on it.
const accessFlowRepository = "esraahaytaa-eng/access-flow"

var githubRepository = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)

// checkGitHub accepts the GitHub settings only complete and pointing at a sandbox.
func checkGitHub(cfg Config) error {
	set := 0
	for _, v := range []string{cfg.GitHubToken, cfg.GitHubRepository, cfg.GitHubToolID} {
		if v != "" {
			set++
		}
	}
	switch {
	case set == 0:
		return nil
	case set < 3:
		return errors.New("GITHUB_TOKEN, GITHUB_REPOSITORY and GITHUB_TOOL_ID are set together or not at all")
	case !githubRepository.MatchString(cfg.GitHubRepository):
		return errors.New("GITHUB_REPOSITORY must be owner/name")
	case strings.EqualFold(cfg.GitHubRepository, accessFlowRepository):
		return errors.New("GITHUB_REPOSITORY must be a sandbox, not the AccessFlow repository")
	}
	if _, err := uuid.Parse(cfg.GitHubToolID); err != nil {
		return fmt.Errorf("GITHUB_TOOL_ID must be a tool ID: %w", err)
	}
	return nil
}
