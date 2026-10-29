package requests

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/people"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func nominate(by person, nominee uuid.UUID) testutil.Response {
	return by.c.Do(http.MethodPost, "/api/v1/super-admin-nominations", map[string]any{"nomineeId": nominee})
}

func decideNomination(by person, n map[string]any, action string) testutil.Response {
	return by.c.Do(http.MethodPost, "/api/v1/super-admin-nominations/"+n["id"].(string)+"/"+action,
		map[string]any{"version": n["version"], "reason": "not needed"})
}

func (w *world) mustNominate(by person, nominee uuid.UUID) map[string]any {
	w.t.Helper()
	r := nominate(by, nominee)
	testutil.RequireStatus(w.t, r, http.StatusCreated, "")
	return r.Body
}

func (w *world) superAdmins() int {
	return w.count(`SELECT count(*) FROM role_assignments r JOIN employees e ON e.id = r.employee_id
		WHERE r.role = 'SUPER_ADMIN' AND r.valid_to IS NULL AND e.status = 'ACTIVE'`)
}

func (w *world) health(p person) map[string]any {
	w.t.Helper()
	r := w.get(p.c, "/api/v1/admin/health")
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	return r.Body
}

func TestSuperAdminPromotionIsTwoPerson(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if h := w.health(w.admin); h["activeSuperAdmins"] != float64(1) || h["recoveryRequired"] != false {
		t.Fatalf("health = %v", h)
	}
	testutil.RequireStatus(t, nominate(w.mgr, w.dev.ID), http.StatusForbidden, "forbidden")
	testutil.RequireStatus(t, nominate(w.admin, w.admin.ID), http.StatusForbidden, "self_change_forbidden")
	testutil.RequireStatus(t, nominate(w.admin, uuid.New()), http.StatusNotFound, "not_found")

	n := w.mustNominate(w.admin, w.dev.ID)
	if n["status"] != "PENDING" || n["recovery"] != false || n["nominee"].(map[string]any)["id"] != w.dev.ID.String() || n["expiresAt"] == nil {
		t.Fatalf("nomination = %v", n)
	}
	testutil.RequireStatus(t, nominate(w.admin, w.dev.ID), http.StatusConflict, "nomination_pending")
	if w.count("SELECT count(*) FROM notifications WHERE kind = 'SUPER_ADMIN_NOMINATED' AND recipient_id = $1", w.is1.ID) != 1 {
		t.Fatal("InfoSec, the authorizer while one Super Admin exists, is not notified")
	}

	// The only Super Admin proposed: a different InfoSec member authorizes; never the nominee.
	testutil.RequireStatus(t, decideNomination(w.dev, n, "authorize"), http.StatusForbidden, "separation_of_duties")
	testutil.RequireStatus(t, decideNomination(w.admin, n, "authorize"), http.StatusForbidden, "separation_of_duties")
	testutil.RequireStatus(t, decideNomination(w.mgr, n, "authorize"), http.StatusForbidden, "forbidden")
	r := decideNomination(w.is1, n, "authorize")
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Body["status"] != "AUTHORIZED" || w.superAdmins() != 2 {
		t.Fatalf("authorized = %v, Super Admins %d", r.Body, w.superAdmins())
	}
	if w.count(`SELECT count(*) FROM role_assignments WHERE employee_id = $1 AND role = 'SUPER_ADMIN' AND granted_by = $2`, w.dev.ID, w.is1.ID) != 1 ||
		w.audits("SUPER_ADMIN_NOMINATED") != 1 || w.audits("SUPER_ADMIN_NOMINATION_AUTHORIZED") != 1 {
		t.Fatal("promotion not recorded")
	}
	testutil.RequireStatus(t, decideNomination(w.is2, r.Body, "authorize"), http.StatusConflict, "nomination_closed")
	testutil.RequireStatus(t, nominate(w.admin, w.dev.ID), http.StatusConflict, "already_super_admin")

	// With another Super Admin besides proposer and nominee, a Super Admin must authorize.
	n = w.mustNominate(w.admin, w.owner.ID)
	testutil.RequireStatus(t, decideNomination(w.is1, n, "authorize"), http.StatusForbidden, "super_admin_required")
	testutil.RequireStatus(t, decideNomination(w.dev, n, "decline"), http.StatusOK, "")
	if w.superAdmins() != 2 || w.audits("SUPER_ADMIN_NOMINATION_DECLINED") != 1 {
		t.Fatal("decline")
	}
}

func TestNominationExpiresAfterSevenDays(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	n := w.mustNominate(w.admin, w.dev.ID)
	if w.count("SELECT count(*) FROM super_admin_nominations WHERE id = $1 AND expires_at = created_at + interval '7 days'", n["id"]) != 1 {
		t.Fatal("not 7 days")
	}
	w.exec("UPDATE super_admin_nominations SET created_at = created_at - interval '8 days', expires_at = expires_at - interval '8 days' WHERE id = $1", n["id"])
	testutil.RequireStatus(t, decideNomination(w.is1, n, "authorize"), http.StatusConflict, "nomination_expired")
	if r := w.get(w.admin.c, "/api/v1/super-admin-nominations"); len(r.Body["nominations"].([]any)) != 0 {
		t.Fatalf("expired nomination listed: %v", r.Body)
	}
	w.mustNominate(w.admin, w.dev.ID) // the expired one no longer blocks a new nomination
}

func TestConcurrentAuthorizationsAreConsistent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	n := w.mustNominate(w.admin, w.dev.ID)
	var wg sync.WaitGroup
	got := make([]testutil.Response, 2)
	wg.Go(func() { got[0] = decideNomination(w.is1, n, "authorize") })
	wg.Go(func() { got[1] = decideNomination(w.is2, n, "authorize") })
	wg.Wait()
	ok := 0
	for _, r := range got {
		switch {
		case r.Status == http.StatusOK:
			ok++
		case r.Status == http.StatusConflict:
		default:
			t.Fatalf("response %d %v", r.Status, r.Body)
		}
	}
	if ok != 1 || w.superAdmins() != 2 || w.audits("SUPER_ADMIN_NOMINATION_AUTHORIZED") != 1 {
		t.Fatalf("successes %d, Super Admins %d", ok, w.superAdmins())
	}
}

// The last Super Admin's termination proceeds and the system enters recovery (D2): an HR
// Manager nominates, a different InfoSec member authorizes, access workflows keep running.
// Only a Super Admin may end a Super Admin's employment, so the last one leaves through a
// termination another Super Admin scheduled.
func TestRecoveryAfterTheLastSuperAdminLeaves(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	hr := w.hr()
	other := w.person(testutil.EmployeeSpec{Roles: []string{"SUPER_ADMIN"}})
	pending := w.mustNominate(w.admin, w.owner.ID)
	testutil.RequireStatus(t, w.schedule(other, w.admin.ID, inDays(7)), http.StatusOK, "") // no last-Super-Admin guard any more
	w.mustLifecycle(w.admin, other.ID, "terminate")                                        // admin is the last Super Admin
	testutil.RequireStatus(t, nominate(hr, w.dev.ID), http.StatusForbidden, "forbidden")   // not in recovery
	w.exec("UPDATE employees SET termination_at = now() - interval '1 minute' WHERE id = $1", w.admin.ID)
	w.sweep()

	if h := w.health(hr); h["activeSuperAdmins"] != float64(0) || h["recoveryRequired"] != true {
		t.Fatalf("health = %v", h)
	}
	if w.audits("ADMIN_RECOVERY_REQUIRED") != 1 ||
		w.count("SELECT count(*) FROM notifications WHERE kind = 'ADMIN_RECOVERY_REQUIRED' AND recipient_id = ANY($1)", []uuid.UUID{hr.ID, w.is1.ID}) != 2 {
		t.Fatal("recovery not audited or HR and InfoSec not notified")
	}
	if w.count("SELECT count(*) FROM super_admin_nominations WHERE id = $1 AND status = 'CANCELLED'", pending["id"]) != 1 {
		t.Fatal("the leaver's nomination is still open")
	}

	w.approved(w.jiraViewer) // access workflows keep running in recovery mode
	tl.drain(t)
	requireGrant(t, w.myGrant(w.dev.c, w.jiraViewer), "ACTIVE", "ACTIVE / VERIFIED")

	self := w.mustNominate(hr, hr.ID) // HR may nominate themself
	n := w.mustNominate(hr, w.dev.ID)
	if n["recovery"] != true {
		t.Fatalf("nomination = %v", n)
	}
	testutil.RequireStatus(t, decideNomination(hr, n, "authorize"), http.StatusForbidden, "separation_of_duties")
	testutil.RequireStatus(t, decideNomination(w.dev, n, "authorize"), http.StatusForbidden, "separation_of_duties")
	testutil.RequireStatus(t, decideNomination(w.owner, n, "authorize"), http.StatusForbidden, "forbidden")
	testutil.RequireStatus(t, decideNomination(w.is1, n, "authorize"), http.StatusOK, "")
	if h := w.health(hr); h["recoveryRequired"] != false || w.superAdmins() != 1 {
		t.Fatalf("after recovery: %v", h)
	}
	testutil.RequireStatus(t, decideNomination(w.is2, self, "authorize"), http.StatusConflict, "recovery_ended")

	// A recovery nomination left open once recovery ended is closed, not stuck for 7 days.
	if r := w.get(w.dev.c, "/api/v1/super-admin-nominations"); len(r.Body["nominations"].([]any)) != 0 {
		t.Fatalf("a recovery nomination outlived recovery: %v", r.Body)
	}
	w.mustNominate(w.dev, hr.ID)
	if w.audits("SUPER_ADMIN_NOMINATION_CANCELLED") != 2 {
		t.Fatal("closing the recovery nomination is not audited")
	}
}

// An actor suspended while their nomination or decision waited for the lock acts no more (D7).
func TestSuccessionActorSuspendedWhileWaiting(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	n := w.mustNominate(w.admin, w.dev.ID)
	suspend := func(id uuid.UUID) func(context.Context, pgx.Tx) error {
		return func(ctx context.Context, tx pgx.Tx) error {
			if err := people.LockLifecycle(ctx, tx); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "UPDATE employees SET status = 'SUSPENDED' WHERE id = $1", id)
			return err
		}
	}
	var r testutil.Response
	testutil.WhileLocked(t, w.db.App, suspend(w.is1.ID), func() error { r = decideNomination(w.is1, n, "authorize"); return nil })
	testutil.RequireStatus(t, r, http.StatusUnauthorized, "")
	testutil.WhileLocked(t, w.db.App, suspend(w.admin.ID), func() error { r = nominate(w.admin, w.owner.ID); return nil })
	testutil.RequireStatus(t, r, http.StatusUnauthorized, "")
	if w.superAdmins() != 0 || w.count("SELECT count(*) FROM super_admin_nominations") != 1 {
		t.Fatal("a suspended actor still acted")
	}
}

// A nominee who became Super Admin another way (recover-admin) is refused, not a 500.
func TestAuthorizingASuperAdminNominee(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	n := w.mustNominate(w.admin, w.dev.ID)
	w.exec("INSERT INTO role_assignments (employee_id, role) VALUES ($1, 'SUPER_ADMIN')", w.dev.ID)
	testutil.RequireStatus(t, decideNomination(w.is1, n, "authorize"), http.StatusConflict, "already_super_admin")
}

func TestNominationExpiryIsAudited(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	n := w.mustNominate(w.admin, w.dev.ID)
	w.exec("UPDATE super_admin_nominations SET created_at = created_at - interval '8 days', expires_at = expires_at - interval '8 days' WHERE id = $1", n["id"])
	w.mustNominate(w.admin, w.dev.ID)
	if w.audits("SUPER_ADMIN_NOMINATION_EXPIRED") != 1 {
		t.Fatal("expiry not audited")
	}
}
