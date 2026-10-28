package people

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func TestHRCreatesAnEmployeeWhoMustChangeThePassword(t *testing.T) {
	env := newEnv(t)
	hr, c := env.user("HR_MANAGER")
	manager, _ := env.user()

	r := c.Do(http.MethodPost, "/api/v1/employees", map[string]any{
		"email": " New.Person@Example.com ", "fullName": " New Person ", "department": "Engineering", "jobTitle": "Developer",
		"managerId": manager.ID,
	})
	testutil.RequireStatus(t, r, http.StatusCreated, "")
	if cc := r.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q; want no-store", cc)
	}
	emp := r.Body["employee"].(map[string]any)
	if emp["email"] != "new.person@example.com" || emp["fullName"] != "New Person" || emp["department"] != "Engineering" ||
		emp["managerId"] != manager.ID.String() || emp["status"] != "ACTIVE" || emp["mustChangePassword"] != true {
		t.Fatalf("employee = %v", emp)
	}
	password, _ := r.Body["temporaryPassword"].(string)
	if len(password) != 16 {
		t.Fatalf("temporaryPassword = %q; want 16 characters", password)
	}
	id := uuid.MustParse(emp["id"].(string))

	var expires time.Time
	_ = env.db.App.QueryRow(context.Background(), "SELECT expires_at FROM password_credentials WHERE employee_id = $1", id).Scan(&expires)
	if d := time.Until(expires); d < 71*time.Hour || d > 73*time.Hour {
		t.Errorf("temporary password expires in %v; want 72h", d)
	}
	if n := env.count(`SELECT count(*) FROM audit_events WHERE action = 'EMPLOYEE_CREATED' AND actor_id = $1 AND subject_employee_id = $2
		AND details::text NOT LIKE '%' || $3 || '%'`, hr.ID, id, password); n != 1 {
		t.Errorf("EMPLOYEE_CREATED by HR without the password = %d; want 1", n)
	}

	nc := testutil.NewClient(t, env.srv)
	r = nc.Login("new.person@example.com", password)
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Body["mustChangePassword"] != true {
		t.Fatal("new employee is not asked to change the password")
	}
}

// Approved 2026-10-28 (F4): the bootstrap Super Admin sets up the organization alone.
func TestSuperAdminCreatesEmployees(t *testing.T) {
	env := newEnv(t)
	_, c := env.user("SUPER_ADMIN")
	r := c.Do(http.MethodPost, "/api/v1/employees", map[string]any{"email": "first.hr@example.com", "fullName": "First HR"})
	testutil.RequireStatus(t, r, http.StatusCreated, "")
}

func TestCreateEmployeeValidation(t *testing.T) {
	env := newEnv(t)
	_, c := env.user("HR_MANAGER")
	inactive := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{Status: "TERMINATED"})
	testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{Email: "taken@example.com"})

	cases := []struct {
		name        string
		body        map[string]any
		status      int
		code, field string
	}{
		{"invalid email", map[string]any{"email": "nobody", "fullName": "X"}, 400, "invalid_email", "email"},
		{"non-ASCII email", map[string]any{"email": "ayşe@example.com", "fullName": "X"}, 400, "invalid_email", "email"},
		{"blank name", map[string]any{"email": "a@example.com", "fullName": "  "}, 400, "full_name_required", "fullName"},
		{"long name", map[string]any{"email": "a@example.com", "fullName": strings.Repeat("n", 201)}, 400, "value_too_long", "fullName"},
		{"long department", map[string]any{"email": "a@example.com", "fullName": "X", "department": strings.Repeat("d", 101)}, 400, "value_too_long", "department"},
		{"long job title", map[string]any{"email": "a@example.com", "fullName": "X", "jobTitle": strings.Repeat("j", 101)}, 400, "value_too_long", "jobTitle"},
		{"unknown manager", map[string]any{"email": "a@example.com", "fullName": "X", "managerId": uuid.New()}, 400, "manager_not_found", "managerId"},
		{"inactive manager", map[string]any{"email": "a@example.com", "fullName": "X", "managerId": inactive.ID}, 400, "manager_inactive", "managerId"},
		{"NUL in name", map[string]any{"email": "a@example.com", "fullName": "A\u0000B"}, 400, "value_invalid", "fullName"},
		{"control character in department", map[string]any{"email": "a@example.com", "fullName": "X", "department": "Fin\u0007ance"}, 400, "value_invalid", "department"},
		{"newline in job title", map[string]any{"email": "a@example.com", "fullName": "X", "jobTitle": "Dev\nOps"}, 400, "value_invalid", "jobTitle"},
		{"email taken, other case", map[string]any{"email": "Taken@Example.com", "fullName": "X"}, 409, "email_taken", ""},
		{"unknown field", map[string]any{"email": "a@example.com", "fullName": "X", "role": "SUPER_ADMIN"}, 400, "invalid_json", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := c.Do(http.MethodPost, "/api/v1/employees", tc.body)
			testutil.RequireStatus(t, r, tc.status, tc.code)
			if tc.field != "" && r.Body["field"] != tc.field {
				t.Errorf("field = %v; want %s", r.Body["field"], tc.field)
			}
		})
	}
	if n := env.count("SELECT count(*) FROM employees WHERE email = 'a@example.com'"); n != 0 {
		t.Fatalf("an invalid request created an employee")
	}
}

func TestOnlyHRAndSuperAdminCreateEmployees(t *testing.T) {
	env := newEnv(t)
	for _, roles := range [][]string{nil, {"INFOSEC"}, {"AUDITOR"}} {
		_, c := env.user(roles...)
		r := c.Do(http.MethodPost, "/api/v1/employees", map[string]any{"email": "x@example.com", "fullName": "X"})
		testutil.RequireStatus(t, r, http.StatusForbidden, "forbidden")
	}
	if n := env.count("SELECT count(*) FROM employees WHERE email = 'x@example.com'"); n != 0 {
		t.Fatal("a forbidden request created an employee")
	}
}

func TestDirectoryVisibility(t *testing.T) {
	env := newEnv(t)
	suspended := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{Status: "SUSPENDED", Roles: []string{"AUDITOR"}})
	terminated := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{Status: "TERMINATED"})

	for _, role := range []string{"SUPER_ADMIN", "HR_MANAGER", "INFOSEC", "AUDITOR"} {
		t.Run(role, func(t *testing.T) {
			_, c := env.user(role)
			r := c.Do(http.MethodGet, "/api/v1/employees", nil)
			testutil.RequireStatus(t, r, http.StatusOK, "")
			byID := index(r)
			s, ok := byID[suspended.ID.String()]
			if !ok || s["status"] != "SUSPENDED" || s["version"] == nil || len(s["roles"].([]any)) != 1 {
				t.Errorf("suspended employee = %v; want full view with roles", s)
			}
			if _, ok := byID[terminated.ID.String()]; !ok {
				t.Error("terminated employee missing from the full directory")
			}
		})
	}

	t.Run("plain employee", func(t *testing.T) {
		me, c := env.user()
		r := c.Do(http.MethodGet, "/api/v1/employees", nil)
		testutil.RequireStatus(t, r, http.StatusOK, "")
		byID := index(r)
		if _, ok := byID[suspended.ID.String()]; ok {
			t.Error("a plain employee sees a suspended employee")
		}
		if _, ok := byID[terminated.ID.String()]; ok {
			t.Error("a plain employee sees a terminated employee")
		}
		self, ok := byID[me.ID.String()]
		if !ok {
			t.Fatal("own record missing")
		}
		want := map[string]bool{"id": true, "email": true, "fullName": true, "department": true, "jobTitle": true, "managerId": true}
		if len(self) != len(want) {
			t.Errorf("basic view keys = %v; want exactly %v", self, want)
		}
		for k := range self {
			if !want[k] {
				t.Errorf("basic view exposes %q", k)
			}
		}
	})
}

func index(r testutil.Response) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, e := range r.Body["employees"].([]any) {
		m := e.(map[string]any)
		out[m["id"].(string)] = m
	}
	return out
}
