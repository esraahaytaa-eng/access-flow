package platform_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// These tests check the users created by deploy/postgres/init.sh on the test server.
func TestDatabaseUsersAreRestrictedToTheirOwnDatabase(t *testing.T) {
	cases := []struct {
		name, urlEnv, database string
	}{
		{"runtime user cannot open mocktools", "TEST_APP_DATABASE_URL", "mocktools"},
		{"owner cannot open mocktools", "TEST_OWNER_DATABASE_URL", "mocktools"},
		{"mocktools user cannot open accessflow", "TEST_MOCKTOOLS_DATABASE_URL", "accessflow"},
		{"runtime user cannot open the maintenance database", "TEST_APP_DATABASE_URL", "postgres"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testutil.ConnConfig(t, testutil.Env(t, tc.urlEnv), tc.database)
			conn, err := pgx.ConnectConfig(context.Background(), cfg)
			if err == nil {
				conn.Close(context.Background())
				t.Fatalf("connected to %s; want permission denied", tc.database)
			}
			testutil.RequireSQLState(t, err, "42501") // insufficient_privilege
		})
	}
}

func TestDatabaseUsersCanOpenTheirOwnDatabase(t *testing.T) {
	for _, urlEnv := range []string{"TEST_APP_DATABASE_URL", "TEST_OWNER_DATABASE_URL", "TEST_MOCKTOOLS_DATABASE_URL"} {
		t.Run(urlEnv, func(t *testing.T) {
			conn, err := pgx.Connect(context.Background(), testutil.Env(t, urlEnv))
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			conn.Close(context.Background())
		})
	}
}

func TestRuntimeUserCannotCreateTables(t *testing.T) {
	db := testutil.NewDB(t)
	_, err := db.App.Exec(context.Background(), "CREATE TABLE sneaky (id int)")
	testutil.RequireSQLState(t, err, "42501")
}
