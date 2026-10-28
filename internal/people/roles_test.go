package people

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func TestCheckRoleChange(t *testing.T) {
	me, other := uuid.New(), uuid.New()
	sa := auth.Principal{EmployeeID: me, Roles: []string{"SUPER_ADMIN"}}
	cases := []struct {
		name   string
		actor  auth.Principal
		target uuid.UUID
		role   string
		status int
		code   string
	}{
		{"Super Admin assigns InfoSec", sa, other, "INFOSEC", 0, ""},
		{"Super Admin assigns HR Manager", sa, other, "HR_MANAGER", 0, ""},
		{"Super Admin assigns Auditor", sa, other, "AUDITOR", 0, ""},
		{"Super Admin is promoted only through D2 (M14)", sa, other, "SUPER_ADMIN", 400, "invalid_role"},
		{"unknown role", sa, other, "ADMIN", 400, "invalid_role"},
		{"no self change", sa, me, "AUDITOR", 403, "self_change_forbidden"},
		{"HR Manager may not", auth.Principal{EmployeeID: me, Roles: []string{"HR_MANAGER"}}, other, "AUDITOR", 403, "forbidden"},
		{"InfoSec may not", auth.Principal{EmployeeID: me, Roles: []string{"INFOSEC"}}, other, "AUDITOR", 403, "forbidden"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkRoleChange(tc.actor, tc.target, tc.role)
			if tc.code == "" {
				if err != nil {
					t.Fatalf("got %v; want allowed", err)
				}
				return
			}
			var e *platform.Error
			if !errors.As(err, &e) || e.Code != tc.code || e.Status != tc.status {
				t.Fatalf("got %v; want %d %s", err, tc.status, tc.code)
			}
		})
	}
}

func roles(c *testutil.Client, method string, id uuid.UUID, role string) testutil.Response {
	if method == http.MethodPost {
		return c.Do(method, "/api/v1/employees/"+id.String()+"/roles", map[string]string{"role": role})
	}
	return c.Do(method, "/api/v1/employees/"+id.String()+"/roles/"+role, nil)
}

func TestAssignAndRemoveRole(t *testing.T) {
	env := newEnv(t)
	admin, c := env.user("SUPER_ADMIN")
	target, tc := env.user()
	testutil.RequireStatus(t, tc.Do(http.MethodGet, "/api/v1/probe/infosec", nil), http.StatusForbidden, "forbidden")

	testutil.RequireStatus(t, roles(c, http.MethodPost, target.ID, "INFOSEC"), http.StatusNoContent, "")
	testutil.RequireStatus(t, tc.Do(http.MethodGet, "/api/v1/probe/infosec", nil), http.StatusOK, "") // next request
	testutil.RequireStatus(t, roles(c, http.MethodPost, target.ID, "INFOSEC"), http.StatusNoContent, "")
	if n := env.count("SELECT count(*) FROM role_assignments WHERE employee_id = $1", target.ID); n != 1 {
		t.Fatalf("role rows after a repeated assignment = %d; want 1", n)
	}
	if n := env.count(`SELECT count(*) FROM audit_events WHERE action = 'ROLE_ASSIGNED' AND actor_id = $1 AND subject_employee_id = $2
		AND details->>'role' = 'INFOSEC'`, admin.ID, target.ID); n != 1 {
		t.Fatalf("ROLE_ASSIGNED events = %d; want 1", n)
	}

	testutil.RequireStatus(t, roles(c, http.MethodDelete, target.ID, "INFOSEC"), http.StatusNoContent, "")
	testutil.RequireStatus(t, tc.Do(http.MethodGet, "/api/v1/probe/infosec", nil), http.StatusForbidden, "forbidden")
	testutil.RequireStatus(t, roles(c, http.MethodDelete, target.ID, "INFOSEC"), http.StatusNoContent, "")
	if n := env.count(`SELECT count(*) FROM role_assignments WHERE employee_id = $1 AND valid_to IS NOT NULL AND revoked_by = $2`,
		target.ID, admin.ID); n != 1 {
		t.Fatalf("closed role rows = %d; want 1 (kept, with revoked_by)", n)
	}
	if n := env.audits("ROLE_REMOVED", target.ID); n != 1 {
		t.Fatalf("ROLE_REMOVED events = %d; want 1", n)
	}
}

func TestRoleChangeRejects(t *testing.T) {
	env := newEnv(t)
	admin, c := env.user("SUPER_ADMIN")
	suspended := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{Status: "SUSPENDED"})
	_, hr := env.user("HR_MANAGER")
	target := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{})

	testutil.RequireStatus(t, roles(c, http.MethodPost, suspended.ID, "AUDITOR"), http.StatusBadRequest, "employee_not_active")
	testutil.RequireStatus(t, roles(c, http.MethodPost, admin.ID, "AUDITOR"), http.StatusForbidden, "self_change_forbidden")
	testutil.RequireStatus(t, roles(c, http.MethodDelete, admin.ID, "SUPER_ADMIN"), http.StatusForbidden, "self_change_forbidden")
	testutil.RequireStatus(t, roles(c, http.MethodPost, target.ID, "SUPER_ADMIN"), http.StatusBadRequest, "invalid_role")
	testutil.RequireStatus(t, roles(c, http.MethodDelete, target.ID, "SUPER_ADMIN"), http.StatusBadRequest, "invalid_role")
	testutil.RequireStatus(t, roles(hr, http.MethodPost, target.ID, "AUDITOR"), http.StatusForbidden, "forbidden")
	testutil.RequireStatus(t, roles(c, http.MethodPost, uuid.New(), "AUDITOR"), http.StatusNotFound, "not_found")
	if n := env.count("SELECT count(*) FROM audit_events WHERE action IN ('ROLE_ASSIGNED', 'ROLE_REMOVED')"); n != 0 {
		t.Fatalf("rejected role changes were audited: %d", n)
	}
}

// A role can be removed from an inactive employee (less access is always allowed).
func TestRoleCanBeRemovedFromAnInactiveEmployee(t *testing.T) {
	env := newEnv(t)
	_, c := env.user("SUPER_ADMIN")
	suspended := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{Status: "SUSPENDED", Roles: []string{"AUDITOR"}})
	testutil.RequireStatus(t, roles(c, http.MethodDelete, suspended.ID, "AUDITOR"), http.StatusNoContent, "")
	if n := env.count("SELECT count(*) FROM role_assignments WHERE employee_id = $1 AND valid_to IS NULL", suspended.ID); n != 0 {
		t.Fatal("role still open")
	}
}

func TestConcurrentRoleAssignmentCreatesOneRow(t *testing.T) {
	env := newEnv(t)
	admin := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{Roles: []string{"SUPER_ADMIN"}})
	actor := auth.Principal{EmployeeID: admin.ID, Roles: []string{"SUPER_ADMIN"}}
	target := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{})
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- env.svc.AssignRole(context.Background(), actor, target.ID, "HR_MANAGER")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent assignment: %v", err)
		}
	}
	if n := env.count("SELECT count(*) FROM role_assignments WHERE employee_id = $1", target.ID); n != 1 {
		t.Fatalf("role rows = %d; want 1", n)
	}
	if n := env.audits("ROLE_ASSIGNED", target.ID); n != 1 {
		t.Fatalf("ROLE_ASSIGNED events = %d; want 1", n)
	}
}

// A role or membership opened after the removing transaction began (a concurrent assign)
// must still close cleanly, never with valid_to before valid_from.
func TestRemovalClosesARowOpenedLater(t *testing.T) {
	env := newEnv(t)
	_, c := env.user("SUPER_ADMIN")
	target := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{})
	env.exec("INSERT INTO role_assignments (employee_id, role, valid_from) VALUES ($1, 'AUDITOR', now() + interval '1 second')", target.ID)
	testutil.RequireStatus(t, roles(c, http.MethodDelete, target.ID, "AUDITOR"), http.StatusNoContent, "")

	var group uuid.UUID
	_ = env.db.App.QueryRow(context.Background(), "INSERT INTO admin_groups (name) VALUES ('Ops') RETURNING id").Scan(&group)
	env.exec("INSERT INTO admin_group_memberships (group_id, employee_id, valid_from) VALUES ($1, $2, now() + interval '1 second')", group, target.ID)
	testutil.RequireStatus(t, members(c, http.MethodDelete, group, target.ID), http.StatusNoContent, "")
}
