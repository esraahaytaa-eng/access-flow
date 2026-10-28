package people

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func members(c *testutil.Client, method string, group, emp uuid.UUID) testutil.Response {
	path := "/api/v1/admin-groups/" + group.String() + "/members"
	if method == http.MethodPost {
		return c.Do(method, path, map[string]any{"employeeId": emp})
	}
	return c.Do(method, path+"/"+emp.String(), nil)
}

func TestAdminGroups(t *testing.T) {
	env := newEnv(t)
	admin, c := env.user("SUPER_ADMIN")
	target, tc := env.user()

	r := c.Do(http.MethodPost, "/api/v1/admin-groups", map[string]string{"name": " Finance IT "})
	testutil.RequireStatus(t, r, http.StatusCreated, "")
	if r.Body["name"] != "Finance IT" {
		t.Fatalf("group = %v", r.Body)
	}
	group := uuid.MustParse(r.Body["id"].(string))
	testutil.RequireStatus(t, c.Do(http.MethodPost, "/api/v1/admin-groups", map[string]string{"name": "finance it"}), http.StatusConflict, "group_name_taken")
	testutil.RequireStatus(t, c.Do(http.MethodPost, "/api/v1/admin-groups", map[string]string{"name": " "}), http.StatusBadRequest, "name_required")
	testutil.RequireStatus(t, c.Do(http.MethodPost, "/api/v1/admin-groups", map[string]string{"name": strings.Repeat("g", 101)}), http.StatusBadRequest, "value_too_long")
	testutil.RequireStatus(t, c.Do(http.MethodPost, "/api/v1/admin-groups", map[string]string{"name": "Ops\u0000"}), http.StatusBadRequest, "value_invalid")
	if n := env.count("SELECT count(*) FROM audit_events WHERE action = 'ADMIN_GROUP_CREATED' AND actor_id = $1", admin.ID); n != 1 {
		t.Fatalf("ADMIN_GROUP_CREATED events = %d; want 1", n)
	}

	empty := c.Do(http.MethodGet, "/api/v1/admin-groups", nil).Body["groups"].([]any)[0].(map[string]any)
	if ids, ok := empty["memberIds"].([]any); !ok || len(ids) != 0 {
		t.Fatalf("empty group for a Super Admin = %v; want memberIds: []", empty)
	}
	testutil.RequireStatus(t, members(c, http.MethodPost, group, target.ID), http.StatusNoContent, "")
	testutil.RequireStatus(t, members(c, http.MethodPost, group, target.ID), http.StatusNoContent, "")
	me := tc.Do(http.MethodGet, "/api/v1/auth/me", nil) // next request
	if gs, _ := me.Body["adminGroups"].([]any); len(gs) != 1 {
		t.Fatalf("member's adminGroups = %v; want the group", me.Body["adminGroups"])
	}
	if n := env.audits("ADMIN_GROUP_MEMBER_ADDED", target.ID); n != 1 {
		t.Fatalf("ADMIN_GROUP_MEMBER_ADDED events = %d; want 1", n)
	}

	list := c.Do(http.MethodGet, "/api/v1/admin-groups", nil)
	testutil.RequireStatus(t, list, http.StatusOK, "")
	g := list.Body["groups"].([]any)[0].(map[string]any)
	if ids, _ := g["memberIds"].([]any); len(ids) != 1 || ids[0] != target.ID.String() {
		t.Fatalf("group in list = %v; want the member", g)
	}
	// Without a directory role the list shows names only.
	plain := tc.Do(http.MethodGet, "/api/v1/admin-groups", nil)
	if _, ok := plain.Body["groups"].([]any)[0].(map[string]any)["memberIds"]; ok {
		t.Fatal("a plain employee sees group members")
	}
	full := c.Do(http.MethodGet, "/api/v1/employees", nil)
	if gs := index(full)[target.ID.String()]["adminGroups"].([]any); len(gs) != 1 {
		t.Fatalf("directory adminGroups = %v", gs)
	}

	testutil.RequireStatus(t, members(c, http.MethodDelete, group, target.ID), http.StatusNoContent, "")
	testutil.RequireStatus(t, members(c, http.MethodDelete, group, target.ID), http.StatusNoContent, "")
	me = tc.Do(http.MethodGet, "/api/v1/auth/me", nil)
	if gs, _ := me.Body["adminGroups"].([]any); len(gs) != 0 {
		t.Fatalf("adminGroups after removal = %v", me.Body["adminGroups"])
	}
	if n := env.count("SELECT count(*) FROM admin_group_memberships WHERE employee_id = $1 AND valid_to IS NOT NULL AND revoked_by = $2", target.ID, admin.ID); n != 1 {
		t.Fatalf("closed memberships = %d; want 1, kept", n)
	}
	if n := env.audits("ADMIN_GROUP_MEMBER_REMOVED", target.ID); n != 1 {
		t.Fatalf("ADMIN_GROUP_MEMBER_REMOVED events = %d; want 1", n)
	}
}

func TestAdminGroupRejects(t *testing.T) {
	env := newEnv(t)
	admin, c := env.user("SUPER_ADMIN")
	_, hr := env.user("HR_MANAGER")
	target := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{})
	suspended := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{Status: "SUSPENDED"})
	group := uuid.MustParse(c.Do(http.MethodPost, "/api/v1/admin-groups", map[string]string{"name": "Ops"}).Body["id"].(string))

	testutil.RequireStatus(t, hr.Do(http.MethodPost, "/api/v1/admin-groups", map[string]string{"name": "Other"}), http.StatusForbidden, "forbidden")
	testutil.RequireStatus(t, members(hr, http.MethodPost, group, target.ID), http.StatusForbidden, "forbidden")
	testutil.RequireStatus(t, members(c, http.MethodPost, group, admin.ID), http.StatusForbidden, "self_change_forbidden")
	testutil.RequireStatus(t, members(c, http.MethodPost, group, suspended.ID), http.StatusBadRequest, "employee_not_active")
	testutil.RequireStatus(t, members(c, http.MethodPost, uuid.New(), target.ID), http.StatusNotFound, "not_found")
	testutil.RequireStatus(t, members(c, http.MethodPost, group, uuid.New()), http.StatusNotFound, "not_found")
	if n := env.count("SELECT count(*) FROM audit_events WHERE action LIKE 'ADMIN_GROUP_MEMBER%'"); n != 0 {
		t.Fatalf("rejected membership changes were audited: %d", n)
	}
}
