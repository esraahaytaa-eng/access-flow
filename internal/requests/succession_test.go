package requests

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
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

// roleLink is the nominee's role-acceptance link token from their own Mock Email Outbox.
func (w *world) roleLink(nominee person) string {
	w.t.Helper()
	r := w.get(nominee.c, "/api/v1/email-outbox")
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	for _, m := range r.Body["emails"].([]any) {
		if m := m.(map[string]any); m["kind"] == "ROLE_ACCEPTANCE" && m["linkAvailable"] == true {
			link := w.get(nominee.c, "/api/v1/email-outbox/"+m["id"].(string)+"/role-link")
			testutil.RequireStatus(w.t, link, http.StatusOK, "")
			return link.Body["token"].(string)
		}
	}
	w.t.Fatal("no usable role-acceptance email")
	return ""
}

func acceptRole(by person, token string) testutil.Response {
	return by.c.Do(http.MethodPost, "/api/v1/super-admin-nominations/accept", map[string]any{"token": token})
}

// authorizeSuccession: an independent InfoSec member or active Super Admin authorizes HR's
// succession nomination, which sends the nominee their link (R-2).
func (w *world) authorizeSuccession(by person, n map[string]any) map[string]any {
	w.t.Helper()
	r := decideNomination(by, n, "authorize")
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	return r.Body
}

func reissueRoleLink(by person, n map[string]any) testutil.Response {
	return by.c.Do(http.MethodPost, "/api/v1/super-admin-nominations/"+n["id"].(string)+"/reissue", map[string]any{"version": n["version"]})
}

// HR owns the last Super Admin's departure (D2 note 2026-10-30): the termination waits for
// a successor; HR suspends the leaver at once, nominates, and the nominee accepts through a
// single-use link, signed in as themself; then the termination goes through.
func TestHRSuccessionOfTheLastSuperAdmin(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	hr := w.hr()
	pending := w.mustNominate(w.admin, w.owner.ID) // an ordinary promotion the leaver proposed
	testutil.RequireStatus(t, w.lifecycle(hr, w.admin.ID, "terminate"), http.StatusConflict, "last_super_admin")
	testutil.RequireStatus(t, w.schedule(hr, w.admin.ID, inDays(7)), http.StatusConflict, "last_super_admin")

	// Emergency revocation: the leaver is blocked at once, and the system is in recovery.
	w.mustLifecycle(hr, w.admin.ID, "suspend")
	testutil.RequireStatus(t, w.get(w.admin.c, "/api/v1/auth/me"), http.StatusUnauthorized, "")
	if h := w.health(hr); h["activeSuperAdmins"] != float64(0) || h["recoveryRequired"] != true || w.audits("ADMIN_RECOVERY_REQUIRED") != 1 {
		t.Fatalf("health after the suspension = %v", h)
	}
	testutil.RequireStatus(t, w.lifecycle(hr, w.admin.ID, "terminate"), http.StatusConflict, "last_super_admin")
	testutil.RequireStatus(t, w.lifecycle(hr, w.admin.ID, "reactivate"), http.StatusForbidden, "privileged_reset_requires_super_admin")
	testutil.RequireStatus(t, hr.c.Do(http.MethodPost, "/api/v1/employees/"+w.admin.ID.String()+"/password-reset", map[string]any{}),
		http.StatusForbidden, "privileged_reset_requires_super_admin")

	w.approved(w.jiraViewer) // access workflows keep running meanwhile
	tl.drain(t)
	requireGrant(t, w.myGrant(w.dev.c, w.jiraViewer), "ACTIVE", "ACTIVE / VERIFIED")

	testutil.RequireStatus(t, nominate(hr, hr.ID), http.StatusForbidden, "self_change_forbidden")
	testutil.RequireStatus(t, nominate(w.is1, w.dev.ID), http.StatusForbidden, "forbidden")
	n := w.mustNominate(hr, w.dev.ID)
	if n["succession"] != true || n["status"] != "PENDING" || n["authorizedAt"] != nil ||
		w.count("SELECT count(*) FROM super_admin_nominations WHERE id = $1 AND expires_at = created_at + interval '7 days'", n["id"]) != 1 {
		t.Fatalf("succession nomination = %v", n)
	}
	// R-2: no link until an independent InfoSec member or active Super Admin authorizes;
	// the authorizers are told. HR and the nominee cannot authorize.
	if w.count("SELECT count(*) FROM email_outbox WHERE kind = 'ROLE_ACCEPTANCE'") != 0 ||
		w.count("SELECT count(*) FROM notifications WHERE kind = 'SUPER_ADMIN_NOMINATED' AND recipient_id = $1 AND entity_id = $2", w.is1.ID, n["id"]) != 1 {
		t.Fatal("a link went out before authorization, or InfoSec was not told")
	}
	testutil.RequireStatus(t, reissueRoleLink(hr, n), http.StatusConflict, "nomination_not_authorized")
	testutil.RequireStatus(t, decideNomination(hr, n, "authorize"), http.StatusForbidden, "separation_of_duties")
	testutil.RequireStatus(t, decideNomination(w.dev, n, "authorize"), http.StatusForbidden, "separation_of_duties")
	testutil.RequireStatus(t, decideNomination(w.mgr, n, "authorize"), http.StatusForbidden, "forbidden")
	n = w.authorizeSuccession(w.is1, n)
	if n["status"] != "PENDING" || n["authorizedAt"] == nil || w.superAdmins() != 0 || w.audits("SUPER_ADMIN_NOMINATION_AUTHORIZED") != 1 ||
		w.count("SELECT count(*) FROM super_admin_nominations WHERE id = $1 AND expires_at = authorized_at + interval '24 hours'", n["id"]) != 1 {
		t.Fatalf("authorized succession = %v", n)
	}
	testutil.RequireStatus(t, decideNomination(w.is2, n, "authorize"), http.StatusConflict, "nomination_needs_acceptance")

	// The link is in the nominee's own outbox only; HR and InfoSec see the status, never the link.
	token := w.roleLink(w.dev)
	for _, p := range []person{hr, w.is1} {
		r := w.get(p.c, "/api/v1/email-outbox")
		for _, m := range r.Body["emails"].([]any) {
			if m.(map[string]any)["kind"] == "ROLE_ACCEPTANCE" {
				t.Fatalf("%v sees the role-acceptance email", p.ID)
			}
		}
	}
	mail := w.count("SELECT count(*) FROM email_outbox WHERE kind = 'ROLE_ACCEPTANCE' AND recipient_id = $1", w.dev.ID)
	if mail != 1 || w.count("SELECT count(*) FROM audit_events WHERE details::text LIKE '%' || $1 || '%'", token) != 0 {
		t.Fatal("one email, and the token never in the audit trail")
	}
	var mailID string
	if err := w.db.App.QueryRow(context.Background(), "SELECT id::text FROM email_outbox WHERE kind = 'ROLE_ACCEPTANCE'").Scan(&mailID); err != nil {
		t.Fatal(err)
	}
	testutil.RequireStatus(t, w.get(hr.c, "/api/v1/email-outbox/"+mailID+"/role-link"), http.StatusNotFound, "not_found")

	// Only the nominee, signed in as themself, accepts; the password stays.
	testutil.RequireStatus(t, acceptRole(w.owner, token), http.StatusBadRequest, "invalid_role_link")
	testutil.RequireStatus(t, acceptRole(w.dev, "not-a-token"), http.StatusBadRequest, "invalid_role_link")
	password := w.count("SELECT count(*) FROM password_credentials WHERE employee_id = $1", w.dev.ID)
	testutil.RequireStatus(t, acceptRole(w.dev, token), http.StatusOK, "")
	if w.superAdmins() != 1 || password != 1 || w.count("SELECT count(*) FROM password_credentials WHERE employee_id = $1", w.dev.ID) != 1 {
		t.Fatalf("after acceptance: Super Admins %d", w.superAdmins())
	}
	if w.count(`SELECT count(*) FROM role_assignments WHERE employee_id = $1 AND role = 'SUPER_ADMIN' AND granted_by = $2`, w.dev.ID, hr.ID) != 1 ||
		w.audits("SUPER_ADMIN_ROLE_ACCEPTED") != 1 || w.audits("SUPER_ADMIN_ROLE_ACCEPT_FAILED") != 1 {
		t.Fatal("acceptance not recorded")
	}
	for _, p := range []person{hr, w.is1, w.is2} {
		if w.count("SELECT count(*) FROM notifications WHERE kind = 'SUPER_ADMIN_ROLE_ACCEPTED' AND recipient_id = $1", p.ID) != 1 {
			t.Fatalf("%v is not told the role was accepted", p.ID)
		}
	}
	testutil.RequireStatus(t, w.get(w.dev.c, "/api/v1/auth/me"), http.StatusUnauthorized, "") // a privileged role: sign in again
	again := testutil.NewClient(t, w.srv)
	testutil.RequireStatus(t, again.Login(w.dev.Email, testutil.Password), http.StatusOK, "")
	testutil.RequireStatus(t, acceptRole(person{w.dev.Employee, again}, token), http.StatusBadRequest, "invalid_role_link") // single use

	// Now HR completes the termination; the leaver's removals follow the normal workflow.
	w.mustLifecycle(hr, w.admin.ID, "terminate")
	if w.count("SELECT count(*) FROM super_admin_nominations WHERE id = $1 AND status = 'CANCELLED'", pending["id"]) != 1 {
		t.Fatal("the leaver's nomination is still open")
	}
	if h := w.health(hr); h["activeSuperAdmins"] != float64(1) || h["recoveryRequired"] != false {
		t.Fatalf("health = %v", h)
	}
}

// A role link lasts 24 hours and a reissue replaces it; only HR reissues.
func TestRoleLinkExpiresAndIsReplacedOnReissue(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	hr := w.hr()
	w.mustLifecycle(hr, w.admin.ID, "suspend")
	n := w.authorizeSuccession(w.is1, w.mustNominate(hr, w.dev.ID))
	first := w.roleLink(w.dev)
	testutil.RequireStatus(t, reissueRoleLink(w.dev, n), http.StatusForbidden, "forbidden")
	testutil.RequireStatus(t, reissueRoleLink(w.is1, n), http.StatusForbidden, "forbidden")
	r := reissueRoleLink(hr, n)
	testutil.RequireStatus(t, r, http.StatusOK, "")
	testutil.RequireStatus(t, reissueRoleLink(hr, n), http.StatusConflict, "version_conflict")
	testutil.RequireStatus(t, acceptRole(w.dev, first), http.StatusBadRequest, "invalid_role_link")
	second := w.roleLink(w.dev)
	if second == first || w.count("SELECT count(*) FROM email_outbox WHERE kind = 'ROLE_ACCEPTANCE' AND sealed_token IS NOT NULL") != 1 {
		t.Fatal("the reissue did not replace the link")
	}

	w.exec("UPDATE super_admin_nominations SET expires_at = now() - interval '1 second' WHERE id = $1", n["id"])
	testutil.RequireStatus(t, acceptRole(w.dev, second), http.StatusBadRequest, "invalid_role_link")
	if w.superAdmins() != 0 {
		t.Fatal("an expired link worked")
	}
	expired := w.get(hr.c, "/api/v1/super-admin-nominations").Body["nominations"].([]any)
	if len(expired) != 1 || expired[0].(map[string]any)["status"] != "EXPIRED" {
		t.Fatalf("HR sees %v", expired)
	}
	testutil.RequireStatus(t, reissueRoleLink(hr, expired[0].(map[string]any)), http.StatusOK, "")
	testutil.RequireStatus(t, acceptRole(w.dev, w.roleLink(w.dev)), http.StatusOK, "")
	if w.superAdmins() != 1 || w.audits("SUPER_ADMIN_ROLE_LINK_ISSUED") != 3 {
		t.Fatalf("Super Admins %d, links issued %d", w.superAdmins(), w.audits("SUPER_ADMIN_ROLE_LINK_ISSUED"))
	}
}

// Two tabs accepting the same link: exactly one wins.
func TestConcurrentRoleAcceptance(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	hr := w.hr()
	w.mustLifecycle(hr, w.admin.ID, "suspend")
	w.authorizeSuccession(w.is1, w.mustNominate(hr, w.dev.ID))
	token := w.roleLink(w.dev)
	other := testutil.NewClient(t, w.srv)
	testutil.RequireStatus(t, other.Login(w.dev.Email, testutil.Password), http.StatusOK, "")
	var wg sync.WaitGroup
	got := make([]testutil.Response, 2)
	wg.Go(func() { got[0] = acceptRole(w.dev, token) })
	wg.Go(func() { got[1] = acceptRole(person{w.dev.Employee, other}, token) })
	wg.Wait()
	ok := 0
	for _, r := range got {
		switch r.Status {
		case http.StatusOK:
			ok++
		case http.StatusBadRequest, http.StatusUnauthorized: // the winner ended the other session
		default:
			t.Fatalf("response %d %v", r.Status, r.Body)
		}
	}
	if ok != 1 || w.superAdmins() != 1 || w.audits("ROLE_ASSIGNED") != 1 {
		t.Fatalf("successes %d, Super Admins %d", ok, w.superAdmins())
	}
}

// A scheduled termination of a Super Admin who has become the last one waits (D2).
func TestScheduledTerminationOfTheLastSuperAdminWaits(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	hr := w.hr()
	other := w.person(testutil.EmployeeSpec{Roles: []string{"SUPER_ADMIN"}})
	testutil.RequireStatus(t, w.schedule(hr, w.admin.ID, inDays(7)), http.StatusOK, "")
	w.mustLifecycle(hr, other.ID, "terminate") // admin is the last one now
	w.exec("UPDATE employees SET termination_at = now() - interval '1 minute' WHERE id = $1", w.admin.ID)
	w.sweep()
	if w.superAdmins() != 1 || w.audits("EMPLOYEE_TERMINATED") != 1 {
		t.Fatalf("the sweep left no Super Admin: %d", w.superAdmins())
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

// HR terminating both remaining Super Admins at once leaves one: the guard is serialized.
func TestConcurrentTerminationsKeepASuperAdmin(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	hr, hr2 := w.hr(), w.hr()
	other := w.person(testutil.EmployeeSpec{Roles: []string{"SUPER_ADMIN"}})
	var wg sync.WaitGroup
	got := make([]testutil.Response, 2)
	wg.Go(func() { got[0] = w.lifecycle(hr, w.admin.ID, "terminate") })
	wg.Go(func() { got[1] = w.lifecycle(hr2, other.ID, "terminate") })
	wg.Wait()
	codes := map[int]int{}
	for _, r := range got {
		codes[r.Status]++
		if r.Status == http.StatusConflict && r.Body["error"] != "last_super_admin" {
			t.Fatalf("conflict %v", r.Body)
		}
	}
	if codes[http.StatusOK] != 1 || codes[http.StatusConflict] != 1 || w.superAdmins() != 1 {
		t.Fatalf("responses %v, Super Admins %d", codes, w.superAdmins())
	}
}

// HR's succession power exists only while at most one Super Admin is active: no second
// parallel succession, no reissue or acceptance once others are active (review finding).
func TestSuccessionOnlyWhileTheLastSuperAdminLeaves(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	hr := w.hr()
	other := w.person(testutil.EmployeeSpec{Roles: []string{"SUPER_ADMIN"}})
	testutil.RequireStatus(t, nominate(hr, w.dev.ID), http.StatusForbidden, "forbidden") // two active
	w.mustLifecycle(hr, other.ID, "terminate")
	n := w.authorizeSuccession(w.is1, w.mustNominate(hr, w.dev.ID))
	testutil.RequireStatus(t, nominate(hr, w.owner.ID), http.StatusConflict, "nomination_pending") // one succession at a time
	token := w.roleLink(w.dev)

	// Two Super Admins again (e.g. recover-admin or a promotion): the old link is dead.
	w.exec("INSERT INTO role_assignments (employee_id, role) VALUES ($1, 'SUPER_ADMIN')", w.owner.ID)
	testutil.RequireStatus(t, acceptRole(w.dev, token), http.StatusConflict, "super_admins_active")
	testutil.RequireStatus(t, reissueRoleLink(hr, n), http.StatusConflict, "super_admins_active")
	if w.superAdmins() != 2 || w.audits("SUPER_ADMIN_ROLE_ACCEPT_FAILED") != 1 {
		t.Fatalf("Super Admins %d", w.superAdmins())
	}
}

// Suspending a proposer closes their open nominations; a suspended or demoted HR proposer's
// nominee cannot accept (review finding).
func TestSuspendedProposersNominationsClose(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	hr, hr2 := w.hr(), w.hr()
	promotion := w.mustNominate(w.admin, w.owner.ID)
	w.mustLifecycle(hr, w.admin.ID, "suspend")
	if w.count("SELECT count(*) FROM super_admin_nominations WHERE id = $1 AND status = 'CANCELLED'", promotion["id"]) != 1 {
		t.Fatal("the suspended Super Admin's promotion is still open")
	}
	w.authorizeSuccession(w.is1, w.mustNominate(hr, w.dev.ID))
	token := w.roleLink(w.dev)
	w.exec("UPDATE role_assignments SET valid_to = now() WHERE employee_id = $1 AND role = 'HR_MANAGER'", hr.ID)
	testutil.RequireStatus(t, acceptRole(w.dev, token), http.StatusBadRequest, "invalid_role_link")
	w.mustLifecycle(hr2, hr.ID, "suspend")
	if w.superAdmins() != 0 || w.count("SELECT count(*) FROM super_admin_nominations WHERE status = 'PENDING'") != 0 {
		t.Fatal("the suspended HR proposer's succession is still open")
	}
}

// A nomination cancelled because its nominee left drops the link's encrypted copy in the
// same transaction, not at the next sweep (BUG-B-07).
func TestCancelledNominationClearsItsLinkAtOnce(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	hr := w.hr()
	w.mustLifecycle(hr, w.admin.ID, "suspend")
	n := w.authorizeSuccession(w.is1, w.mustNominate(hr, w.dev.ID))
	if w.count("SELECT count(*) FROM email_outbox WHERE nomination_id = $1 AND sealed_token IS NOT NULL", n["id"]) != 1 {
		t.Fatal("no sealed link to clear")
	}
	w.mustLifecycle(hr, w.dev.ID, "suspend")
	if w.count("SELECT count(*) FROM super_admin_nominations WHERE id = $1 AND status = 'CANCELLED'", n["id"]) != 1 ||
		w.count("SELECT count(*) FROM email_outbox WHERE nomination_id = $1 AND sealed_token IS NOT NULL", n["id"]) != 0 {
		t.Fatal("the cancelled nomination's link is still stored")
	}
}

// F-1: suspended Super Admins still count, so HR cannot suspend their way into a succession;
// HR keeps emergency suspension, and recover-admin stays the way back once none is active.
func TestSuspendingSuperAdminsOpensNoHRSuccession(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	hr := w.hr()
	a2 := w.person(testutil.EmployeeSpec{Roles: []string{"SUPER_ADMIN"}})
	a3 := w.person(testutil.EmployeeSpec{Roles: []string{"SUPER_ADMIN"}})
	w.mustLifecycle(hr, a2.ID, "suspend")
	w.mustLifecycle(hr, a3.ID, "suspend")
	testutil.RequireStatus(t, nominate(hr, w.dev.ID), http.StatusForbidden, "forbidden")
	w.mustLifecycle(hr, w.admin.ID, "suspend")
	if h := w.health(hr); h["recoveryRequired"] != true {
		t.Fatalf("health = %v", h)
	}
	testutil.RequireStatus(t, nominate(hr, w.dev.ID), http.StatusForbidden, "forbidden")
	if w.count("SELECT count(*) FROM super_admin_nominations") != 0 || w.superAdmins() != 0 {
		t.Fatal("a succession opened while suspended Super Admins remain")
	}
	authSvc, err := auth.NewService(w.db.App, auth.Options{Secret: []byte(strings.Repeat("k", 32)), BootstrapFile: t.TempDir() + "/admin.txt", MailKey: testutil.MailKey})
	if err != nil {
		t.Fatal(err)
	}
	if err := authSvc.RecoverAdmin(context.Background(), w.dev.Email); err != nil {
		t.Fatalf("recover-admin: %v", err)
	}
	if w.superAdmins() != 1 {
		t.Fatal("recover-admin did not restore a Super Admin")
	}
}

// R-2: with the last Super Admin suspended, HR and the nominee alone never create a Super
// Admin. An authorizer declining closes the nomination; a link that exists without an
// authorization (one sent before this rule) is refused.
func TestHRSuccessionNeedsAnIndependentAuthorizer(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	hr := w.hr()
	w.mustLifecycle(hr, w.admin.ID, "suspend")
	n := w.mustNominate(hr, w.dev.ID)
	if w.count("SELECT count(*) FROM email_outbox WHERE recipient_id = $1 AND kind = 'ROLE_ACCEPTANCE'", w.dev.ID) != 0 {
		t.Fatal("the nominee got a link before authorization")
	}
	list := w.get(w.is1.c, "/api/v1/super-admin-nominations").Body["nominations"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["canAuthorize"] != true {
		t.Fatalf("InfoSec cannot authorize: %v", list)
	}
	if l := w.get(hr.c, "/api/v1/super-admin-nominations").Body["nominations"].([]any); l[0].(map[string]any)["canAuthorize"] != false {
		t.Fatalf("HR may authorize their own nomination: %v", l)
	}
	testutil.RequireStatus(t, decideNomination(w.is1, n, "decline"), http.StatusOK, "")
	if w.count("SELECT count(*) FROM super_admin_nominations WHERE id = $1 AND status = 'DECLINED' AND decided_by = $2", n["id"], w.is1.ID) != 1 {
		t.Fatal("decline not recorded")
	}

	n = w.authorizeSuccession(w.is1, w.mustNominate(hr, w.dev.ID))
	token := w.roleLink(w.dev)
	w.exec("UPDATE super_admin_nominations SET authorized_by = NULL, authorized_at = NULL WHERE id = $1", n["id"])
	testutil.RequireStatus(t, acceptRole(w.dev, token), http.StatusBadRequest, "invalid_role_link")
	if w.superAdmins() != 0 || w.count("SELECT count(*) FROM audit_events WHERE action = 'SUPER_ADMIN_ROLE_ACCEPT_FAILED' AND details->>'reason' = 'not_authorized'") != 1 {
		t.Fatal("an unauthorized link made a Super Admin")
	}
}

// Two authorizers at once: exactly one link goes out.
func TestConcurrentSuccessionAuthorizations(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	hr := w.hr()
	w.mustLifecycle(hr, w.admin.ID, "suspend")
	n := w.mustNominate(hr, w.dev.ID)
	var wg sync.WaitGroup
	got := make([]testutil.Response, 2)
	wg.Go(func() { got[0] = decideNomination(w.is1, n, "authorize") })
	wg.Go(func() { got[1] = decideNomination(w.is2, n, "authorize") })
	wg.Wait()
	ok := 0
	for _, r := range got {
		switch r.Status {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
		default:
			t.Fatalf("response %d %v", r.Status, r.Body)
		}
	}
	if ok != 1 || w.audits("SUPER_ADMIN_ROLE_LINK_ISSUED") != 1 || w.count("SELECT count(*) FROM email_outbox WHERE kind = 'ROLE_ACCEPTANCE'") != 1 {
		t.Fatalf("successes %d", ok)
	}
}
