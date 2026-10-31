package explorer

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// queries records the statements a pool sends, with their arguments.
type queries struct {
	mu   sync.Mutex
	seen []pgx.TraceQueryStartData
}

func (q *queries) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.seen = append(q.seen, d)
	return ctx
}

func (q *queries) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// auditRowsRead is how many audit_events rows the statement's plan really read.
func auditRowsRead(t *testing.T, pool *pgxpool.Pool, q pgx.TraceQueryStartData) float64 {
	t.Helper()
	var raw []byte
	if err := pool.QueryRow(context.Background(), "EXPLAIN (ANALYZE, FORMAT JSON) "+q.SQL, q.Args...).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var plan []struct{ Plan map[string]any }
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	var walk func(n map[string]any) float64
	walk = func(n map[string]any) (rows float64) {
		if n["Relation Name"] == "audit_events" {
			rows += n["Actual Rows"].(float64) * n["Actual Loops"].(float64)
		}
		children, _ := n["Plans"].([]any)
		for _, c := range children {
			rows += walk(c.(map[string]any))
		}
		return rows
	}
	return walk(plan[0].Plan)
}

// PERF-011: a page of the audit log reads about one page of events, whatever the log's size.
// A count in the page query (count(*) OVER ()) made PostgreSQL join and sort every matching
// event before cutting the page: 0.7–3 s and an 86 MB sort on disk at 314k events.
func TestAuditPageReadsOnlyItsPage(t *testing.T) {
	t.Parallel()
	db := testutil.NewDB(t)
	ctx := context.Background()
	if _, err := db.Owner.Exec(ctx, `INSERT INTO audit_events (action, entity_type, reason, details)
		SELECT 'QA_BULK', 'test_entity', 'row ' || n, jsonb_build_object('n', n, 'pad', repeat('x', 200)) FROM generate_series(1, 5000) n`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Owner.Exec(ctx, "ANALYZE audit_events"); err != nil {
		t.Fatal(err)
	}
	var all int
	if err := db.App.QueryRow(ctx, "SELECT count(*) FROM audit_events").Scan(&all); err != nil {
		t.Fatal(err)
	}

	rec := &queries{}
	cfg := db.App.Config().Copy()
	cfg.ConnConfig.Tracer = rec
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	svc := NewService(pool, time.UTC)
	auditor := auth.Principal{Roles: []string{"AUDITOR"}}

	for _, offset := range []int{0, 300} {
		rec.seen = nil
		events, total, err := svc.Events(ctx, auditor, EventFilter{}, offset)
		if err != nil {
			t.Fatal(err)
		}
		if total != all || len(events) != 100 {
			t.Fatalf("offset %d: %d events of %d, want 100 of %d", offset, len(events), total, all)
		}
		for _, q := range rec.seen {
			if !strings.Contains(q.SQL, "ORDER BY") {
				continue // the count reads the index, not the events' details
			}
			if read := auditRowsRead(t, db.App, q); read > float64(offset+100) {
				t.Errorf("offset %d: the page query read %v audit events, want at most %d", offset, read, offset+100)
			}
		}
	}

	// A text search still counts every match and pages through them.
	events, total, err := svc.Events(ctx, auditor, EventFilter{Text: "row 12"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 111 || len(events) != 100 { // "row 12", "row 120"–"row 129", "row 1200"–"row 1299"
		t.Fatalf("text search: %d events of %d, want 100 of 111", len(events), total)
	}
}
