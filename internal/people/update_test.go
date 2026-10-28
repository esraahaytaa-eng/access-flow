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
	manager := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{})

	r := put(c, emp.ID, map[string]any{"version": 1, "managerId": manager.ID, "department": "Finance", "jobTitle": nil})
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Body["version"] != float64(2) || r.Body["department"] != "Finance" || r.Body["managerId"] != manager.ID.String() {
		t.Fatalf("updated employee = %v", r.Body)
	}
	var details []byte
	_ = env.db.App.QueryRow(context.Background(), `SELECT details FROM audit_events
		WHERE action = 'EMPLOYEE_UPDATED' AND actor_id = $1 AND subject_employee_id = $2`, hr.ID, emp.ID).Scan(&details)
	var changes map[string]map[string]any
	if err := json.Unmarshal(details, &changes); err != nil {
		t.Fatalf("details %s: %v", details, err)
	}
	if len(changes) != 2 || changes["department"]["from"] != nil || changes["department"]["to"] != "Finance" ||
		changes["managerId"]["to"] != manager.ID.String() {
		t.Fatalf("EMPLOYEE_UPDATED details = %s; want only department and managerId with from/to", details)
	}

	// A job-title-only change is audited the same way (D5: audit only).
	r = put(c, emp.ID, map[string]any{"version": 2, "managerId": manager.ID, "department": "Finance", "jobTitle": "Analyst"})
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if n := env.audits("EMPLOYEE_UPDATED", emp.ID); n != 2 {
		t.Fatalf("EMPLOYEE_UPDATED events = %d; want 2", n)
	}

	// An unchanged body changes nothing.
	r = put(c, emp.ID, map[string]any{"version": 3, "managerId": manager.ID, "department": " Finance ", "jobTitle": "Analyst"})
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Body["version"] != float64(3) || env.audits("EMPLOYEE_UPDATED", emp.ID) != 2 {
		t.Fatalf("an unchanged update bumped the version or was audited: %v", r.Body)
	}
}

func TestUpdateEmployeeRejects(t *testing.T) {
	env := newEnv(t)
	_, c := env.user("SUPER_ADMIN")
	a := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{})
	b := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{})
	cc := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{})
	env.exec("UPDATE employees SET manager_id = $2 WHERE id = $1", a.ID, b.ID)
	env.exec("UPDATE employees SET manager_id = $2 WHERE id = $1", b.ID, cc.ID)
	terminated := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{Status: "TERMINATED"})
	suspended := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{Status: "SUSPENDED"})
	_, plain := env.user("AUDITOR", "INFOSEC")

	cases := []struct {
		name        string
		client      *testutil.Client
		id          uuid.UUID
		body        map[string]any
		status      int
		code, field string
	}{
		{"stale version", c, cc.ID, map[string]any{"version": 7, "department": "X"}, 409, "version_conflict", ""},
		{"unknown employee", c, uuid.New(), map[string]any{"version": 1}, 404, "not_found", ""},
		{"terminated employee", c, terminated.ID, map[string]any{"version": 1, "department": "X"}, 400, "employee_terminated", ""},
		{"own manager", c, cc.ID, map[string]any{"version": 1, "managerId": cc.ID}, 400, "manager_cycle", "managerId"},
		{"indirect cycle", c, cc.ID, map[string]any{"version": 1, "managerId": a.ID}, 400, "manager_cycle", "managerId"},
		{"inactive manager", c, cc.ID, map[string]any{"version": 1, "managerId": suspended.ID}, 400, "manager_inactive", "managerId"},
		{"unknown manager", c, cc.ID, map[string]any{"version": 1, "managerId": uuid.New()}, 400, "manager_not_found", "managerId"},
		{"long department", c, cc.ID, map[string]any{"version": 1, "department": string(make([]byte, 101))}, 400, "value_too_long", "department"},
		{"not HR or Super Admin", plain, cc.ID, map[string]any{"version": 1, "department": "X"}, 403, "forbidden", ""},
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

// Two opposite manager changes at once must not both pass the cycle check.
func TestConcurrentManagerChangesCannotCreateACycle(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	hr := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{Roles: []string{"HR_MANAGER"}})
	admin := auth.Principal{EmployeeID: hr.ID, Roles: []string{"HR_MANAGER"}}
	for round := 0; round < 10; round++ {
		a := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{})
		b := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{})
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, pair := range [][2]uuid.UUID{{a.ID, b.ID}, {b.ID, a.ID}} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				mgr := pair[1]
				_, errs[i] = env.svc.UpdateEmployee(ctx, admin, pair[0], EmployeeUpdate{Version: 1, ManagerID: &mgr})
			}()
		}
		wg.Wait()
		var cycles int
		for _, err := range errs {
			var e *platform.Error
			if errors.As(err, &e) && e.Code == "manager_cycle" {
				cycles++
			} else if err != nil {
				t.Fatalf("round %d: unexpected error %v", round, err)
			}
		}
		if cycles != 1 {
			t.Fatalf("round %d: manager_cycle errors = %d (%v); want exactly 1", round, cycles, errs)
		}
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
		for i, dept := range []string{"Finance", "Legal"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = env.svc.UpdateEmployee(ctx, actor, emp.ID, EmployeeUpdate{Version: 1, Department: &dept})
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
		body := map[string]any{"version": 1, "managerId": nil, "department": "Finance", "jobTitle": "Analyst"}
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
