package onboarding

import (
	"slices"
	"testing"

	"github.com/google/uuid"
)

var (
	adminID, goneID, otherID = uuid.New(), uuid.New(), uuid.New()
	jiraID, jiraUser         = uuid.New(), uuid.New()
	awsID, awsRead           = uuid.New(), uuid.New()
	groupID                  = uuid.New()
)

// fixture is a valid workbook against a company with an admin, a leaver, a Manual tool
// (Jira) and an API tool (AWS).
func fixture() (Workbook, Snapshot) {
	wb := Workbook{
		Employees: []EmployeeRow{
			{2, "ann@co.test", "Ann Lee", "Engineering", "Backend Developer", "bob@co.test"},
			{3, "bob@co.test", "Bob Ray", "Engineering", "", "admin@co.test"},
		},
		Tools: []ToolRow{
			{2, "GitHub", "Manual", "bob@co.test", "IT Operations", "Developer", "Manager + Tool Owner"},
			{3, "GitHub", "Manual", "bob@co.test", "IT Operations", "Admin", "Manager + Tool Owner + InfoSec"},
		},
		Access: []AccessRow{
			{2, "ann@co.test", "GitHub", "Developer", ""},
			{3, "ann@co.test", "Jira", "User", "ann.lee"},
			{4, "bob@co.test", "GitHub", "Admin", ""},
			{5, "ann@co.test", "AWS", "ReadOnly", ""},
		},
	}
	s := Snapshot{
		Departments: map[string]bool{"Engineering": true, "Old Dept": false},
		Positions:   map[string]bool{"Backend Developer": true},
		Groups:      map[string]uuid.UUID{"IT Operations": groupID},
		Employees: map[string]Employee{
			"admin@co.test": {ID: adminID, Email: "admin@co.test", FullName: "Admin", Status: "ACTIVE"},
			"gone@co.test":  {ID: goneID, Email: "gone@co.test", FullName: "Gone", Status: "TERMINATED"},
		},
		Tools: map[string]Tool{
			"jira": {ID: jiraID, Name: "Jira", Method: "MANUAL", OwnerEmail: "admin@co.test", AdminGroup: "IT Operations", Status: "ACTIVE",
				Levels: map[string]Level{"user": {jiraUser, "User", "Manager", true}}},
			"aws": {ID: awsID, Name: "AWS", Method: "API", OwnerEmail: "admin@co.test", AdminGroup: "IT Operations", Status: "ACTIVE",
				Levels: map[string]Level{"readonly": {awsRead, "ReadOnly", "Manager + InfoSec", true}}},
		},
		Imported: map[Key]Imported{},
		Held:     map[[2]uuid.UUID]bool{},
	}
	return wb, s
}

func TestValidWorkbook(t *testing.T) {
	wb, s := fixture()
	p := Validate(wb, s)
	if len(p.Problems) != 0 || len(p.Warnings) != 0 {
		t.Fatalf("problems = %v, warnings = %v", p.Problems, p.Warnings)
	}
	want := Summary{EmployeesNew: 2, ToolsNew: 1, LevelsNew: 2, AccessNew: 4}
	if p.Summary != want {
		t.Fatalf("summary = %+v; want %+v", p.Summary, want)
	}
	if p.employees[0].Email != "bob@co.test" || p.employees[1].Email != "ann@co.test" {
		t.Fatalf("order = %v; want the manager first", p.employees)
	}
	if !slices.Equal(p.Owners, []Owner{{"GitHub", "bob@co.test"}}) {
		t.Fatalf("owners = %v", p.Owners)
	}
	if len(p.tools) != 1 || len(p.tools[0].levels) != 2 || len(p.access) != 4 {
		t.Fatalf("plan = %+v", p)
	}
}

func TestProblems(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Workbook, *Snapshot)
		want   Problem // Value is not compared
	}{
		{"invalid email", func(w *Workbook, _ *Snapshot) { w.Employees[0].Email = "ann@" },
			Problem{Sheet: SheetEmployees, Row: 2, Column: "Email", Reason: "invalid_email"}},
		{"missing name", func(w *Workbook, _ *Snapshot) { w.Employees[0].FullName = "" },
			Problem{Sheet: SheetEmployees, Row: 2, Column: "Full Name", Reason: "field_required"}},
		{"department in another case", func(w *Workbook, _ *Snapshot) { w.Employees[0].Department = "engineering" },
			Problem{Sheet: SheetEmployees, Row: 2, Column: "Department", Reason: "unknown_department", Expected: "Engineering"}},
		{"retired department", func(w *Workbook, _ *Snapshot) { w.Employees[0].Department = "Old Dept" },
			Problem{Sheet: SheetEmployees, Row: 2, Column: "Department", Reason: "retired_department"}},
		{"unknown position", func(w *Workbook, _ *Snapshot) { w.Employees[0].JobPosition = "CTO" },
			Problem{Sheet: SheetEmployees, Row: 2, Column: "Job Position", Reason: "unknown_job_position"}},
		{"duplicate employee", func(w *Workbook, _ *Snapshot) {
			r := w.Employees[1]
			r.Row = 4
			w.Employees = append(w.Employees, r)
		}, Problem{Sheet: SheetEmployees, Row: 4, Column: "Email", Reason: "duplicate_employee", OtherRow: 3}},
		{"conflicting employee", func(w *Workbook, _ *Snapshot) {
			r := w.Employees[1]
			r.Row, r.FullName = 4, "Robert Ray"
			w.Employees = append(w.Employees, r)
		}, Problem{Sheet: SheetEmployees, Row: 4, Column: "Email", Reason: "conflicting_employee", OtherRow: 3}},
		{"self manager", func(w *Workbook, _ *Snapshot) { w.Employees[0].ManagerEmail = "ANN@co.test" },
			Problem{Sheet: SheetEmployees, Row: 2, Column: "Manager Email", Reason: "self_manager"}},
		{"unknown manager", func(w *Workbook, _ *Snapshot) { w.Employees[0].ManagerEmail = "zed@co.test" },
			Problem{Sheet: SheetEmployees, Row: 2, Column: "Manager Email", Reason: "unknown_manager"}},
		{"inactive manager", func(w *Workbook, _ *Snapshot) { w.Employees[0].ManagerEmail = "gone@co.test" },
			Problem{Sheet: SheetEmployees, Row: 2, Column: "Manager Email", Reason: "inactive_manager"}},
		{"manager cycle", func(w *Workbook, _ *Snapshot) { w.Employees[1].ManagerEmail = "ann@co.test" },
			Problem{Sheet: SheetEmployees, Row: 2, Column: "Manager Email", Reason: "manager_cycle"}},
		{"three-person cycle", func(w *Workbook, _ *Snapshot) {
			w.Employees[1].ManagerEmail = "cem@co.test"
			w.Employees = append(w.Employees, EmployeeRow{4, "cem@co.test", "Cem", "", "", "ann@co.test"})
		}, Problem{Sheet: SheetEmployees, Row: 4, Column: "Manager Email", Reason: "manager_cycle"}},
		{"sample employee", func(w *Workbook, _ *Snapshot) { w.Employees[0].Email = "example.employee" + sampleDomain },
			Problem{Sheet: SheetEmployees, Row: 2, Column: "Email", Reason: "sample_row"}},
		{"unknown method", func(w *Workbook, _ *Snapshot) { w.Tools[0].Method, w.Tools[1].Method = "Script", "Script" },
			Problem{Sheet: SheetTools, Row: 2, Column: "Fulfillment Method", Reason: "invalid_fulfillment_method", Expected: "Manual, Vendor, API"}},
		{"unknown steps", func(w *Workbook, _ *Snapshot) { w.Tools[0].Steps = "Manager + HR" },
			Problem{Sheet: SheetTools, Row: 2, Column: "Approval Steps", Reason: "invalid_approval_steps",
				Expected: "Manager, Manager + Tool Owner, Manager + InfoSec, Manager + Tool Owner + InfoSec"}},
		{"group in another case", func(w *Workbook, _ *Snapshot) {
			w.Tools[0].AdminGroup, w.Tools[1].AdminGroup = "it operations", "it operations"
		},
			Problem{Sheet: SheetTools, Row: 2, Column: "Admin Group", Reason: "unknown_admin_group", Expected: "IT Operations"}},
		{"unknown owner", func(w *Workbook, _ *Snapshot) {
			w.Tools[0].OwnerEmail, w.Tools[1].OwnerEmail = "zed@co.test", "zed@co.test"
		},
			Problem{Sheet: SheetTools, Row: 2, Column: "Tool Owner Email", Reason: "unknown_owner"}},
		{"inactive owner", func(w *Workbook, _ *Snapshot) {
			w.Tools[0].OwnerEmail, w.Tools[1].OwnerEmail = "gone@co.test", "gone@co.test"
		},
			Problem{Sheet: SheetTools, Row: 2, Column: "Tool Owner Email", Reason: "inactive_owner"}},
		{"conflicting tool rows", func(w *Workbook, _ *Snapshot) { w.Tools[1].Method = "Vendor" },
			Problem{Sheet: SheetTools, Row: 3, Column: "Fulfillment Method", Reason: "conflicting_tool", Expected: "Manual", OtherRow: 2}},
		{"duplicate level", func(w *Workbook, _ *Snapshot) { w.Tools[1].Level = "developer" },
			Problem{Sheet: SheetTools, Row: 3, Column: "Access Level", Reason: "duplicate_level", OtherRow: 2}},
		{"new tool named like an existing one", func(w *Workbook, _ *Snapshot) {
			w.Tools = append(w.Tools, ToolRow{4, "jira", "Manual", "admin@co.test", "IT Operations", "User", "Manager"})
		}, Problem{Sheet: SheetTools, Row: 4, Column: "Tool", Reason: "unknown_tool", Expected: "Jira"}},
		{"retired tool", func(w *Workbook, s *Snapshot) {
			jira := s.Tools["jira"]
			jira.Status = "RETIRED"
			s.Tools["jira"] = jira
			w.Tools = append(w.Tools, ToolRow{4, "Jira", "Manual", "admin@co.test", "IT Operations", "User", "Manager"})
		}, Problem{Sheet: SheetTools, Row: 4, Column: "Tool", Reason: "retired_tool"}},
		{"sample tool", func(w *Workbook, _ *Snapshot) { w.Tools[0].Tool, w.Tools[1].Tool = SampleTool, SampleTool },
			Problem{Sheet: SheetTools, Row: 2, Column: "Tool", Reason: "sample_row"}},
		{"missing level", func(w *Workbook, _ *Snapshot) { w.Tools[0].Level = "" },
			Problem{Sheet: SheetTools, Row: 2, Column: "Access Level", Reason: "field_required"}},
		{"access of an unknown employee", func(w *Workbook, _ *Snapshot) { w.Access[0].EmployeeEmail = "zed@co.test" },
			Problem{Sheet: SheetAccess, Row: 2, Column: "Employee Email", Reason: "unknown_employee"}},
		{"access to an unknown tool", func(w *Workbook, _ *Snapshot) { w.Access[0].Tool = "Slack" },
			Problem{Sheet: SheetAccess, Row: 2, Column: "Tool", Reason: "unknown_tool"}},
		{"access to a tool in another case", func(w *Workbook, _ *Snapshot) { w.Access[0].Tool = "github" },
			Problem{Sheet: SheetAccess, Row: 2, Column: "Tool", Reason: "unknown_tool", Expected: "GitHub"}},
		{"level the tool lacks", func(w *Workbook, _ *Snapshot) { w.Access[0].Level = "Owner" },
			Problem{Sheet: SheetAccess, Row: 2, Column: "Access Level", Reason: "unknown_level", Expected: "Admin, Developer"}},
		{"level of an existing tool", func(w *Workbook, _ *Snapshot) { w.Access[1].Level = "Admin" },
			Problem{Sheet: SheetAccess, Row: 3, Column: "Access Level", Reason: "unknown_level", Expected: "User"}},
		{"API account of someone else", func(w *Workbook, _ *Snapshot) { w.Access[3].Account = "bob@co.test" },
			Problem{Sheet: SheetAccess, Row: 5, Column: "Account", Reason: "api_account_mismatch", Expected: "ann@co.test"}},
		{"access to a tool whose accounts are GitHub usernames", func(_ *Workbook, s *Snapshot) {
			aws := s.Tools["aws"]
			aws.AccountKind = "GITHUB_LOGIN" // requested with the username and its GitHub id, never imported
			s.Tools["aws"] = aws
		}, Problem{Sheet: SheetAccess, Row: 5, Column: "Tool", Reason: "account_kind_tool"}},
		{"duplicate access", func(w *Workbook, _ *Snapshot) {
			w.Access = append(w.Access, AccessRow{6, "ann@co.test", "GitHub", "Developer", "ANN@co.test"})
		},
			Problem{Sheet: SheetAccess, Row: 6, Column: "Account", Reason: "duplicate_access", OtherRow: 2}},
		{"same access under a second account", func(w *Workbook, _ *Snapshot) {
			w.Access = append(w.Access, AccessRow{6, "ann@co.test", "Jira", "User", "ann.other"})
		},
			Problem{Sheet: SheetAccess, Row: 6, Column: "Account", Reason: "duplicate_access", OtherRow: 3}},
		{"one account for two employees", func(w *Workbook, _ *Snapshot) {
			w.Access = append(w.Access, AccessRow{6, "bob@co.test", "GitHub", "Developer", "ann@co.test"})
		},
			Problem{Sheet: SheetAccess, Row: 6, Column: "Account", Reason: "conflicting_access", OtherRow: 2}},
		{"sample access", func(w *Workbook, _ *Snapshot) { w.Access[0].EmployeeEmail = "example.employee" + sampleDomain },
			Problem{Sheet: SheetAccess, Row: 2, Column: "Employee Email", Reason: "sample_row"}},
		{"empty workbook", func(w *Workbook, _ *Snapshot) { *w = Workbook{} },
			Problem{Reason: "empty_workbook"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wb, s := fixture()
			tc.change(&wb, &s)
			p := Validate(wb, s)
			for _, got := range p.Problems {
				got.Value = ""
				if got == tc.want {
					return
				}
			}
			t.Fatalf("problems = %+v; want %+v", p.Problems, tc.want)
		})
	}
}

// What AccessFlow already holds is never overwritten: it is skipped with a warning.
func TestWarnings(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Workbook, *Snapshot)
		want   Problem
	}{
		{"existing employee differs", func(w *Workbook, _ *Snapshot) {
			w.Employees = append(w.Employees, EmployeeRow{4, "admin@co.test", "Admin", "Engineering", "", ""})
		}, Problem{Sheet: SheetEmployees, Row: 4, Column: "Department", Reason: "employee_differs"}},
		{"existing employee not active", func(w *Workbook, _ *Snapshot) {
			w.Employees = append(w.Employees, EmployeeRow{4, "gone@co.test", "Gone", "", "", ""})
		}, Problem{Sheet: SheetEmployees, Row: 4, Column: "Email", Reason: "employee_not_active", Expected: "TERMINATED"}},
		{"existing tool differs", func(w *Workbook, _ *Snapshot) {
			w.Tools = append(w.Tools, ToolRow{4, "Jira", "Manual", "bob@co.test", "IT Operations", "User", "Manager"})
		}, Problem{Sheet: SheetTools, Row: 4, Column: "Tool Owner Email", Reason: "tool_differs", Expected: "admin@co.test"}},
		{"existing level differs", func(w *Workbook, _ *Snapshot) {
			w.Tools = append(w.Tools, ToolRow{4, "Jira", "Manual", "admin@co.test", "IT Operations", "User", "Manager + InfoSec"})
		}, Problem{Sheet: SheetTools, Row: 4, Column: "Approval Steps", Reason: "level_differs", Expected: "Manager"}},
		{"access imported for someone else", func(_ *Workbook, s *Snapshot) {
			s.Imported[Key{jiraID, jiraUser, "ann.lee"}] = Imported{Employee: &otherID, Open: true}
		}, Problem{Sheet: SheetAccess, Row: 3, Column: "Account", Reason: "held_by_other"}},
		{"access removed since", func(_ *Workbook, s *Snapshot) {
			s.Imported[Key{jiraID, jiraUser, "ann.lee"}] = Imported{Open: false}
		}, Problem{Sheet: SheetAccess, Row: 3, Column: "Account", Reason: "previously_removed"}},
		{"access of a terminated employee", func(w *Workbook, _ *Snapshot) {
			w.Access = append(w.Access, AccessRow{6, "gone@co.test", "Jira", "User", ""})
		}, Problem{Sheet: SheetAccess, Row: 6, Column: "Employee Email", Reason: "employee_terminated"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wb, s := fixture()
			tc.change(&wb, &s)
			p := Validate(wb, s)
			if len(p.Problems) != 0 {
				t.Fatalf("problems = %+v; want none", p.Problems)
			}
			for _, got := range p.Warnings {
				got.Value = ""
				if got == tc.want {
					return
				}
			}
			t.Fatalf("warnings = %+v; want %+v", p.Warnings, tc.want)
		})
	}
}

// Re-importing skips what exists: existing employees, tools, levels and access are counted,
// not created again.
func TestReimportCountsExisting(t *testing.T) {
	wb, s := fixture()
	s.Employees["ann@co.test"] = Employee{ID: uuid.New(), Email: "ann@co.test", FullName: "Ann Lee", Department: "Engineering",
		JobTitle: "Backend Developer", ManagerEmail: "bob@co.test", Status: "ACTIVE"}
	ann := s.Employees["ann@co.test"].ID
	s.Held[[2]uuid.UUID{ann, jiraUser}] = true
	s.Imported[Key{awsID, awsRead, "ann@co.test"}] = Imported{Employee: &ann, Open: true}
	p := Validate(wb, s)
	if len(p.Problems) != 0 || len(p.Warnings) != 0 {
		t.Fatalf("problems = %v, warnings = %v", p.Problems, p.Warnings)
	}
	want := Summary{EmployeesNew: 1, EmployeesExisting: 1, ToolsNew: 1, LevelsNew: 2, AccessNew: 2, AccessExisting: 2}
	if p.Summary != want {
		t.Fatalf("summary = %+v; want %+v", p.Summary, want)
	}
}

// A bad tool row is reported once, at its own row: its other rows and the access rows that
// name it add nothing.
func TestToolProblemReportedOnce(t *testing.T) {
	wb, s := fixture()
	wb.Tools[0].Method, wb.Tools[1].Method = "Script", "Script"
	if p := Validate(wb, s); len(p.Problems) != 1 {
		t.Fatalf("problems = %+v; want one", p.Problems)
	}
}

// A manager email that is invalid or the employee's own is one problem, not also "unknown".
func TestManagerProblemReportedOnce(t *testing.T) {
	for _, manager := range []string{"ANN@co.test", "not an email"} {
		wb, s := fixture()
		wb.Employees[0].ManagerEmail = manager
		if p := Validate(wb, s); len(p.Problems) != 1 {
			t.Errorf("manager %q: problems = %+v; want one", manager, p.Problems)
		}
	}
}
