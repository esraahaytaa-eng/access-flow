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
	cases := []struct {
		name    string
		env     map[string]string
		want    platform.Config
		wantErr bool
	}{
		{
			name: "defaults the HTTP address",
			env:  full,
			want: platform.Config{HTTPAddr: ":8080", DatabaseURL: full["DATABASE_URL"], MigrationDatabaseURL: full["MIGRATION_DATABASE_URL"]},
		},
		{
			name: "HTTP address from the environment",
			env:  with(full, "HTTP_ADDR", ":9000"),
			want: platform.Config{HTTPAddr: ":9000", DatabaseURL: full["DATABASE_URL"], MigrationDatabaseURL: full["MIGRATION_DATABASE_URL"]},
		},
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
