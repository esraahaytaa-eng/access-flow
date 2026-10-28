// Package platform holds the shared plumbing: configuration, the database
// pool and transaction helper, embedded migrations and HTTP helpers.
package platform

import "fmt"

// Config is read from environment variables (A3: no configuration framework).
type Config struct {
	HTTPAddr             string
	DatabaseURL          string // runtime user: no DDL, insert/select only on audit_events
	MigrationDatabaseURL string // owner user: used only to run migrations at startup
}

// LoadConfig reads the configuration through getenv (os.Getenv in production).
func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{
		HTTPAddr:             getenv("HTTP_ADDR"),
		DatabaseURL:          getenv("DATABASE_URL"),
		MigrationDatabaseURL: getenv("MIGRATION_DATABASE_URL"),
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = ":8080"
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.MigrationDatabaseURL == "" {
		return Config{}, fmt.Errorf("MIGRATION_DATABASE_URL is required")
	}
	return cfg, nil
}
