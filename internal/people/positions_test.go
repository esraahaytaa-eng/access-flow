package people

import (
	"net/http"
	"testing"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func TestCompanyCatalog(t *testing.T) {
	env := newEnv(t)
	admin, sa := env.user("SUPER_ADMIN")
	_, hr := env.user("HR_MANAGER")
	_, plain := env.user()

	r := hr.Do(http.MethodPost, "/api/v1/company-catalog/departments", map[string]string{"name": " Research "})
	testutil.RequireStatus(t, r, http.StatusCreated, "")
	if r.Body["name"] != "Research" || r.Body["active"] != true {
		t.Fatalf("department = %v", r.Body)
	}
	dept := r.Body["id"].(string)
	testutil.RequireStatus(t, sa.Do(http.MethodPost, "/api/v1/company-catalog/job-positions", map[string]string{"name": "Senior Backend Developer"}), http.StatusCreated, "")

	// Names differing only by case are one entry; the error names the defined spelling.
	r = sa.Do(http.MethodPost, "/api/v1/company-catalog/departments", map[string]string{"name": "research"})
	testutil.RequireStatus(t, r, http.StatusConflict, "name_taken")
	if r.Body["expected"] != "Research" {
		t.Fatalf("expected = %v; want the defined spelling", r.Body["expected"])
	}
	testutil.RequireStatus(t, sa.Do(http.MethodPost, "/api/v1/company-catalog/departments", map[string]string{"name": " "}), http.StatusBadRequest, "name_required")
	testutil.RequireStatus(t, sa.Do(http.MethodPost, "/api/v1/company-catalog/teams", map[string]string{"name": "X"}), http.StatusNotFound, "not_found")
	testutil.RequireStatus(t, plain.Do(http.MethodPost, "/api/v1/company-catalog/departments", map[string]string{"name": "Sales"}), http.StatusForbidden, "forbidden")

	// Everyone signed in reads the lists (the forms' pickers).
	r = plain.Do(http.MethodGet, "/api/v1/company-catalog", nil)
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if d := r.Body["departments"].([]any); len(d) != 3 || d[2].(map[string]any)["name"] != "Research" {
		t.Fatalf("departments = %v", r.Body["departments"])
	}
	if p := r.Body["jobPositions"].([]any); len(p) != 5 {
		t.Fatalf("job positions = %v", r.Body["jobPositions"])
	}

	// An employee holding the department keeps it after retirement; new use is refused.
	holder := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{})
	env.exec("UPDATE employees SET department = 'Research' WHERE id = $1", holder.ID)
	testutil.RequireStatus(t, plain.Do(http.MethodPost, "/api/v1/company-catalog/departments/"+dept+"/retire", nil), http.StatusForbidden, "forbidden")
	r = hr.Do(http.MethodPost, "/api/v1/company-catalog/departments/"+dept+"/retire", nil)
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Body["active"] != false {
		t.Fatalf("retired = %v", r.Body)
	}
	testutil.RequireStatus(t, hr.Do(http.MethodPost, "/api/v1/company-catalog/departments/"+dept+"/retire", nil), http.StatusOK, "")
	if n := env.count("SELECT count(*) FROM employees WHERE id = $1 AND department = 'Research'", holder.ID); n != 1 {
		t.Fatal("the holder lost the retired department")
	}
	r = hr.Do(http.MethodPost, "/api/v1/employees", map[string]any{"email": "new@x.test", "fullName": "New", "department": "Research"})
	testutil.RequireStatus(t, r, http.StatusBadRequest, "retired_department")

	for action, want := range map[string]int{"DEPARTMENT_CREATED": 1, "JOB_POSITION_CREATED": 1, "DEPARTMENT_RETIRED": 1} {
		if n := env.count("SELECT count(*) FROM audit_events WHERE action = $1", action); n != want {
			t.Errorf("%s events = %d; want %d", action, n, want)
		}
	}
	if n := env.count("SELECT count(*) FROM audit_events WHERE action = 'JOB_POSITION_CREATED' AND actor_id = $1", admin.ID); n != 1 {
		t.Errorf("job position event actor is not the Super Admin")
	}
}

// Every write that sets a department or job position checks the catalog; a case-only
// difference is refused with the defined spelling.
func TestEmployeePlacementUsesCatalog(t *testing.T) {
	env := newEnv(t)
	_, hr := env.user("HR_MANAGER")
	testutil.DefinePositions(t, env.db, "Backend Developer", "Lead Developer")

	cases := []struct {
		name, department, position, code, expected string
	}{
		{"unknown department", "Marketing", "Backend Developer", "unknown_department", ""},
		{"case-only department", "engineering", "Backend Developer", "unknown_department", "Engineering"},
		{"unknown position", "Engineering", "CTO", "unknown_job_position", ""},
		{"case-only position", "Engineering", "backend developer", "unknown_job_position", "Backend Developer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := hr.Do(http.MethodPost, "/api/v1/employees", map[string]any{"email": "e@x.test", "fullName": "E", "department": tc.department, "jobTitle": tc.position})
			testutil.RequireStatus(t, r, http.StatusBadRequest, tc.code)
			if got, _ := r.Body["expected"].(string); got != tc.expected {
				t.Fatalf("expected = %q; want %q", got, tc.expected)
			}
		})
	}
	r := hr.Do(http.MethodPost, "/api/v1/employees", map[string]any{"email": "e@x.test", "fullName": "E", "department": "Engineering", "jobTitle": "Backend Developer"})
	testutil.RequireStatus(t, r, http.StatusCreated, "")
	id := r.Body["employee"].(map[string]any)["id"].(string)

	env.exec("UPDATE job_positions SET active = false WHERE name = 'Lead Developer'")
	r = hr.Do(http.MethodPut, "/api/v1/employees/"+id, map[string]any{"version": 1, "department": "Engineering", "jobTitle": "Lead Developer", "managerId": nil})
	testutil.RequireStatus(t, r, http.StatusBadRequest, "retired_job_position")
}
