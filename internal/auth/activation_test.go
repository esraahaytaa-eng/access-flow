package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

const newPassword = "violet anchor quiet 29"

// issue gives id a new activation link (as a reset by the system) and returns its mock email.
func (e *testEnv) issue(id uuid.UUID) uuid.UUID {
	e.t.Helper()
	ctx := context.Background()
	err := platform.InTx(ctx, e.db.App, func(tx pgx.Tx, now time.Time) error {
		return e.svc.IssueActivation(ctx, tx, now, nil, id, "RESET")
	})
	if err != nil {
		e.t.Fatalf("issue: %v", err)
	}
	var mail uuid.UUID
	if err := e.db.App.QueryRow(ctx, "SELECT id FROM email_outbox WHERE recipient_id = $1 ORDER BY id DESC LIMIT 1", id).Scan(&mail); err != nil {
		e.t.Fatal(err)
	}
	return mail
}

// admin signs in a Super Admin.
func (e *testEnv) admin() (employee, *testutil.Client) {
	e.t.Helper()
	a := e.employee("SUPER_ADMIN")
	c := e.client()
	testutil.RequireStatus(e.t, c.Login(a.Email, a.Password), http.StatusOK, "")
	return a, c
}

// reveal reads the link of mock email mail as Super Admin c and returns its token.
func reveal(t *testing.T, c *testutil.Client, mail uuid.UUID) string {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/email-outbox/"+mail.String()+"/activation-link", nil)
	testutil.RequireStatus(t, r, http.StatusOK, "")
	token, _ := r.Body["token"].(string)
	if len(token) != 43 {
		t.Fatalf("token %q; want 43 base64url characters", token)
	}
	return token
}

func activate(c *testutil.Client, token, password string) testutil.Response {
	return c.Do(http.MethodPost, "/api/v1/auth/activate", map[string]string{"token": token, "newPassword": password})
}

func TestIssueActivationKeepsOnlyAHash(t *testing.T) {
	env := newEnv(t)
	_, admin := env.admin()
	emp := env.employee()
	session := env.client()
	testutil.RequireStatus(t, session.Login(emp.Email, emp.Password), http.StatusOK, "")

	mail := env.issue(emp.ID)

	// A reset removes the current password and ends every session at once (M18 ⚑1).
	if env.count("SELECT count(*) FROM password_credentials WHERE employee_id = $1", emp.ID) != 0 {
		t.Error("the old password is still stored")
	}
	testutil.RequireStatus(t, session.Do(http.MethodGet, "/api/v1/probe", nil), http.StatusUnauthorized, "authentication_required")
	if env.auditCount("ACTIVATION_LINK_ISSUED", emp.ID) != 1 {
		t.Error("issue not audited")
	}
	token := reveal(t, admin, mail)
	sum := sha256.Sum256([]byte(token))
	if env.count("SELECT count(*) FROM activation_tokens WHERE employee_id = $1 AND token_hash = $2", emp.ID, sum[:]) != 1 {
		t.Error("the stored hash is not SHA-256 of the token")
	}
	var sealed []byte
	_ = env.db.App.QueryRow(context.Background(), "SELECT sealed_token FROM email_outbox WHERE id = $1", mail).Scan(&sealed)
	if len(sealed) == 0 || bytes.Contains(sealed, []byte(token)) {
		t.Error("the mock email does not hold the token encrypted")
	}
	requireNoToken(t, env, token)
}

// requireNoToken: the raw token is in no audit event, mock email text or log line.
func requireNoToken(t *testing.T, env *testEnv, token string) {
	t.Helper()
	if env.count("SELECT count(*) FROM audit_events WHERE details::text LIKE '%' || $1 || '%' OR coalesce(reason, '') LIKE '%' || $1 || '%'", token) != 0 {
		t.Error("the token is in an audit event")
	}
	if env.count("SELECT count(*) FROM email_outbox WHERE subject LIKE '%' || $1 || '%' OR body LIKE '%' || $1 || '%'", token) != 0 {
		t.Error("the token is in a mock email's text")
	}
	if strings.Contains(env.logs.String(), token) {
		t.Error("the token is in the log")
	}
}

func TestOnlyASuperAdminRevealsActivationLinks(t *testing.T) {
	env := newEnv(t)
	emp := env.employee()
	mail := env.issue(emp.ID)
	path := "/api/v1/email-outbox/" + mail.String() + "/activation-link"

	testutil.RequireStatus(t, env.client().Do(http.MethodGet, path, nil), http.StatusUnauthorized, "authentication_required")
	for _, role := range []string{"HR_MANAGER", "INFOSEC", "AUDITOR", ""} {
		var roles []string
		if role != "" {
			roles = []string{role}
		}
		other := env.employee(roles...)
		c := env.client()
		testutil.RequireStatus(t, c.Login(other.Email, other.Password), http.StatusOK, "")
		testutil.RequireStatus(t, c.Do(http.MethodGet, path, nil), http.StatusForbidden, "forbidden")
	}
	// A Super Admin still on a temporary password may only change it.
	temp := env.employee("SUPER_ADMIN")
	env.setPassword(temp.ID, "temporary password 1", true, nil)
	c := env.client()
	testutil.RequireStatus(t, c.Login(temp.Email, "temporary password 1"), http.StatusOK, "")
	testutil.RequireStatus(t, c.Do(http.MethodGet, path, nil), http.StatusForbidden, "password_change_required")
	if env.auditCount("ACTIVATION_LINK_VIEWED", emp.ID) != 0 {
		t.Fatal("a refused reveal was audited as viewed")
	}

	a, admin := env.admin()
	r := admin.Do(http.MethodGet, path, nil)
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q; want no-store", r.Header.Get("Cache-Control"))
	}
	if env.count("SELECT count(*) FROM audit_events WHERE action = 'ACTIVATION_LINK_VIEWED' AND actor_id = $1 AND subject_employee_id = $2", a.ID, emp.ID) != 1 {
		t.Error("the reveal is not audited with the Super Admin as actor")
	}
	testutil.RequireStatus(t, admin.Do(http.MethodGet, "/api/v1/email-outbox/"+uuid.NewString()+"/activation-link", nil), http.StatusNotFound, "not_found")
}

func TestActivationSetsThePasswordWithoutSigningIn(t *testing.T) {
	env := newEnv(t)
	a, admin := env.admin()
	emp := env.employee()
	mail := env.issue(emp.ID)
	token := reveal(t, admin, mail)

	c := env.client()
	r := activate(c, token, newPassword)
	testutil.RequireStatus(t, r, http.StatusNoContent, "")
	if c.Cookie("accessflow_session") != "" {
		t.Fatal("activation set a session cookie; it must not sign anyone in")
	}
	testutil.RequireStatus(t, c.Do(http.MethodGet, "/api/v1/auth/me", nil), http.StatusUnauthorized, "authentication_required")
	r = c.Login(emp.Email, newPassword)
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Body["mustChangePassword"] != false {
		t.Error("the chosen password is treated as temporary")
	}

	// Single use: the link and its mock copy are gone.
	testutil.RequireStatus(t, activate(env.client(), token, "another fine password"), http.StatusBadRequest, "invalid_activation_link")
	testutil.RequireStatus(t, admin.Do(http.MethodGet, "/api/v1/email-outbox/"+mail.String()+"/activation-link", nil), http.StatusGone, "activation_link_unavailable")
	if env.count("SELECT count(*) FROM email_outbox WHERE id = $1 AND sealed_token IS NULL", mail) != 1 {
		t.Error("the encrypted link was kept after use")
	}
	if env.count(`SELECT count(*) FROM audit_events WHERE action = 'ACTIVATION_COMPLETED' AND actor_id = $1
		AND details->'viewedBy' @> to_jsonb(ARRAY[$2::text])`, emp.ID, a.ID.String()) != 1 {
		t.Error("completion not audited with who viewed the link")
	}
	requireNoToken(t, env, token)
}

func TestExpiredReplacedAndInactiveLinksFail(t *testing.T) {
	env := newEnv(t)
	_, admin := env.admin()

	expired := env.employee()
	mail := env.issue(expired.ID)
	token := reveal(t, admin, mail)
	env.exec("UPDATE activation_tokens SET expires_at = now() - interval '1 second' WHERE employee_id = $1", expired.ID)
	testutil.RequireStatus(t, activate(env.client(), token, newPassword), http.StatusBadRequest, "invalid_activation_link")
	testutil.RequireStatus(t, admin.Do(http.MethodGet, "/api/v1/email-outbox/"+mail.String()+"/activation-link", nil), http.StatusGone, "activation_link_unavailable")

	replaced := env.employee()
	oldMail := env.issue(replaced.ID)
	oldToken := reveal(t, admin, oldMail)
	newMail := env.issue(replaced.ID)
	if env.count("SELECT count(*) FROM email_outbox WHERE id = $1 AND sealed_token IS NULL", oldMail) != 1 {
		t.Error("a reissue kept the old encrypted link")
	}
	testutil.RequireStatus(t, activate(env.client(), oldToken, newPassword), http.StatusBadRequest, "invalid_activation_link")
	testutil.RequireStatus(t, activate(env.client(), reveal(t, admin, newMail), newPassword), http.StatusNoContent, "")

	suspended := env.employee()
	token = reveal(t, admin, env.issue(suspended.ID))
	env.exec("UPDATE employees SET status = 'SUSPENDED' WHERE id = $1", suspended.ID)
	testutil.RequireStatus(t, activate(env.client(), token, newPassword), http.StatusBadRequest, "invalid_activation_link")

	for id, reason := range map[uuid.UUID]string{expired.ID: "expired", replaced.ID: "replaced", suspended.ID: "inactive"} {
		if env.count("SELECT count(*) FROM audit_events WHERE action = 'ACTIVATION_FAILED' AND subject_employee_id = $1 AND details->>'reason' = $2", id, reason) != 1 {
			t.Errorf("no ACTIVATION_FAILED %s", reason)
		}
	}
	testutil.RequireStatus(t, activate(env.client(), "not-a-token", newPassword), http.StatusBadRequest, "invalid_activation_link")
	testutil.RequireStatus(t, activate(env.client(), strings.Repeat("A", 43), newPassword), http.StatusBadRequest, "invalid_activation_link")
}

func TestAWeakPasswordKeepsTheLinkUsable(t *testing.T) {
	env := newEnv(t)
	_, admin := env.admin()
	emp := env.employee()
	token := reveal(t, admin, env.issue(emp.ID))
	name, _, _ := strings.Cut(emp.Email, "@")

	for pw, code := range map[string]string{"short": "password_length", "Password1234": "common_password", name + "-2026-x": "password_contains_email"} {
		r := activate(env.client(), token, pw)
		testutil.RequireStatus(t, r, http.StatusBadRequest, code)
		if r.Body["field"] != "newPassword" {
			t.Errorf("%s: field = %v", code, r.Body["field"])
		}
	}
	testutil.RequireStatus(t, activate(env.client(), token, newPassword), http.StatusNoContent, "")

	// A dead link gets the generic answer even with a weak password.
	testutil.RequireStatus(t, activate(env.client(), token, "short"), http.StatusBadRequest, "invalid_activation_link")
	if env.count("SELECT count(*) FROM audit_events WHERE action = 'ACTIVATION_FAILED' AND subject_employee_id = $1 AND details->>'reason' = 'used'", emp.ID) != 1 {
		t.Error("no ACTIVATION_FAILED for a used link")
	}
}

func TestActivationWaitingForTheLockSeesTheCommittedChange(t *testing.T) {
	env := newEnv(t)
	_, admin := env.admin()
	for change, reason := range map[string]string{
		"UPDATE activation_tokens SET used_at = now() WHERE employee_id = $1":    "used",     // another activation won
		"UPDATE activation_tokens SET revoked_at = now() WHERE employee_id = $1": "replaced", // a reissue committed
	} {
		emp := env.employee()
		token := reveal(t, admin, env.issue(emp.ID))
		var r testutil.Response
		testutil.WhileLocked(t, env.db.App, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", emp.ID); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, change, emp.ID)
			return err
		}, func() error { r = activate(env.client(), token, newPassword); return nil })
		testutil.RequireStatus(t, r, http.StatusBadRequest, "invalid_activation_link")
		if env.count("SELECT count(*) FROM password_credentials WHERE employee_id = $1", emp.ID) != 0 ||
			env.count("SELECT count(*) FROM audit_events WHERE action = 'ACTIVATION_FAILED' AND subject_employee_id = $1 AND details->>'reason' = $2", emp.ID, reason) != 1 {
			t.Errorf("%s: a password was set or the failure not audited", reason)
		}
	}
}

// newKeyedService is a second service on the same database, as after an app restart.
func newKeyedService(t *testing.T, env *testEnv, key []byte) *testutil.Client {
	t.Helper()
	svc, err := NewService(env.db.App, Options{Secret: testSecret, BootstrapFile: filepath.Join(t.TempDir(), "b.txt"),
		LoginRateLimit: 1000, MailKey: key})
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Mount("/api/v1/auth", svc.Routes())
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(svc.RequireSession)
		svc.RegisterActivations(r)
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return testutil.NewClient(t, srv)
}

func TestLinksSurviveARestartButNotAnotherKey(t *testing.T) {
	env := newEnv(t)
	a := env.employee("SUPER_ADMIN")
	emp := env.employee()
	mail := env.issue(emp.ID)
	path := "/api/v1/email-outbox/" + mail.String() + "/activation-link"

	restarted := newKeyedService(t, env, testMailKey)
	testutil.RequireStatus(t, restarted.Login(a.Email, a.Password), http.StatusOK, "")
	testutil.RequireStatus(t, restarted.Do(http.MethodGet, path, nil), http.StatusOK, "")

	other := newKeyedService(t, env, bytes.Repeat([]byte{7}, 32))
	testutil.RequireStatus(t, other.Login(a.Email, a.Password), http.StatusOK, "")
	testutil.RequireStatus(t, other.Do(http.MethodGet, path, nil), http.StatusGone, "activation_link_unavailable")

	// Without a usable key nothing can be revealed or issued, and nothing is half-written.
	none := newKeyedService(t, env, nil)
	testutil.RequireStatus(t, none.Login(a.Email, a.Password), http.StatusOK, "")
	testutil.RequireStatus(t, none.Do(http.MethodGet, path, nil), http.StatusGone, "activation_link_unavailable")
}

func TestIssuingWithoutAMailKeyChangesNothing(t *testing.T) {
	env := newEnv(t)
	emp := env.employee()
	svc, err := NewService(env.db.App, Options{Secret: testSecret, BootstrapFile: filepath.Join(t.TempDir(), "b.txt")})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	err = platform.InTx(ctx, env.db.App, func(tx pgx.Tx, now time.Time) error {
		return svc.IssueActivation(ctx, tx, now, nil, emp.ID, "RESET")
	})
	var e *platform.Error
	if !errors.As(err, &e) || e.Status != http.StatusServiceUnavailable || e.Code != "mock_email_unavailable" {
		t.Fatalf("err = %v; want 503 mock_email_unavailable", err)
	}
	if env.count("SELECT count(*) FROM activation_tokens") != 0 || env.count("SELECT count(*) FROM password_credentials WHERE employee_id = $1", emp.ID) != 1 {
		t.Fatal("a refused issue changed data")
	}
}

func TestActivationList(t *testing.T) {
	env := newEnv(t)
	_, admin := env.admin()
	pending, expired, done := env.employee(), env.employee(), env.employee()
	env.issue(pending.ID)
	env.issue(expired.ID)
	env.exec("UPDATE activation_tokens SET expires_at = now() - interval '1 second' WHERE employee_id = $1", expired.ID)
	env.issue(done.ID)
	testutil.Activate(t, admin, env.db.App, done.ID, newPassword)
	noLink := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{}) // e.g. imported before M18
	withPassword := env.employee()                                        // e.g. a demo account: not listed
	terminated := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{Status: "TERMINATED"})

	r := admin.Do(http.MethodGet, "/api/v1/activations", nil)
	testutil.RequireStatus(t, r, http.StatusOK, "")
	got := map[string]string{}
	for _, a := range r.Body["activations"].([]any) {
		m := a.(map[string]any)
		got[m["employee"].(map[string]any)["id"].(string)] = m["status"].(string)
	}
	want := map[string]string{pending.ID.String(): "PENDING", expired.ID.String(): "EXPIRED", done.ID.String(): "COMPLETED", noLink.ID.String(): "NO_LINK"}
	for id, status := range want {
		if got[id] != status {
			t.Errorf("%s: %q; want %s", id, got[id], status)
		}
	}
	if _, ok := got[withPassword.ID.String()]; ok {
		t.Error("an employee with a password and no link is listed")
	}
	if _, ok := got[terminated.ID.String()]; ok {
		t.Error("a terminated employee is listed")
	}

	hr := env.employee("HR_MANAGER")
	c := env.client()
	testutil.RequireStatus(t, c.Login(hr.Email, hr.Password), http.StatusOK, "")
	testutil.RequireStatus(t, c.Do(http.MethodGet, "/api/v1/activations", nil), http.StatusForbidden, "forbidden")
}

func TestExpiredLinksAreCleared(t *testing.T) {
	env := newEnv(t)
	old, fresh := env.employee(), env.employee()
	oldMail, freshMail := env.issue(old.ID), env.issue(fresh.ID)
	env.exec("UPDATE activation_tokens SET expires_at = now() - interval '1 second' WHERE employee_id = $1", old.ID)

	n, err := ClearExpiredLinks(context.Background(), env.db.App)
	if err != nil || n != 1 {
		t.Fatalf("cleared %d, %v; want 1", n, err)
	}
	if env.count("SELECT count(*) FROM email_outbox WHERE id = $1 AND sealed_token IS NULL", oldMail) != 1 ||
		env.count("SELECT count(*) FROM email_outbox WHERE id = $1 AND sealed_token IS NOT NULL", freshMail) != 1 {
		t.Fatal("wrong links cleared")
	}
}

// issueBy gives id a password reset link issued by actor and returns its mock email.
func (e *testEnv) issueBy(actor, id uuid.UUID) uuid.UUID {
	e.t.Helper()
	ctx := context.Background()
	if err := platform.InTx(ctx, e.db.App, func(tx pgx.Tx, now time.Time) error {
		return e.svc.IssueActivation(ctx, tx, now, &actor, id, "RESET")
	}); err != nil {
		e.t.Fatalf("issue: %v", err)
	}
	var mail uuid.UUID
	if err := e.db.App.QueryRow(ctx, "SELECT id FROM email_outbox WHERE recipient_id = $1 ORDER BY id DESC LIMIT 1", id).Scan(&mail); err != nil {
		e.t.Fatal(err)
	}
	return mail
}

// A Super Admin who reset a privileged account cannot reveal its link and sign in as that
// person: an independent Super Admin hands it over, and the others are told (R-1).
func TestPrivilegedActivationLinkNeedsAnIndependentSuperAdmin(t *testing.T) {
	env := newEnv(t)
	issuer, issuerC := env.admin()
	other, otherC := env.admin()
	path := func(mail uuid.UUID) string { return "/api/v1/email-outbox/" + mail.String() + "/activation-link" }

	for _, role := range []string{"SUPER_ADMIN", "INFOSEC"} {
		target := env.employee(role)
		mail := env.issueBy(issuer.ID, target.ID)
		testutil.RequireStatus(t, issuerC.Do(http.MethodGet, path(mail), nil), http.StatusForbidden, "independent_super_admin_required")
		if env.auditCount("ACTIVATION_LINK_VIEWED", target.ID) != 0 {
			t.Fatalf("%s: a refused reveal was audited as viewed", role)
		}
		reveal(t, otherC, mail)
		if env.count(`SELECT count(*) FROM notifications WHERE kind = 'PRIVILEGED_ACTIVATION_LINK_VIEWED' AND recipient_id = $1
			AND details->>'viewedBy' = $2 AND details->>'employee' = $3`, issuer.ID, other.ID.String(), target.ID.String()) != 1 {
			t.Fatalf("%s: the other Super Admin is not told about the reveal", role)
		}
		if env.count("SELECT count(*) FROM notifications WHERE kind = 'PRIVILEGED_ACTIVATION_LINK_VIEWED' AND recipient_id = $1", other.ID) != 0 {
			t.Fatalf("%s: the revealer notified themself", role)
		}
	}

	// Nobody reveals the link to their own account.
	self, selfC := env.admin()
	mail := env.issue(self.ID)
	env.setPassword(self.ID, goodPassword, false, nil)
	testutil.RequireStatus(t, selfC.Login(self.Email, goodPassword), http.StatusOK, "")
	testutil.RequireStatus(t, selfC.Do(http.MethodGet, path(mail), nil), http.StatusForbidden, "independent_super_admin_required")

	// An ordinary employee's link is revealed by the issuer as before, with no extra notice.
	emp := env.employee()
	reveal(t, issuerC, env.issueBy(issuer.ID, emp.ID))
	if env.count("SELECT count(*) FROM notifications WHERE kind = 'PRIVILEGED_ACTIVATION_LINK_VIEWED' AND details->>'employee' = $1", emp.ID.String()) != 0 {
		t.Fatal("an ordinary activation notified the Super Admins")
	}
}
