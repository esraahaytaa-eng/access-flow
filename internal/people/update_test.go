package people

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func put(c *testutil.Client, id uuid.UUID, body map[string]any) testutil.Response {
	return c.Do(http.MethodPut, "/api/v1/employees/"+id.String(), body)
}

// edit sends a full PUT body: the fields not in body are null (cleared).
func edit(c *testutil.Client, id uuid.UUID, body map[string]any) testutil.Response {
	full := map[string]any{"managerId": nil, "department": nil, "jobTitle": nil}
	for k, v := range body {
		full[k] = v
	}
	return put(c, id, full)
}

func TestUpdateEmployee(t *testing.T) {
	env := newEnv(t)
	hr, c := env.user("HR_MANAGER")
	emp := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{})

	// A job-title-only change is audited only (D5).
	r := put(c, emp.ID, map[string]any{"version": 1, "managerId": nil, "department": nil, "jobTitle": "Analyst"})
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Body["version"] != float64(2) || r.Body["jobTitle"] != "Analyst" {
		t.Fatalf("updated employee = %v", r.Body)
	}
	var details []byte
	_ = env.db.App.QueryRow(context.Background(), `SELECT details FROM audit_events
		WHERE action = 'EMPLOYEE_UPDATED' AND actor_id = $1 AND subject_employee_id = $2`, hr.ID, emp.ID).Scan(&details)
	var changes map[string]map[string]any
	if err := json.Unmarshal(details, &changes); err != nil {
		t.Fatalf("details %s: %v", details, err)
	}
	if len(changes) != 1 || changes["jobTitle"]["from"] != nil || changes["jobTitle"]["to"] != "Analyst" {
		t.Fatalf("EMPLOYEE_UPDATED details = %s; want only jobTitle with from/to", details)
	}

	// An unchanged body changes nothing.
	r = put(c, emp.ID, map[string]any{"version": 2, "managerId": nil, "department": nil, "jobTitle": " Analyst "})
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Body["version"] != float64(2) || env.audits("EMPLOYEE_UPDATED", emp.ID) != 1 {
		t.Fatalf("an unchanged update bumped the version or was audited: %v", r.Body)
	}
}

// A department or manager change is a transfer (D5): PUT refuses it and changes nothing.
func TestUpdateRejectsTransfer(t *testing.T) {
	env := newEnv(t)
	_, c := env.user("HR_MANAGER")
	emp := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{})
	manager := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{})
	testutil.RequireStatus(t, edit(c, emp.ID, map[string]any{"version": 1, "managerId": manager.ID}), http.StatusConflict, "transfer_required")
	testutil.RequireStatus(t, edit(c, emp.ID, map[string]any{"version": 1, "department": "Finance", "jobTitle": "Analyst"}),
		http.StatusConflict, "transfer_required")
	if env.count("SELECT count(*) FROM employees WHERE id = $1 AND version = 1 AND manager_id IS NULL AND department IS NULL AND job_title IS NULL", emp.ID) != 1 ||
		env.audits("EMPLOYEE_UPDATED", emp.ID) != 0 {
		t.Fatal("a refused transfer changed the employee")
	}
}

func TestUpdateEmployeeRejects(t *testing.T) {
	env := newEnv(t)
	_, c := env.user("SUPER_ADMIN")
	cc := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{})
	terminated := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{Status: "TERMINATED"})
	_, plain := env.user("AUDITOR", "INFOSEC")

	cases := []struct {
		name        string
		client      *testutil.Client
		id          uuid.UUID
		body        map[string]any
		status      int
		code, field string
	}{
		{"stale version", c, cc.ID, map[string]any{"version": 7, "jobTitle": "X"}, 409, "version_conflict", ""},
		{"unknown employee", c, uuid.New(), map[string]any{"version": 1}, 404, "not_found", ""},
		{"terminated employee", c, terminated.ID, map[string]any{"version": 1, "jobTitle": "X"}, 400, "employee_terminated", ""},
		{"long job title", c, cc.ID, map[string]any{"version": 1, "jobTitle": string(make([]byte, 101))}, 400, "value_too_long", "jobTitle"},
		{"not HR or Super Admin", plain, cc.ID, map[string]any{"version": 1, "jobTitle": "X"}, 403, "forbidden", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := edit(tc.client, tc.id, tc.body)
			testutil.RequireStatus(t, r, tc.status, tc.code)
			if tc.field != "" && r.Body["field"] != tc.field {
				t.Errorf("field = %v; want %s", r.Body["field"], tc.field)
			}
		})
	}
	if n := env.count("SELECT count(*) FROM audit_events WHERE action = 'EMPLOYEE_UPDATED'"); n != 0 {
		t.Fatalf("rejected updates were audited: %d", n)
	}
}

// Nobody edits their own record, so nobody chooses who approves their requests (2026-10-28).
func TestNobodyEditsTheirOwnRecord(t *testing.T) {
	env := newEnv(t)
	manager := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{})
	for _, role := range []string{"HR_MANAGER", "SUPER_ADMIN"} {
		me, c := env.user(role)
		testutil.RequireStatus(t, edit(c, me.ID, map[string]any{"version": 1, "managerId": manager.ID}), http.StatusForbidden, "self_change_forbidden")
		testutil.RequireStatus(t, edit(c, me.ID, map[string]any{"version": 1, "jobTitle": "X"}), http.StatusForbidden, "self_change_forbidden")
	}
	if n := env.count("SELECT count(*) FROM audit_events WHERE action = 'EMPLOYEE_UPDATED'"); n != 0 {
		t.Fatalf("self-edits were audited: %d", n)
	}
}

// Review focus 2: two edits with the same version; exactly one wins.
func TestConcurrentEditsWithTheSameVersion(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	hr := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{Roles: []string{"HR_MANAGER"}})
	actor := auth.Principal{EmployeeID: hr.ID, Roles: []string{"HR_MANAGER"}}
	for round := 0; round < 10; round++ {
		emp := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{})
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, title := range []string{"Analyst", "Engineer"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = env.svc.UpdateEmployee(ctx, actor, emp.ID, EmployeeUpdate{Version: 1, JobTitle: &title})
			}()
		}
		wg.Wait()
		conflicts := 0
		for _, err := range errs {
			var e *platform.Error
			if errors.As(err, &e) && e.Code == "version_conflict" {
				conflicts++
			} else if err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
		}
		if conflicts != 1 || env.count("SELECT version FROM employees WHERE id = $1", emp.ID) != 2 {
			t.Fatalf("round %d: conflicts = %d (%v); want exactly one edit to win", round, conflicts, errs)
		}
	}
}

// An omitted field is an error, not a silent clear: a PUT must say what it wants.
func TestUpdateEmployeeRequiresEveryField(t *testing.T) {
	env := newEnv(t)
	_, c := env.user("HR_MANAGER")
	emp := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{})
	env.exec("UPDATE employees SET job_title = 'Analyst' WHERE id = $1", emp.ID)
	for _, missing := range []string{"version", "managerId", "department", "jobTitle"} {
		body := map[string]any{"version": 1, "managerId": nil, "department": nil, "jobTitle": "Engineer"}
		delete(body, missing)
		r := put(c, emp.ID, body)
		testutil.RequireStatus(t, r, http.StatusBadRequest, "field_required")
		if r.Body["field"] != missing {
			t.Errorf("field = %v; want %s", r.Body["field"], missing)
		}
	}
	if env.count("SELECT count(*) FROM employees WHERE id = $1 AND job_title = 'Analyst' AND version = 1", emp.ID) != 1 {
		t.Fatal("a rejected update changed the employee")
	}
}
