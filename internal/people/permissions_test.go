package people

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// Every M3 endpoint for every role (approved 2026-10-28). The Auditor is strictly read-only;
// HR Managers and Super Admins administer employees; resets of InfoSec or Super Admin
// accounts need a Super Admin; roles and groups are Super Admin only.
func TestPermissionMatrix(t *testing.T) {
	env := newEnv(t)
	actors := []string{"employee", "HR_MANAGER", "INFOSEC", "AUDITOR", "SUPER_ADMIN"}
	clients := map[string]*testutil.Client{}
	for _, a := range actors {
		if a == "employee" {
			_, clients[a] = env.user()
		} else {
			_, clients[a] = env.user(a)
		}
	}
	newTarget := func(roles ...string) uuid.UUID {
		return testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{Roles: roles}).ID
	}
	newGroup := func() uuid.UUID {
		var id uuid.UUID
		if err := env.db.App.QueryRow(context.Background(), "INSERT INTO admin_groups (name) VALUES ($1) RETURNING id",
			"group-"+uuid.NewString()).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	const (
		ok, created, none, deny = http.StatusOK, http.StatusCreated, http.StatusNoContent, http.StatusForbidden
	)
	cases := []struct {
		name string
		do   func(c *testutil.Client) testutil.Response
		want []int // in the order of actors
	}{
		{"list employees", func(c *testutil.Client) testutil.Response {
			return c.Do(http.MethodGet, "/api/v1/employees", nil)
		}, []int{ok, ok, ok, ok, ok}},
		{"create employee", func(c *testutil.Client) testutil.Response {
			return c.Do(http.MethodPost, "/api/v1/employees", map[string]any{"email": uuid.NewString() + "@example.com", "fullName": "X"})
		}, []int{deny, created, deny, deny, created}},
		{"edit employee", func(c *testutil.Client) testutil.Response {
			return edit(c, newTarget(), map[string]any{"version": 1, "jobTitle": "Analyst"})
		}, []int{deny, ok, deny, deny, ok}},
		{"reset a plain employee", func(c *testutil.Client) testutil.Response {
			return c.Do(http.MethodPost, "/api/v1/employees/"+newTarget().String()+"/password-reset", nil)
		}, []int{deny, ok, deny, deny, ok}},
		{"reset an HR Manager", func(c *testutil.Client) testutil.Response {
			return c.Do(http.MethodPost, "/api/v1/employees/"+newTarget("HR_MANAGER").String()+"/password-reset", nil)
		}, []int{deny, ok, deny, deny, ok}},
		{"reset an InfoSec member (privileged)", func(c *testutil.Client) testutil.Response {
			return c.Do(http.MethodPost, "/api/v1/employees/"+newTarget("INFOSEC").String()+"/password-reset", nil)
		}, []int{deny, deny, deny, deny, ok}},
		{"reset another Super Admin (privileged)", func(c *testutil.Client) testutil.Response {
			return c.Do(http.MethodPost, "/api/v1/employees/"+newTarget("SUPER_ADMIN").String()+"/password-reset", nil)
		}, []int{deny, deny, deny, deny, ok}},
		{"assign a role", func(c *testutil.Client) testutil.Response {
			return roles(c, http.MethodPost, newTarget(), "AUDITOR")
		}, []int{deny, deny, deny, deny, none}},
		{"remove a role", func(c *testutil.Client) testutil.Response {
			return roles(c, http.MethodDelete, newTarget("AUDITOR"), "AUDITOR")
		}, []int{deny, deny, deny, deny, none}},
		{"list admin groups", func(c *testutil.Client) testutil.Response {
			return c.Do(http.MethodGet, "/api/v1/admin-groups", nil)
		}, []int{ok, ok, ok, ok, ok}},
		{"create an admin group", func(c *testutil.Client) testutil.Response {
			return c.Do(http.MethodPost, "/api/v1/admin-groups", map[string]string{"name": "g-" + uuid.NewString()})
		}, []int{deny, deny, deny, deny, created}},
		{"add a group member", func(c *testutil.Client) testutil.Response {
			return members(c, http.MethodPost, newGroup(), newTarget())
		}, []int{deny, deny, deny, deny, none}},
		{"remove a group member", func(c *testutil.Client) testutil.Response {
			g, emp := newGroup(), newTarget()
			env.exec("INSERT INTO admin_group_memberships (group_id, employee_id) VALUES ($1, $2)", g, emp)
			return members(c, http.MethodDelete, g, emp)
		}, []int{deny, deny, deny, deny, none}},
	}
	for _, tc := range cases {
		for i, a := range actors {
			t.Run(tc.name+"/"+a, func(t *testing.T) {
				r := tc.do(clients[a])
				if r.Status != tc.want[i] {
					t.Fatalf("status = %d (body %v); want %d", r.Status, r.Body, tc.want[i])
				}
			})
		}
	}
}
