package people

import (
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func TestCheckReset(t *testing.T) {
	me, other := uuid.New(), uuid.New()
	hr := auth.Principal{EmployeeID: me, Roles: []string{"HR_MANAGER"}}
	sa := auth.Principal{EmployeeID: me, Roles: []string{"SUPER_ADMIN"}}
	cases := []struct {
		name   string
		actor  auth.Principal
		target uuid.UUID
		roles  []string
		code   string // "" = allowed
	}{
		{"HR resets a plain employee", hr, other, nil, ""},
		{"HR resets another HR Manager", hr, other, []string{"HR_MANAGER"}, ""},
		{"HR resets an Auditor", hr, other, []string{"AUDITOR"}, ""},
		{"HR may not reset InfoSec", hr, other, []string{"INFOSEC"}, "privileged_reset_requires_super_admin"},
		{"HR may not reset a Super Admin", hr, other, []string{"AUDITOR", "SUPER_ADMIN"}, "privileged_reset_requires_super_admin"},
		{"Super Admin resets InfoSec", sa, other, []string{"INFOSEC"}, ""},
		{"Super Admin resets another Super Admin", sa, other, []string{"SUPER_ADMIN"}, ""},
		{"nobody resets themselves", sa, me, nil, "self_change_forbidden"},
		{"HR does not reset themselves", hr, me, []string{"HR_MANAGER"}, "self_change_forbidden"},
		{"InfoSec may not reset", auth.Principal{EmployeeID: me, Roles: []string{"INFOSEC"}}, other, nil, "forbidden"},
		{"Auditor may not reset", auth.Principal{EmployeeID: me, Roles: []string{"AUDITOR"}}, other, nil, "forbidden"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkReset(tc.actor, tc.target, tc.roles)
			if tc.code == "" {
				if err != nil {
					t.Fatalf("got %v; want allowed", err)
				}
				return
			}
			var e *platform.Error
			if !errors.As(err, &e) || e.Code != tc.code || e.Status != http.StatusForbidden {
				t.Fatalf("got %v; want 403 %s", err, tc.code)
			}
		})
	}
}

func TestResetPassword(t *testing.T) {
	env := newEnv(t)
	hr, c := env.user("HR_MANAGER")
	target, targetClient := env.user()
	env.exec("UPDATE employees SET locked_until = now() + interval '10 minutes' WHERE id = $1", target.ID)

	r := c.Do(http.MethodPost, "/api/v1/employees/"+target.ID.String()+"/password-reset", nil)
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if cc := r.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q; want no-store", cc)
	}
	password, _ := r.Body["temporaryPassword"].(string)
	if len(password) != 16 {
		t.Fatalf("temporaryPassword = %q", password)
	}

	testutil.RequireStatus(t, targetClient.Do(http.MethodGet, "/api/v1/auth/me", nil), http.StatusUnauthorized, "authentication_required")
	testutil.RequireStatus(t, testutil.NewClient(t, env.srv).Login(target.Email, testutil.Password), http.StatusUnauthorized, "invalid_credentials")
	r = testutil.NewClient(t, env.srv).Login(target.Email, password) // the lock is cleared too
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Body["mustChangePassword"] != true {
		t.Fatal("reset password is not temporary")
	}
	if n := env.count(`SELECT count(*) FROM audit_events WHERE action = 'PASSWORD_RESET' AND actor_id = $1 AND subject_employee_id = $2
		AND details::text NOT LIKE '%' || $3 || '%'`, hr.ID, target.ID, password); n != 1 {
		t.Fatalf("PASSWORD_RESET events without the password = %d; want 1", n)
	}
}

func TestResetPasswordRejects(t *testing.T) {
	env := newEnv(t)
	hr, c := env.user("HR_MANAGER")
	suspended := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{Status: "SUSPENDED", Password: true})
	infosec := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{Roles: []string{"INFOSEC"}, Password: true})
	cases := []struct {
		name   string
		id     uuid.UUID
		status int
		code   string
	}{
		{"suspended employee", suspended.ID, 400, "employee_not_active"},
		{"privileged target", infosec.ID, 403, "privileged_reset_requires_super_admin"},
		{"self", hr.ID, 403, "self_change_forbidden"},
		{"unknown", uuid.New(), 404, "not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testutil.RequireStatus(t, c.Do(http.MethodPost, "/api/v1/employees/"+tc.id.String()+"/password-reset", nil), tc.status, tc.code)
		})
	}
	if n := env.count("SELECT count(*) FROM audit_events WHERE action = 'PASSWORD_RESET'"); n != 0 {
		t.Fatalf("rejected resets were audited: %d", n)
	}
	testutil.RequireStatus(t, testutil.NewClient(t, env.srv).Login(infosec.Email, testutil.Password), http.StatusOK, "")
}
