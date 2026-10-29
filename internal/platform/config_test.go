package platform_test

import (
	"strings"
	"testing"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

func TestLoadConfig(t *testing.T) {
	full := map[string]string{
		"DATABASE_URL":           "postgres://app@db/accessflow",
		"MIGRATION_DATABASE_URL": "postgres://owner@db/accessflow",
	}
	defaults := platform.Config{HTTPAddr: ":8080", DatabaseURL: full["DATABASE_URL"], MigrationDatabaseURL: full["MIGRATION_DATABASE_URL"],
		BootstrapAdminEmail: "admin@accessflow.local", BootstrapFile: "/var/lib/accessflow/bootstrap-admin.txt",
		CompanyTimezone: "Europe/Istanbul", MockToolsURL: "http://mocktools:8081"}
	changed := func(f func(*platform.Config)) platform.Config { c := defaults; f(&c); return c }
	demo := with(with(with(with(full, "ACCESSFLOW_DEMO", "true"), "ACCESSFLOW_ENV", "local"),
		"DATABASE_URL", "postgres://accessflow_app:x@postgres:5432/accessflow"), "MIGRATION_DATABASE_URL", "postgres://accessflow_owner:x@postgres:5432/accessflow")
	demo = with(demo, "DEMO_PASSWORDS", "admin=Demo-Admin-Pass-1")
	cases := []struct {
		name    string
		env     map[string]string
		want    platform.Config
		wantErr bool
	}{
		{name: "defaults", env: full, want: defaults},
		{name: "HTTP address from the environment", env: with(full, "HTTP_ADDR", ":9000"),
			want: changed(func(c *platform.Config) { c.HTTPAddr = ":9000" })},
		{name: "JWT secret from the environment", env: with(full, "JWT_SECRET", "s3cret"),
			want: changed(func(c *platform.Config) { c.JWTSecret = "s3cret" })},
		{name: "secure cookies", env: with(full, "COOKIE_SECURE", "true"),
			want: changed(func(c *platform.Config) { c.SecureCookies = true })},
		{name: "COOKIE_SECURE must be a boolean", env: with(full, "COOKIE_SECURE", "yes please"), wantErr: true},
		{name: "bootstrap admin and file from the environment",
			env: with(with(full, "BOOTSTRAP_ADMIN_EMAIL", "root@example.com"), "BOOTSTRAP_FILE", "/data/admin.txt"),
			want: changed(func(c *platform.Config) {
				c.BootstrapAdminEmail, c.BootstrapFile = "root@example.com", "/data/admin.txt"
			})},
		{name: "company timezone from the environment", env: with(full, "COMPANY_TIMEZONE", "America/New_York"),
			want: changed(func(c *platform.Config) { c.CompanyTimezone = "America/New_York" })},
		{name: "Mock Tool Service URL from the environment", env: with(full, "MOCK_TOOLS_URL", "http://localhost:9999"),
			want: changed(func(c *platform.Config) { c.MockToolsURL = "http://localhost:9999" })},
		{name: "unknown company timezone", env: with(full, "COMPANY_TIMEZONE", "Mars/Olympus"), wantErr: true},
		{name: "runtime database URL is required", env: with(full, "DATABASE_URL", ""), wantErr: true},
		{name: "migration database URL is required", env: with(full, "MIGRATION_DATABASE_URL", ""), wantErr: true},
		{name: "demo mode in the local Compose setup", env: demo,
			want: changed(func(c *platform.Config) {
				c.DatabaseURL, c.MigrationDatabaseURL = demo["DATABASE_URL"], demo["MIGRATION_DATABASE_URL"]
				c.Demo, c.DemoPasswords = true, demo["DEMO_PASSWORDS"]
			})},
		{name: "ACCESSFLOW_DEMO must be a boolean", env: with(demo, "ACCESSFLOW_DEMO", "sure"), wantErr: true},
		// Demo mode fails closed: anything that looks like a real deployment refuses to start.
		{name: "demo without ACCESSFLOW_ENV", env: with(demo, "ACCESSFLOW_ENV", ""), wantErr: true},
		{name: "demo outside the local environment", env: with(demo, "ACCESSFLOW_ENV", "production"), wantErr: true},
		{name: "demo with secure cookies", env: with(demo, "COOKIE_SECURE", "true"), wantErr: true},
		{name: "demo with a configured JWT secret", env: with(demo, "JWT_SECRET", strings.Repeat("s", 32)), wantErr: true},
		{name: "demo on another runtime database", env: with(demo, "DATABASE_URL", "postgres://app:x@db.prod.internal:5432/accessflow"), wantErr: true},
		{name: "demo on another migration database", env: with(demo, "MIGRATION_DATABASE_URL", "postgres://owner:x@10.0.0.5/accessflow"), wantErr: true},
		{name: "demo whose URL overrides the host in its query", env: with(demo, "DATABASE_URL", "postgres://app:x@postgres:5432/accessflow?host=db.prod.internal"), wantErr: true},
		{name: "demo without demo passwords", env: with(demo, "DEMO_PASSWORDS", ""), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := platform.LoadConfig(func(k string) string { return tc.env[k] })
			if tc.wantErr {
				if err == nil {
					t.Fatalf("got %+v; want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %+v; want %+v", got, tc.want)
			}
		})
	}
}

func with(env map[string]string, key, value string) map[string]string {
	out := map[string]string{key: value}
	for k, v := range env {
		if k != key {
			out[k] = v
		}
	}
	return out
}
