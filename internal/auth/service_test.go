package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func TestLoginSetsCookiesAndMeShowsTheEmployee(t *testing.T) {
	env := newEnv(t)
	emp := env.employee("AUDITOR", "INFOSEC")
	c := env.client()

	r := c.login(strings.ToUpper(emp.Email), emp.Password) // email matching ignores case
	requireStatus(t, r, http.StatusOK, "")
	if r.body["mustChangePassword"] != false {
		t.Errorf("mustChangePassword = %v; want false", r.body["mustChangePassword"])
	}

	var session, csrf *http.Cookie
	for _, ck := range (&http.Response{Header: r.header}).Cookies() {
		switch ck.Name {
		case sessionCookie:
			session = ck
		case csrfCookie:
			csrf = ck
		}
	}
	if session == nil || !session.HttpOnly || session.SameSite != http.SameSiteLaxMode || session.Secure {
		t.Fatalf("session cookie = %+v; want HttpOnly, SameSite=Lax, not Secure on plain HTTP", session)
	}
	if csrf == nil || csrf.HttpOnly || csrf.Value == "" {
		t.Fatalf("CSRF cookie = %+v; want a readable value", csrf)
	}

	me := c.do(http.MethodGet, "/api/v1/auth/me", nil)
	requireStatus(t, me, http.StatusOK, "")
	got := me.body["employee"].(map[string]any)
	if got["id"] != emp.ID.String() || got["email"] != emp.Email {
		t.Errorf("me = %v; want %s %s", got, emp.ID, emp.Email)
	}
	if roles := got["roles"].([]any); len(roles) != 2 || roles[0] != "AUDITOR" || roles[1] != "INFOSEC" {
		t.Errorf("roles = %v; want [AUDITOR INFOSEC]", roles)
	}
	if n := env.auditCount("LOGIN_SUCCEEDED", emp.ID); n != 1 {
		t.Errorf("LOGIN_SUCCEEDED events = %d; want 1", n)
	}
}

// Every failed login gets the same answer, so it reveals nothing about the account (A4).
func TestFailedLoginsLookIdentical(t *testing.T) {
	env := newEnv(t)
	active := env.employee()
	suspended := env.employee()
	env.exec("UPDATE employees SET status = 'SUSPENDED' WHERE id = $1", suspended.ID)
	terminated := env.employee()
	env.exec("UPDATE employees SET status = 'TERMINATED' WHERE id = $1", terminated.ID)
	locked := env.employee()
	env.exec("UPDATE employees SET locked_until = now() + interval '10 minutes' WHERE id = $1", locked.ID)
	noPassword := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{})
	corrupted := env.employee()
	env.exec("UPDATE password_credentials SET password_hash = 'not a hash' WHERE employee_id = $1", corrupted.ID)

	cases := []struct {
		name, email, password, reason string
		subject                       uuid.UUID
	}{
		{"unknown email", "nobody@example.com", goodPassword, "unknown_email", uuid.Nil},
		{"wrong password", active.Email, "wrong password!", "wrong_password", active.ID},
		{"suspended", suspended.Email, goodPassword, "inactive", suspended.ID},
		{"terminated", terminated.Email, goodPassword, "inactive", terminated.ID},
		{"locked, even with the right password", locked.Email, goodPassword, "locked", locked.ID},
		{"empty input", "", "", "unknown_email", uuid.Nil},
		{"employee without a password", noPassword.Email, goodPassword, "wrong_password", noPassword.ID},
		{"corrupted stored hash", corrupted.Email, goodPassword, "wrong_password", corrupted.ID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := env.client().login(tc.email, tc.password)
			requireStatus(t, r, http.StatusUnauthorized, "invalid_credentials")
			if len(r.body) != 1 {
				t.Errorf("body = %v; want only the error code", r.body)
			}
			var n int
			err := env.db.App.QueryRow(context.Background(), `SELECT count(*) FROM audit_events
				WHERE action = 'LOGIN_FAILED' AND details->>'reason' = $1 AND subject_employee_id IS NOT DISTINCT FROM $2`,
				tc.reason, nullable(tc.subject)).Scan(&n)
			if err != nil || n == 0 {
				t.Errorf("LOGIN_FAILED with reason %s: count %d, err %v; want recorded", tc.reason, n, err)
			}
		})
	}
}

func TestFiveFailuresLockTheAccountForFifteenMinutes(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	emp := env.employee()
	c := env.client()

	for i := 1; i <= 4; i++ {
		requireStatus(t, c.login(emp.Email, "wrong password!"), http.StatusUnauthorized, "invalid_credentials")
	}
	requireStatus(t, c.login(emp.Email, emp.Password), http.StatusOK, "") // 4 failures do not lock
	for i := 1; i <= 5; i++ {
		c.login(emp.Email, "wrong password!")
	}

	var lockedFor time.Duration
	err := env.db.App.QueryRow(ctx, "SELECT locked_until - now() FROM employees WHERE id = $1", emp.ID).Scan(&lockedFor)
	if err != nil || lockedFor < 14*time.Minute || lockedFor > 15*time.Minute {
		t.Fatalf("locked for %v (err %v); want about 15 minutes", lockedFor, err)
	}
	if n := env.auditCount("LOGIN_LOCKED", emp.ID); n != 1 {
		t.Fatalf("LOGIN_LOCKED events = %d; want 1", n)
	}
	requireStatus(t, c.login(emp.Email, emp.Password), http.StatusUnauthorized, "invalid_credentials")

	// Once the lock has ended the right password works again and the counter is clear.
	env.exec("UPDATE employees SET locked_until = now() - interval '1 second' WHERE id = $1", emp.ID)
	requireStatus(t, c.login(emp.Email, emp.Password), http.StatusOK, "")
	var failed int
	_ = env.db.App.QueryRow(ctx, "SELECT failed_login_count FROM employees WHERE id = $1", emp.ID).Scan(&failed)
	if failed != 0 {
		t.Fatalf("failed_login_count = %d after success; want 0", failed)
	}
}

func TestConcurrentFailuresLockExactlyOnce(t *testing.T) {
	env := newEnv(t)
	emp := env.employee()

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			env.client().login(emp.Email, "wrong password!")
		}()
	}
	wg.Wait()

	if n := env.auditCount("LOGIN_LOCKED", emp.ID); n != 1 {
		t.Fatalf("LOGIN_LOCKED events = %d; want exactly 1", n)
	}
	if n := env.auditCount("LOGIN_FAILED", emp.ID); n != 10 {
		t.Fatalf("LOGIN_FAILED events = %d; want 10", n)
	}
}

func TestRequestsNeedAValidSession(t *testing.T) {
	env := newEnv(t)
	c := env.client()
	requireStatus(t, c.do(http.MethodGet, "/api/v1/probe", nil), http.StatusUnauthorized, "authentication_required")
	c.setCookie(sessionCookie, "not-a-token")
	requireStatus(t, c.do(http.MethodGet, "/api/v1/probe", nil), http.StatusUnauthorized, "authentication_required")

	// A well-signed token for an employee that does not exist.
	s, _ := newSession(uuid.New(), 0, time.Now())
	token, _ := signToken(testSecret, s)
	c.setCookie(sessionCookie, token)
	requireStatus(t, c.do(http.MethodGet, "/api/v1/probe", nil), http.StatusUnauthorized, "authentication_required")
}

func TestLogoutInvalidatesACopiedToken(t *testing.T) {
	env := newEnv(t)
	emp := env.employee()
	c := env.client()
	requireStatus(t, c.login(emp.Email, emp.Password), http.StatusOK, "")
	copied := c.cookie(sessionCookie)

	requireStatus(t, c.do(http.MethodPost, "/api/v1/auth/logout", nil), http.StatusNoContent, "")
	if c.cookie(sessionCookie) != "" {
		t.Error("session cookie still set after logout")
	}

	thief := env.client()
	thief.setCookie(sessionCookie, copied)
	requireStatus(t, thief.do(http.MethodGet, "/api/v1/probe", nil), http.StatusUnauthorized, "authentication_required")
	if n := env.auditCount("LOGOUT", emp.ID); n != 1 {
		t.Errorf("LOGOUT events = %d; want 1", n)
	}
}

func TestInactiveEmployeeIsRejectedDespiteAValidToken(t *testing.T) {
	for _, status := range []string{"SUSPENDED", "TERMINATED"} {
		t.Run(status, func(t *testing.T) {
			env := newEnv(t)
			emp := env.employee()
			c := env.client()
			requireStatus(t, c.login(emp.Email, emp.Password), http.StatusOK, "")
			requireStatus(t, c.do(http.MethodGet, "/api/v1/probe", nil), http.StatusOK, "")

			env.exec("UPDATE employees SET status = $2 WHERE id = $1", emp.ID, status)
			requireStatus(t, c.do(http.MethodGet, "/api/v1/probe", nil), http.StatusUnauthorized, "authentication_required")
		})
	}
}

func TestRemovedRoleTakesEffectOnTheNextRequest(t *testing.T) {
	env := newEnv(t)
	emp := env.employee("SUPER_ADMIN")
	other := env.employee("INFOSEC")
	c := env.client()
	requireStatus(t, c.login(emp.Email, emp.Password), http.StatusOK, "")
	requireStatus(t, c.do(http.MethodGet, "/api/v1/probe/admin", nil), http.StatusOK, "")

	env.exec("UPDATE role_assignments SET valid_to = now() WHERE employee_id = $1", emp.ID)
	requireStatus(t, c.do(http.MethodGet, "/api/v1/probe/admin", nil), http.StatusForbidden, "forbidden")

	o := env.client()
	requireStatus(t, o.login(other.Email, other.Password), http.StatusOK, "")
	requireStatus(t, o.do(http.MethodGet, "/api/v1/probe/admin", nil), http.StatusForbidden, "forbidden")
}

func TestUnsafeRequestsNeedTheCSRFHeader(t *testing.T) {
	env := newEnv(t)
	emp := env.employee()
	c := env.client()
	requireStatus(t, c.login(emp.Email, emp.Password), http.StatusOK, "")

	c.csrf = false
	requireStatus(t, c.do(http.MethodPost, "/api/v1/probe", nil), http.StatusForbidden, "csrf_failed")
	requireStatus(t, c.do(http.MethodPost, "/api/v1/auth/logout", nil), http.StatusForbidden, "csrf_failed")
	requireStatus(t, c.do(http.MethodGet, "/api/v1/probe", nil), http.StatusOK, "") // safe methods need no header

	req, _ := http.NewRequest(http.MethodPost, env.srv.URL+"/api/v1/probe", nil)
	req.Header.Set("X-CSRF-Token", "not-the-right-value")
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("mismatched CSRF header: status %d; want 403", resp.StatusCode)
	}

	c.csrf = true
	requireStatus(t, c.do(http.MethodPost, "/api/v1/probe", nil), http.StatusOK, "")
}

func TestTemporaryPasswordSessionMayOnlyChangeThePassword(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	emp := env.employee()
	expires := time.Now().Add(72 * time.Hour)
	env.setPassword(emp.ID, "Temporary1234567", true, &expires)
	c := env.client()

	r := c.login(emp.Email, "Temporary1234567")
	requireStatus(t, r, http.StatusOK, "")
	if r.body["mustChangePassword"] != true {
		t.Fatalf("mustChangePassword = %v; want true", r.body["mustChangePassword"])
	}
	requireStatus(t, c.do(http.MethodGet, "/api/v1/auth/me", nil), http.StatusOK, "")
	requireStatus(t, c.do(http.MethodGet, "/api/v1/probe", nil), http.StatusForbidden, "password_change_required")
	requireStatus(t, c.do(http.MethodPost, "/api/v1/probe", nil), http.StatusForbidden, "password_change_required")

	r = c.do(http.MethodPost, "/api/v1/auth/password", map[string]string{"currentPassword": "Temporary1234567", "newPassword": "a brand new password"})
	requireStatus(t, r, http.StatusOK, "")
	if r.body["mustChangePassword"] != false {
		t.Fatalf("after change mustChangePassword = %v; want false", r.body["mustChangePassword"])
	}
	requireStatus(t, c.do(http.MethodGet, "/api/v1/probe", nil), http.StatusOK, "")

	// "Single use": the temporary password is invalid after the change.
	requireStatus(t, env.client().login(emp.Email, "Temporary1234567"), http.StatusUnauthorized, "invalid_credentials")
	requireStatus(t, env.client().login(emp.Email, "a brand new password"), http.StatusOK, "")

	var temporary bool
	var expiresAt *time.Time
	_ = env.db.App.QueryRow(ctx, "SELECT is_temporary, expires_at FROM password_credentials WHERE employee_id = $1", emp.ID).Scan(&temporary, &expiresAt)
	if temporary || expiresAt != nil {
		t.Fatalf("credential temporary=%v expires=%v; want a permanent password", temporary, expiresAt)
	}
	if n := env.auditCount("PASSWORD_CHANGED", emp.ID); n != 1 {
		t.Fatalf("PASSWORD_CHANGED events = %d; want 1", n)
	}
}

func TestTemporaryPasswordIsValidUntilChangedOrExpired(t *testing.T) {
	env := newEnv(t)
	emp := env.employee()
	expires := time.Now().Add(72 * time.Hour)
	env.setPassword(emp.ID, "Temporary1234567", true, &expires)

	// Signing in does not use it up: a lost session can sign in again until the change.
	requireStatus(t, env.client().login(emp.Email, "Temporary1234567"), http.StatusOK, "")
	requireStatus(t, env.client().login(emp.Email, "Temporary1234567"), http.StatusOK, "")

	expired := time.Now().Add(-time.Second)
	env.setPassword(emp.ID, "Temporary1234567", true, &expired)
	requireStatus(t, env.client().login(emp.Email, "Temporary1234567"), http.StatusUnauthorized, "invalid_credentials")
}

func TestPasswordChange(t *testing.T) {
	env := newEnv(t)
	emp := env.employee()
	c := env.client()
	requireStatus(t, c.login(emp.Email, emp.Password), http.StatusOK, "")
	other := env.client() // the same person in another browser
	requireStatus(t, other.login(emp.Email, emp.Password), http.StatusOK, "")

	r := c.do(http.MethodPost, "/api/v1/auth/password", map[string]string{"currentPassword": "wrong password!", "newPassword": "a brand new password"})
	requireStatus(t, r, http.StatusBadRequest, "invalid_current_password")
	if r.body["field"] != "currentPassword" {
		t.Errorf("field = %v; want currentPassword", r.body["field"])
	}
	if n := env.auditCount("PASSWORD_CHANGE_FAILED", emp.ID); n != 1 {
		t.Errorf("PASSWORD_CHANGE_FAILED events = %d; want 1", n)
	}
	var failed int
	_ = env.db.App.QueryRow(context.Background(), "SELECT failed_login_count FROM employees WHERE id = $1", emp.ID).Scan(&failed)
	if failed != 1 {
		t.Errorf("failed_login_count = %d; want 1 (wrong current passwords count toward lockout)", failed)
	}

	r = c.do(http.MethodPost, "/api/v1/auth/password", map[string]string{"currentPassword": emp.Password, "newPassword": "too short"})
	requireStatus(t, r, http.StatusBadRequest, "password_length")
	// Otherwise a temporary password the issuer knows could become permanent (F4).
	r = c.do(http.MethodPost, "/api/v1/auth/password", map[string]string{"currentPassword": emp.Password, "newPassword": emp.Password})
	requireStatus(t, r, http.StatusBadRequest, "password_unchanged")
	if r.body["field"] != "newPassword" {
		t.Errorf("field = %v; want newPassword", r.body["field"])
	}

	requireStatus(t, c.do(http.MethodPost, "/api/v1/auth/password", map[string]string{"currentPassword": emp.Password, "newPassword": "a brand new password"}), http.StatusOK, "")
	requireStatus(t, c.do(http.MethodGet, "/api/v1/probe", nil), http.StatusOK, "")                                      // this browser stays signed in
	requireStatus(t, other.do(http.MethodGet, "/api/v1/probe", nil), http.StatusUnauthorized, "authentication_required") // others are signed out
	requireStatus(t, env.client().login(emp.Email, emp.Password), http.StatusUnauthorized, "invalid_credentials")
}

func TestRequestBodiesAreValidated(t *testing.T) {
	env := newEnv(t)
	c := env.client()
	req, _ := http.NewRequest(http.MethodPost, env.srv.URL+"/api/v1/auth/login", strings.NewReader(`{"email":`))
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed JSON: status %d; want 400", resp.StatusCode)
	}
	big := strings.Repeat("a", 70*1024)
	requireStatus(t, c.login("x@example.com", big), http.StatusBadRequest, "invalid_json")
}

func TestSessionIsRenewedOnlyWhenLessThanHalfIsLeft(t *testing.T) {
	env := newEnv(t)
	emp := env.employee()
	now := time.Now().UTC().Truncate(time.Second)

	cases := []struct {
		name         string
		loginAgo     time.Duration
		expiresIn    time.Duration
		wantRenewed  bool
		wantExpireIn time.Duration
	}{
		{"fresh token is not renewed", time.Minute, 29 * time.Minute, false, 0},
		{"token past half-life is renewed for 30 minutes", time.Hour, 10 * time.Minute, true, 30 * time.Minute},
		{"renewal never goes past 12 hours after login", 11*time.Hour + 50*time.Minute, 5 * time.Minute, true, 10 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Session{EmployeeID: emp.ID, CSRF: "csrf-value", LoginAt: now.Add(-tc.loginAgo),
				IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(tc.expiresIn)}
			token, _ := signToken(testSecret, s)
			c := env.client()
			c.setCookie(sessionCookie, token)
			r := c.do(http.MethodGet, "/api/v1/probe", nil)
			requireStatus(t, r, http.StatusOK, "")

			var renewed string
			for _, ck := range (&http.Response{Header: r.header}).Cookies() {
				if ck.Name == sessionCookie {
					renewed = ck.Value
				}
			}
			if (renewed != "") != tc.wantRenewed {
				t.Fatalf("renewed = %v; want %v", renewed != "", tc.wantRenewed)
			}
			if !tc.wantRenewed {
				return
			}
			got, err := parseToken(testSecret, renewed, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if !got.LoginAt.Equal(s.LoginAt) || got.CSRF != s.CSRF {
				t.Errorf("renewed login %v csrf %q; want the original %v %q", got.LoginAt, got.CSRF, s.LoginAt, s.CSRF)
			}
			if d := got.ExpiresAt.Sub(now) - tc.wantExpireIn; d < -2*time.Second || d > 2*time.Second {
				t.Errorf("renewed token expires in %v; want %v", got.ExpiresAt.Sub(now), tc.wantExpireIn)
			}
		})
	}

	// After 12 hours the token has expired and a new login is required.
	s := Session{EmployeeID: emp.ID, CSRF: "csrf-value", LoginAt: now.Add(-12*time.Hour - time.Minute),
		IssuedAt: now.Add(-31 * time.Minute), ExpiresAt: now.Add(-time.Minute)}
	token, _ := signToken(testSecret, s)
	c := env.client()
	c.setCookie(sessionCookie, token)
	requireStatus(t, c.do(http.MethodGet, "/api/v1/probe", nil), http.StatusUnauthorized, "authentication_required")
}

// The submitted email of an unknown-email failure is kept in the append-only audit log,
// so its size is capped there.
func TestFailedLoginAuditCapsTheSubmittedEmail(t *testing.T) {
	env := newEnv(t)
	requireStatus(t, env.client().login(strings.Repeat("a", 5000)+"@example.com", goodPassword), http.StatusUnauthorized, "invalid_credentials")
	var n int
	if err := env.db.App.QueryRow(context.Background(),
		"SELECT length(details->>'email') FROM audit_events WHERE action = 'LOGIN_FAILED'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 254 {
		t.Fatalf("recorded email length = %d; want 254", n)
	}
}

// Argon2 needs 19 MiB per hash; a burst of logins must queue instead of taking every
// database connection and unbounded memory.
func TestPasswordHashingIsLimited(t *testing.T) {
	env := newEnv(t)
	emp := env.employee()
	for range cap(env.svc.hashSlots) {
		env.svc.hashSlots <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := env.svc.login(ctx, emp.Email, emp.Password); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("login with all hash slots busy: %v; want it to wait and time out", err)
	}
	<-env.svc.hashSlots
	if _, err := env.svc.login(context.Background(), emp.Email, emp.Password); err != nil {
		t.Fatalf("login with a free slot: %v", err)
	}
}
