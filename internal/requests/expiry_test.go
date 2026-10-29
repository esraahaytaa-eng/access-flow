package requests

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/sweeps"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// sweep runs one round of the scheduled jobs.
func (w *world) sweep() {
	w.t.Helper()
	if err := sweeps.New(w.db.App, w.svc.loc, TerminateDue).RunOnce(context.Background()); err != nil {
		w.t.Fatal(err)
	}
}

// expiresOn sets the grant's expiry date (D12: dates move, the clock does not).
func (w *world) expiresOn(g map[string]any, date any) {
	w.t.Helper()
	w.exec("UPDATE grants SET expires_on = $2 WHERE id = $1", g["id"], date)
}

// removals counts the grant's REVOKE operations.
func (w *world) removals(g map[string]any) int {
	w.t.Helper()
	return w.count("SELECT count(*) FROM operations WHERE grant_id = $1 AND type = 'REVOKE'", g["id"])
}

// fulfilled approves level for dev and lets f.fixer work its Manual or Vendor task.
func (w *world) fulfilled(f fulfillers, level uuid.UUID) map[string]any {
	w.t.Helper()
	w.approved(level)
	task := w.mustAct(f.fixer, w.mustTask(f.fixer, level, "GRANT"), "claim", nil)
	if level == f.vendor {
		task = w.mustAct(f.fixer, task, "ticket", map[string]any{"ticketRef": "VND-1"})
	}
	w.mustAct(f.fixer, task, "confirm", nil)
	return w.myGrant(w.dev.c, level)
}

func TestExpirySweepRemovesTemporaryAccess(t *testing.T) {
	t.Run("API", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		w.approved(w.jiraViewer)
		tl.drain(t)
		g := w.myGrant(w.dev.c, w.jiraViewer)
		w.expiresOn(g, in(-1)) // valid through yesterday: due since 00:00 today
		w.sweep()
		requireGrant(t, w.myGrant(w.dev.c, w.jiraViewer), "REVOCATION_REQUESTED", "REVOCATION_REQUESTED")
		if n := w.count(`SELECT count(*) FROM operations o JOIN audit_events a ON a.action = 'GRANT_REVOCATION_REQUESTED' AND a.entity_id = o.grant_id
			WHERE o.grant_id = $1 AND o.type = 'REVOKE' AND o.reason = 'EXPIRED' AND o.priority = 1
			  AND a.actor_id IS NULL AND a.details->>'by' = 'EXPIRED' AND a.reason = 'expired'`, g["id"]); n != 1 {
			t.Fatalf("expiry removal = %d", n)
		}
		tl.drain(t)
		g = w.myGrant(w.dev.c, w.jiraViewer)
		requireGrant(t, g, "REVOKED", "REVOKED / VERIFIED")
		if tl.held(t, g, w.dev.Email) != 0 || w.count("SELECT count(*) FROM notifications") != 0 {
			t.Fatal("expired access still held, or someone was notified (D6: no reminders)")
		}
	})
	for _, method := range []string{"MANUAL", "VENDOR"} {
		t.Run(method, func(t *testing.T) {
			w := newWorld(t)
			f := w.fulfillers()
			level := map[string]uuid.UUID{"MANUAL": f.manual, "VENDOR": f.vendor}[method]
			g := w.fulfilled(f, level)
			w.expiresOn(g, in(-1))
			w.sweep()
			requireGrant(t, w.myGrant(w.dev.c, level), "REVOCATION_REQUESTED", "REVOCATION_REQUESTED")
			task := w.mustAct(f.fixer, w.mustTask(f.fixer, level, "REVOKE"), "claim", nil)
			if method == "VENDOR" {
				task = w.mustAct(f.fixer, task, "ticket", map[string]any{"ticketRef": "VND-2"})
			}
			w.mustAct(f.fixer, task, "confirm", nil)
			requireGrant(t, w.myGrant(w.dev.c, level), "REVOKED", "REVOKED / SELF_REPORTED")
		})
	}
}

func TestExpirySweepIsIdempotent(t *testing.T) {
	t.Run("one after the other", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		w.approved(w.jiraViewer)
		tl.drain(t)
		g := w.myGrant(w.dev.c, w.jiraViewer)
		w.expiresOn(g, in(-3)) // downtime: the sweep catches up
		w.sweep()
		w.sweep()
		if w.removals(g) != 1 || w.audits("GRANT_REVOCATION_REQUESTED") != 1 {
			t.Fatalf("removals %d, events %d", w.removals(g), w.audits("GRANT_REVOCATION_REQUESTED"))
		}
	})
	t.Run("concurrently", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		w.approved(w.jiraViewer)
		tl.drain(t)
		g := w.myGrant(w.dev.c, w.jiraViewer)
		w.expiresOn(g, in(-1))
		var wg sync.WaitGroup
		errs := make(chan error, 4)
		for range 4 {
			wg.Go(func() { errs <- sweeps.New(w.db.App, w.svc.loc, TerminateDue).RunOnce(context.Background()) })
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if w.removals(g) != 1 || w.audits("GRANT_REVOCATION_REQUESTED") != 1 {
			t.Fatalf("removals %d, events %d", w.removals(g), w.audits("GRANT_REVOCATION_REQUESTED"))
		}
	})
}

func TestExpirySweepSkipsWhileAnotherHoldsTheLock(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	tl.drain(t)
	g := w.myGrant(w.dev.c, w.jiraViewer)
	w.expiresOn(g, in(-1))
	ctx := context.Background()
	tx, err := w.db.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", access.ExpiryLockKey); err != nil {
		t.Fatal(err)
	}
	w.sweep()
	if w.removals(g) != 0 {
		t.Fatal("a second round ran while another held the lock")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	w.sweep()
	if w.removals(g) != 1 {
		t.Fatal("no removal after the lock was released")
	}
}

func TestExpiryOfAGrantNeverSent(t *testing.T) {
	t.Run("never sent: cancelled", func(t *testing.T) {
		w := newWorld(t)
		w.approved(w.jiraViewer) // no runner: never sent
		g := w.myGrant(w.dev.c, w.jiraViewer)
		w.expiresOn(g, in(-1))
		w.sweep()
		g = w.myGrant(w.dev.c, w.jiraViewer)
		if g["status"] != "CANCELLED" || g["closedReason"] != "expired" || w.removals(g) != 0 {
			t.Fatalf("grant = %v", g)
		}
	})
	t.Run("failed: removed", func(t *testing.T) {
		w := newWorld(t)
		tl := w.startTools()
		w.failed(tl) // the tool may have applied it: remove it
		g := w.myGrant(w.dev.c, w.jiraViewer)
		w.expiresOn(g, in(-1))
		w.sweep()
		requireGrant(t, w.myGrant(w.dev.c, w.jiraViewer), "REVOCATION_REQUESTED", "REVOCATION_REQUESTED")
		if w.removals(g) != 1 || w.count("SELECT count(*) FROM operations WHERE grant_id = $1 AND type = 'GRANT' AND status = 'CANCELLED'", g["id"]) != 1 {
			t.Fatal("the failed GRANT was not cancelled with a removal queued")
		}
	})
}

func TestUntilRevokedAndFutureGrantsAreKept(t *testing.T) {
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	tl.drain(t)
	g := w.myGrant(w.dev.c, w.jiraViewer)
	for _, date := range []any{nil, in(0), in(1)} { // until revoked; valid through today; tomorrow
		w.expiresOn(g, date)
		w.sweep()
		if got := w.myGrant(w.dev.c, w.jiraViewer); got["status"] != "ACTIVE" || w.removals(g) != 0 {
			t.Fatalf("expires %v: grant %v, removals %d", date, got["status"], w.removals(g))
		}
	}
}

// The sweep listed the grant as due, then waited for the employee while an extension's
// approval moved its date: after the locks it sees the new date and keeps the access.
func TestExpiryAndExtensionSerialize(t *testing.T) {
	for _, extended := range []any{in(30), nil} { // a later date, or Until Revoked
		t.Run(fmt.Sprint(extended), func(t *testing.T) {
			w := newWorld(t)
			tl := w.startTools()
			w.approved(w.jiraViewer)
			tl.drain(t)
			g := w.myGrant(w.dev.c, w.jiraViewer)
			w.expiresOn(g, in(-1))
			err := testutil.WhileLocked(t, w.db.App, func(ctx context.Context, tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", w.dev.ID); err != nil {
					return err
				}
				_, err := tx.Exec(ctx, "UPDATE grants SET expires_on = $2 WHERE id = $1", g["id"], extended)
				return err
			}, func() error {
				return sweeps.New(w.db.App, w.svc.loc, TerminateDue).RunOnce(context.Background())
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := w.myGrant(w.dev.c, w.jiraViewer); got["status"] != "ACTIVE" || got["expiresOn"] != extended || w.removals(g) != 0 {
				t.Fatalf("grant %v, removals %d", got, w.removals(g))
			}
		})
	}
}
