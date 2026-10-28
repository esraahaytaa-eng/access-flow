package testutil

import (
	"context"
	"fmt"
	"io/fs"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// NewMockToolsDB creates a fresh Mock Tool Service database owned by the mocktools user,
// applies migrations (passed in, so testutil does not import the mock) and drops it at the end.
func NewMockToolsDB(t testing.TB, migrations fs.FS) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	mockURL := Env(t, "TEST_MOCKTOOLS_DATABASE_URL")
	admin := connect(t, Env(t, "TEST_ADMIN_DATABASE_URL"))
	defer admin.Close(ctx)
	name := "mock_" + randomHex(t)
	exec(t, admin, fmt.Sprintf("CREATE DATABASE %s OWNER mocktools", name))
	t.Cleanup(func() {
		admin := connect(t, Env(t, "TEST_ADMIN_DATABASE_URL"))
		defer admin.Close(ctx)
		exec(t, admin, fmt.Sprintf("DROP DATABASE %s WITH (FORCE)", name))
	})
	dbURL, err := withDatabase(mockURL, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := platform.Migrate(ctx, dbURL, migrations); err != nil {
		t.Fatalf("migrate mock database: %v", err)
	}
	return pool(t, mockURL, name)
}
