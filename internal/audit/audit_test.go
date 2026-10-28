package audit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func TestRecordStoresTheEventAtTheTransactionTime(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	actor := testutil.CreateEmployee(t, db, testutil.EmployeeSpec{}).ID
	subject := testutil.CreateEmployee(t, db, testutil.EmployeeSpec{}).ID
	entity := uuid.New()

	var txNow time.Time
	err := platform.InTx(ctx, db.App, func(tx pgx.Tx, now time.Time) error {
		txNow = now
		return audit.Record(ctx, tx, audit.Event{
			ActorID:   &actor,
			Action:    "TEST_ACTION",
			Entity:    "test_entity",
			EntityID:  &entity,
			SubjectID: &subject,
			Reason:    "because",
			Details:   map[string]any{"tool": "jira"},
		})
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	var (
		at                           time.Time
		gotActor, gotEntity, gotSubj uuid.UUID
		action, entityType, reason   string
		tool                         string
	)
	err = db.App.QueryRow(ctx, `SELECT occurred_at, actor_id, action, entity_type, entity_id,
		subject_employee_id, reason, details->>'tool' FROM audit_events`).
		Scan(&at, &gotActor, &action, &entityType, &gotEntity, &gotSubj, &reason, &tool)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !at.Equal(txNow) {
		t.Errorf("occurred_at = %v; want transaction time %v", at, txNow)
	}
	if gotActor != actor || gotEntity != entity || gotSubj != subject {
		t.Errorf("ids = %v %v %v; want %v %v %v", gotActor, gotEntity, gotSubj, actor, entity, subject)
	}
	if action != "TEST_ACTION" || entityType != "test_entity" || reason != "because" || tool != "jira" {
		t.Errorf("got %q %q %q %q", action, entityType, reason, tool)
	}
}

func TestRecordWithoutActorIsASystemEvent(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)

	err := platform.InTx(ctx, db.App, func(tx pgx.Tx, _ time.Time) error {
		return audit.Record(ctx, tx, audit.Event{Action: "SWEEP_RAN", Entity: "system"})
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	var actorIsNull, reasonIsNull bool
	var details string
	err = db.App.QueryRow(ctx, `SELECT actor_id IS NULL, reason IS NULL, details::text FROM audit_events`).
		Scan(&actorIsNull, &reasonIsNull, &details)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !actorIsNull || !reasonIsNull || details != "{}" {
		t.Fatalf("actor null=%v reason null=%v details=%s; want true true {}", actorIsNull, reasonIsNull, details)
	}
}

func TestRecordRejectsAnEventWithoutActionOrEntity(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	for _, e := range []audit.Event{{Entity: "x"}, {Action: "X"}} {
		err := platform.InTx(ctx, db.App, func(tx pgx.Tx, _ time.Time) error {
			return audit.Record(ctx, tx, e)
		})
		testutil.RequireSQLState(t, err, "23514") // check_violation
	}
}

func TestAuditEventRollsBackWithTheStateChange(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	testutil.CreateProbeTable(t, db)
	boom := errors.New("boom")

	err := platform.InTx(ctx, db.App, func(tx pgx.Tx, _ time.Time) error {
		if _, err := tx.Exec(ctx, "INSERT INTO probe (id) VALUES (1)"); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Action: "PROBE_CHANGED", Entity: "probe"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("InTx error = %v; want %v", err, boom)
	}
	if n := testutil.Count(t, db.App, "probe"); n != 0 {
		t.Errorf("probe rows = %d; want 0", n)
	}
	if n := testutil.Count(t, db.App, "audit_events"); n != 0 {
		t.Errorf("audit events = %d; want 0", n)
	}
}

func TestAuditTimeCannotBeBackdated(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)

	_, err := db.App.Exec(ctx, `INSERT INTO audit_events (occurred_at, action, entity_type)
		VALUES ('2020-01-01T00:00:00Z', 'FAKE', 'x')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	var at time.Time
	if err := db.App.QueryRow(ctx, "SELECT occurred_at FROM audit_events").Scan(&at); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if time.Since(at) > time.Minute {
		t.Fatalf("occurred_at = %v; want the real insert time", at)
	}
}

func TestRuntimeUserCannotChangeAuditHistory(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	insertEvent(t, db)

	for _, stmt := range []string{
		"UPDATE audit_events SET reason = 'rewritten'",
		"DELETE FROM audit_events",
		"TRUNCATE audit_events",
		"DROP TABLE audit_events",
		"ALTER TABLE audit_events DISABLE TRIGGER USER",
	} {
		t.Run(stmt, func(t *testing.T) {
			_, err := db.App.Exec(ctx, stmt)
			testutil.RequireSQLState(t, err, "42501") // insufficient_privilege / must be owner
		})
	}
	if n := testutil.Count(t, db.App, "audit_events"); n != 1 {
		t.Fatalf("audit events = %d; want 1", n)
	}
}

func TestTriggerBlocksAuditChangesEvenForTheOwner(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	insertEvent(t, db)

	for _, stmt := range []string{
		"UPDATE audit_events SET reason = 'rewritten'",
		"DELETE FROM audit_events",
		"TRUNCATE audit_events",
	} {
		t.Run(stmt, func(t *testing.T) {
			_, err := db.Owner.Exec(ctx, stmt)
			testutil.RequireSQLState(t, err, "P0001") // raise_exception from the append-only trigger
		})
	}
	if n := testutil.Count(t, db.App, "audit_events"); n != 1 {
		t.Fatalf("audit events = %d; want 1", n)
	}
}

func insertEvent(t *testing.T, db testutil.DB) {
	t.Helper()
	ctx := context.Background()
	err := platform.InTx(ctx, db.App, func(tx pgx.Tx, _ time.Time) error {
		return audit.Record(ctx, tx, audit.Event{Action: "TEST_ACTION", Entity: "test_entity"})
	})
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
}
