package notify_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/notify"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func TestToRoleReachesOnlyActiveHolders(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	a := testutil.CreateEmployee(t, db, testutil.EmployeeSpec{Roles: []string{"INFOSEC"}})
	b := testutil.CreateEmployee(t, db, testutil.EmployeeSpec{Roles: []string{"INFOSEC", "AUDITOR"}})
	suspended := testutil.CreateEmployee(t, db, testutil.EmployeeSpec{Status: "SUSPENDED", Roles: []string{"INFOSEC"}})
	hr := testutil.CreateEmployee(t, db, testutil.EmployeeSpec{Roles: []string{"HR_MANAGER"}})
	entity := uuid.New()

	err := pgx.BeginFunc(ctx, db.App, func(tx pgx.Tx) error {
		return notify.ToRole(ctx, tx, "INFOSEC", "APPROVAL_POLICY_CHANGED", "access_level", entity, map[string]any{"level": "Viewer"})
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		who  uuid.UUID
		want int
	}{{a.ID, 1}, {b.ID, 1}, {suspended.ID, 0}, {hr.ID, 0}} {
		list, err := notify.List(ctx, db.App, tc.who)
		if err != nil || len(list) != tc.want {
			t.Fatalf("notifications for %s = %d (%v); want %d", tc.who, len(list), err, tc.want)
		}
	}
	list, _ := notify.List(ctx, db.App, a.ID)
	n := list[0]
	if n.Kind != "APPROVAL_POLICY_CHANGED" || n.Entity != "access_level" || *n.EntityID != entity || n.Details["level"] != "Viewer" || n.ReadAt != nil {
		t.Fatalf("notification = %+v", n)
	}
}

func TestMarkReadIsOwnOnlyAndRepeatable(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	me := testutil.CreateEmployee(t, db, testutil.EmployeeSpec{Roles: []string{"INFOSEC"}})
	other := testutil.CreateEmployee(t, db, testutil.EmployeeSpec{})
	pgx.BeginFunc(ctx, db.App, func(tx pgx.Tx) error {
		return notify.ToRole(ctx, tx, "INFOSEC", "K", "e", uuid.New(), nil)
	})
	list, _ := notify.List(ctx, db.App, me.ID)
	id := list[0].ID

	if err := notify.MarkRead(ctx, db.App, other.ID, id); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("someone else's notification: %v; want not found", err)
	}
	for range 2 {
		if err := notify.MarkRead(ctx, db.App, me.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	var first, now string
	db.App.QueryRow(ctx, "SELECT read_at::text FROM notifications WHERE id = $1", id).Scan(&first)
	notify.MarkRead(ctx, db.App, me.ID, id)
	db.App.QueryRow(ctx, "SELECT read_at::text FROM notifications WHERE id = $1", id).Scan(&now)
	if first == "" || first != now {
		t.Fatalf("read_at %q then %q; want set once", first, now)
	}
}

func TestListIsNewestFirstAndCapped(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDB(t)
	me := testutil.CreateEmployee(t, db, testutil.EmployeeSpec{Roles: []string{"INFOSEC"}})
	for i := range 105 {
		pgx.BeginFunc(ctx, db.App, func(tx pgx.Tx) error {
			return notify.ToRole(ctx, tx, "INFOSEC", "K", "e", uuid.New(), map[string]any{"i": i})
		})
	}
	list, _ := notify.List(ctx, db.App, me.ID)
	if len(list) != 100 || list[0].Details["i"] != 104.0 {
		t.Fatalf("len %d, first %v; want 100 newest first", len(list), list[0].Details["i"])
	}
}
