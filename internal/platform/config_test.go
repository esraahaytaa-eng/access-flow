package platform_test

import (
	"testing"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

func TestLoadConfig(t *testing.T) {
	full := map[string]string{
		"DATABASE_URL":           "postgres://app@db/accessflow",
		"MIGRATION_DATABASE_URL": "postgres://owner@db/accessflow",
	}
	defaults := platform.Config{HTTPAddr: ":8080", DatabaseURL: full["DATABASE_URL"], MigrationDatabaseURL: full["MIGRATION_DATABASE_URL"],
		BootstrapAdminEmail: "admin@accessflow.local", BootstrapFile: "/var/lib/accessflow/bootstrap-admin.txt"}
	changed := func(f func(*platform.Config)) platform.Config { c := defaults; f(&c); return c }
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
		{name: "runtime database URL is required", env: with(full, "DATABASE_URL", ""), wantErr: true},
		{name: "migration database URL is required", env: with(full, "MIGRATION_DATABASE_URL", ""), wantErr: true},
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
