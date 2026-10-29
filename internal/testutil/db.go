package testutil

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx", needed by goose
	"github.com/pressly/goose/v3"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// DB is a fresh, migrated database for one test.
type DB struct {
	Name  string
	App   *pgxpool.Pool // runtime user, as the application connects
	Owner *pgxpool.Pool // owner user, for test-only setup such as probe tables
}

var (
	templateOnce sync.Once
	templateName string
	templateErr  error
)

// NewDB clones the migrated template into a new database and drops it when the test ends.
func NewDB(t testing.TB) DB {
	t.Helper()
	ctx := context.Background()
	admin := connect(t, Env(t, "TEST_ADMIN_DATABASE_URL"))
	defer admin.Close(ctx)

	templateOnce.Do(func() { templateName, templateErr = ensureTemplate(ctx, admin, Env(t, "TEST_OWNER_DATABASE_URL")) })
	if templateErr != nil {
		t.Fatalf("prepare template database: %v", templateErr)
	}

	return newDatabase(t, admin, "TEMPLATE "+templateName)
}

// NewDBAt is a new database migrated only up to version, to test a migration against data
// written before it. The returned function applies the remaining migrations.
func NewDBAt(t testing.TB, version int64) (DB, func()) {
	t.Helper()
	ctx := context.Background()
	admin := connect(t, Env(t, "TEST_ADMIN_DATABASE_URL"))
	defer admin.Close(ctx)
	db := newDatabase(t, admin, "")
	ownerURL, err := withDatabase(Env(t, "TEST_OWNER_DATABASE_URL"), db.Name)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := sql.Open("pgx", ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	files, err := fs.Sub(platform.Migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, version); err != nil {
		t.Fatalf("migrate to %d: %v", version, err)
	}
	return db, func() {
		t.Helper()
		if _, err := provider.Up(ctx); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
}

// newDatabase creates a database (from a template when given), dropped when the test ends.
func newDatabase(t testing.TB, admin *pgx.Conn, template string) DB {
	t.Helper()
	ctx := context.Background()
	appURL, ownerURL := Env(t, "TEST_APP_DATABASE_URL"), Env(t, "TEST_OWNER_DATABASE_URL")
	name := "test_" + randomHex(t)
	exec(t, admin, fmt.Sprintf("CREATE DATABASE %s %s OWNER accessflow_owner", name, template))
	t.Cleanup(func() {
		admin := connect(t, Env(t, "TEST_ADMIN_DATABASE_URL"))
		defer admin.Close(ctx)
		exec(t, admin, fmt.Sprintf("DROP DATABASE %s WITH (FORCE)", name))
	})
	// Database-level privileges are not copied from a template; mirror deploy/postgres/init.sh.
	exec(t, admin, fmt.Sprintf("REVOKE ALL ON DATABASE %s FROM PUBLIC", name))
	exec(t, admin, fmt.Sprintf("GRANT CONNECT ON DATABASE %s TO accessflow_app", name))

	return DB{
		Name:  name,
		App:   pool(t, appURL, name),
		Owner: pool(t, ownerURL, name),
	}
}

// ensureTemplate creates the template once per migration set. Test packages run in
// parallel processes, so creation is serialized with an advisory lock, and the name
// carries a hash of the migrations so a changed migration never reuses a stale template.
func ensureTemplate(ctx context.Context, admin *pgx.Conn, ownerURL string) (string, error) {
	sum, err := migrationsHash()
	if err != nil {
		return "", err
	}
	name := "template_" + sum
	if _, err := admin.Exec(ctx, "SELECT pg_advisory_lock(hashtext('accessflow test template'))"); err != nil {
		return "", err
	}
	defer admin.Exec(ctx, "SELECT pg_advisory_unlock(hashtext('accessflow test template'))")

	var exists bool
	if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
		return "", err
	}
	if exists {
		return name, nil
	}

	// Build under a temporary name so a crash mid-migration never leaves a half-built template.
	building := name + "_building"
	for _, stmt := range []string{
		fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", building),
		fmt.Sprintf("CREATE DATABASE %s OWNER accessflow_owner", building),
	} {
		if _, err := admin.Exec(ctx, stmt); err != nil {
			return "", err
		}
	}
	buildingURL, err := withDatabase(ownerURL, building)
	if err != nil {
		return "", err
	}
	if err := platform.Migrate(ctx, buildingURL, platform.Migrations); err != nil {
		return "", err
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf("ALTER DATABASE %s RENAME TO %s", building, name)); err != nil {
		return "", err
	}
	return name, nil
}

func migrationsHash() (string, error) {
	h := sha256.New()
	err := fs.WalkDir(platform.Migrations, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(platform.Migrations, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\n%s\n", path, b)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:16], err
}

// CreateProbeTable adds a plain table the runtime user may write, standing in for
// business state in tests that run before any business tables exist.
func CreateProbeTable(t testing.TB, db DB) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		"CREATE TABLE probe (id int PRIMARY KEY)",
		"GRANT SELECT, INSERT ON probe TO accessflow_app",
	} {
		if _, err := db.Owner.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

// Count returns the number of rows in table.
func Count(t testing.TB, p *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := p.QueryRow(context.Background(), "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func withDatabase(rawURL, database string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	u.Path = "/" + database
	return u.String(), nil
}

func connect(t testing.TB, connURL string) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), connURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return conn
}

func pool(t testing.TB, connURL, database string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(connURL)
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	cfg.ConnConfig.Database = database
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("pool for %s: %v", database, err)
	}
	t.Cleanup(p.Close) // registered after the drop, so it runs first
	return p
}

func exec(t testing.TB, conn *pgx.Conn, sql string) {
	t.Helper()
	if _, err := conn.Exec(context.Background(), sql); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func randomHex(t testing.TB) string {
	t.Helper()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random name: %v", err)
	}
	return hex.EncodeToString(b)
}
