// Package onboarding imports a company's employees, tools and existing access from one Excel
// workbook (M17): validated as a whole, applied in one transaction, never overwriting what
// AccessFlow already holds.
package onboarding

import (
	"maps"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// Sheet names and their exact headers.
const (
	SheetEmployees = "Employees"
	SheetTools     = "Tools"
	SheetAccess    = "Existing Access"
)

// Problem is a reason the workbook cannot be imported, or (as a warning) something the
// import will skip. Row is the worksheet row; OtherRow names the row it conflicts with.
type Problem struct {
	Sheet    string `json:"sheet,omitempty"`
	Row      int    `json:"row,omitempty"`
	Column   string `json:"column,omitempty"`
	Value    string `json:"value,omitempty"`
	Reason   string `json:"reason"`
	Expected string `json:"expected,omitempty"` // the configured value it should name
	OtherRow int    `json:"otherRow,omitempty"`
}

// EmployeeRow, ToolRow and AccessRow are the parsed, trimmed rows.
type EmployeeRow struct {
	Row                                                    int
	Email, FullName, Department, JobPosition, ManagerEmail string
}

type ToolRow struct {
	Row                                                int
	Tool, Method, OwnerEmail, AdminGroup, Level, Steps string
}

type AccessRow struct {
	Row                                 int
	EmployeeEmail, Tool, Level, Account string
}

// Workbook is the parsed workbook.
type Workbook struct {
	Employees []EmployeeRow
	Tools     []ToolRow
	Access    []AccessRow
}

// Snapshot is what AccessFlow holds, read in the import's transaction.
type Snapshot struct {
	Departments, Positions map[string]bool // name -> active
	Groups                 map[string]uuid.UUID
	Employees              map[string]Employee // by lowercase email
	Tools                  map[string]Tool     // by lowercase name
	Imported               map[Key]Imported    // baseline grants, by (tool, account, level)
	Held                   map[[2]uuid.UUID]bool
}

// Employee is an existing employee; ManagerEmail is empty without a manager.
type Employee struct {
	ID                                                          uuid.UUID
	Email, FullName, Department, JobTitle, ManagerEmail, Status string
}

// Tool is an existing tool with its levels by lowercase name.
type Tool struct {
	ID                                           uuid.UUID
	Name, Method, OwnerEmail, AdminGroup, Status string
	AccountKind                                  string // set: accounts are not emails (GITHUB_LOGIN)
	Levels                                       map[string]Level
}

type Level struct {
	ID          uuid.UUID
	Name, Steps string // Steps as written in the workbook, e.g. "Manager + Tool Owner"
	Active      bool
}

// Key identifies an imported baseline grant (D10: imported once).
type Key struct {
	Tool, Level uuid.UUID
	Account     string // lowercase
}

// Imported is an existing baseline grant: its holder (nil for an orphan) and whether it is open.
type Imported struct {
	Employee *uuid.UUID
	Open     bool
}

// Methods and Steps are the workbook spellings and their stored values.
var (
	Methods = map[string]string{"Manual": "MANUAL", "Vendor": "VENDOR", "API": "API"}
	Steps   = map[string][2]bool{ // requires tool owner, requires InfoSec
		"Manager": {false, false}, "Manager + Tool Owner": {true, false},
		"Manager + InfoSec": {false, true}, "Manager + Tool Owner + InfoSec": {true, true},
	}
	StepNames = []string{"Manager", "Manager + Tool Owner", "Manager + InfoSec", "Manager + Tool Owner + InfoSec"}
)

// StepsName is the workbook spelling of a level's approval steps.
func StepsName(toolOwner, infosec bool) string {
	for _, n := range StepNames {
		if Steps[n] == [2]bool{toolOwner, infosec} {
			return n
		}
	}
	return ""
}

// Plan is what the import will do and why it cannot, when Problems is not empty.
type Plan struct {
	Summary  Summary   `json:"summary"`
	Owners   []Owner   `json:"owners"` // tool owners the import assigns
	Problems []Problem `json:"problems"`
	Warnings []Problem `json:"warnings"`

	employees []EmployeeRow // to create, managers first
	tools     []newTool     // to create, with their levels
	levels    []ToolRow     // new levels of existing tools
	access    []AccessRow   // every valid access row; the D10 code skips what exists
}

type newTool struct {
	ToolRow
	levels []ToolRow
}

// Summary counts what the import creates and skips.
type Summary struct {
	EmployeesNew      int `json:"employeesNew"`
	EmployeesExisting int `json:"employeesExisting"`
	ToolsNew          int `json:"toolsNew"`
	ToolsExisting     int `json:"toolsExisting"`
	LevelsNew         int `json:"levelsNew"`
	LevelsExisting    int `json:"levelsExisting"`
	AccessNew         int `json:"accessNew"`
	AccessExisting    int `json:"accessExisting"`
}

// Owner is a tool owner the import assigns, shown before the import is confirmed.
type Owner struct {
	Tool       string `json:"tool"`
	OwnerEmail string `json:"ownerEmail"`
}

// sampleDomain marks the template's example rows; they must be replaced or deleted.
const (
	sampleDomain = "@example.invalid"
	SampleTool   = "Example Tool"
)

func lower(s string) string { return strings.ToLower(s) }

// Validate checks the whole workbook against the snapshot and returns the plan. It reads no
// database and no clock; the import runs it again inside its transaction.
func Validate(wb Workbook, s Snapshot) Plan {
	v := &validator{s: s, people: map[string]person{}, tools: map[string]*sheetTool{},
		p: Plan{Owners: []Owner{}, Problems: []Problem{}, Warnings: []Problem{}}}
	if len(wb.Employees)+len(wb.Tools)+len(wb.Access) == 0 {
		v.p.Problems = append(v.p.Problems, Problem{Reason: "empty_workbook"})
		return v.p
	}
	v.employees(wb.Employees)
	v.toolRows(wb.Tools)
	v.accessRows(wb.Access)
	return v.p
}

type validator struct {
	s      Snapshot
	p      Plan
	people map[string]person     // every employee the workbook can name, by email
	tools  map[string]*sheetTool // tools of the Tools sheet, by lowercase name
}

// person is an employee a row can name: a new row of the Employees sheet or an existing one.
type person struct {
	row      int
	status   string
	existing *Employee
}

type sheetTool struct {
	first    ToolRow
	existing *Tool
	method   string            // stored value
	levels   map[string]string // lowercase -> exact name
	byLevel  map[string]int    // lowercase level -> row
}

func (v *validator) problem(sheet string, row int, column, value, reason string) *Problem {
	v.p.Problems = append(v.p.Problems, Problem{Sheet: sheet, Row: row, Column: column, Value: value, Reason: reason})
	return &v.p.Problems[len(v.p.Problems)-1]
}

func (v *validator) warning(sheet string, row int, column, value, reason, expected string) {
	v.p.Warnings = append(v.p.Warnings, Problem{Sheet: sheet, Row: row, Column: column, Value: value, Reason: reason, Expected: expected})
}

// email checks a required email cell; ok is false after a problem was reported.
func (v *validator) email(sheet string, row int, column, value string) (string, bool) {
	if value == "" {
		v.problem(sheet, row, column, value, "field_required")
		return "", false
	}
	e, err := auth.NormalizeEmail(value)
	if err != nil {
		v.problem(sheet, row, column, value, "invalid_email")
		return "", false
	}
	if strings.HasSuffix(e, sampleDomain) {
		v.problem(sheet, row, column, value, "sample_row")
		return "", false
	}
	return e, true
}

// text checks a cell's length and characters; required cells must not be empty.
func (v *validator) text(sheet string, row int, column, value string, required bool, max int) bool {
	if value == "" {
		if required {
			v.problem(sheet, row, column, value, "field_required")
		}
		return !required
	}
	if platform.CheckText(value, column, max) != nil {
		v.problem(sheet, row, column, value, "invalid_text")
		return false
	}
	return true
}

// entry checks a department or job position: exact, active; a case-only difference names
// the defined spelling.
func (v *validator) entry(row int, column, value string, list map[string]bool, kind string) bool {
	if value == "" {
		return true
	}
	active, ok := list[value]
	switch {
	case ok && active:
		return true
	case ok:
		v.problem(SheetEmployees, row, column, value, "retired_"+kind)
	default:
		v.problem(SheetEmployees, row, column, value, "unknown_"+kind).Expected = caseMatch(value, list)
	}
	return false
}

// caseMatch is the active name equal to value ignoring case, if any.
func caseMatch(value string, names map[string]bool) string {
	for n, active := range names {
		if active && strings.EqualFold(n, value) {
			return n
		}
	}
	return ""
}

func (v *validator) employees(rows []EmployeeRow) {
	seen := map[string]EmployeeRow{}
	var fresh []EmployeeRow
	for _, r := range rows {
		email, ok := v.email(SheetEmployees, r.Row, "Email", r.Email)
		if !ok {
			continue
		}
		if first, dup := seen[email]; dup {
			reason, a, b := "conflicting_employee", first, r
			a.Row, b.Row = 0, 0
			if a == b {
				reason = "duplicate_employee"
			}
			v.problem(SheetEmployees, r.Row, "Email", r.Email, reason).OtherRow = first.Row
			continue
		}
		seen[email] = r
		if ex, ok := v.s.Employees[email]; ok {
			v.p.Summary.EmployeesExisting++
			v.people[email] = person{row: r.Row, status: ex.Status, existing: &ex}
			v.compareEmployee(r, ex)
			continue
		}
		v.people[email] = person{row: r.Row, status: "ACTIVE"}
		r.Email = email
		v.text(SheetEmployees, r.Row, "Full Name", r.FullName, true, 200)
		v.entry(r.Row, "Department", r.Department, v.s.Departments, "department")
		v.entry(r.Row, "Job Position", r.JobPosition, v.s.Positions, "job_position")
		if r.ManagerEmail != "" {
			m, err := auth.NormalizeEmail(r.ManagerEmail)
			switch {
			case err != nil:
				v.problem(SheetEmployees, r.Row, "Manager Email", r.ManagerEmail, "invalid_email")
				m = "" // reported; the manager checks below skip it
			case m == email:
				v.problem(SheetEmployees, r.Row, "Manager Email", r.ManagerEmail, "self_manager")
				m = ""
			}
			r.ManagerEmail = m
		}
		fresh = append(fresh, r)
	}
	v.p.Summary.EmployeesNew = len(fresh)

	// Managers: an employee of the workbook or of AccessFlow, active; no cycles. Created
	// managers first, whatever the row order.
	byEmail := map[string]EmployeeRow{}
	for _, r := range fresh {
		byEmail[r.Email] = r
	}
	for _, r := range fresh {
		if r.ManagerEmail == "" {
			continue
		}
		m, ok := v.people[r.ManagerEmail]
		if !ok {
			if ex, found := v.s.Employees[r.ManagerEmail]; found {
				m, ok = person{status: ex.Status, existing: &ex}, true
			}
		}
		switch {
		case !ok:
			v.problem(SheetEmployees, r.Row, "Manager Email", r.ManagerEmail, "unknown_manager")
		case m.status != "ACTIVE":
			v.problem(SheetEmployees, r.Row, "Manager Email", r.ManagerEmail, "inactive_manager")
		}
	}
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[string]int{}
	var visit func(r EmployeeRow) bool // false: r is in, or leads into, a cycle
	visit = func(r EmployeeRow) bool {
		switch state[r.Email] {
		case visiting:
			return false
		case done:
			return true
		}
		state[r.Email] = visiting
		if m, ok := byEmail[r.ManagerEmail]; ok && !visit(m) {
			return false
		}
		state[r.Email] = done
		v.p.employees = append(v.p.employees, r)
		return true
	}
	for _, r := range fresh {
		if state[r.Email] == unvisited && !visit(r) {
			// Report every row of the cycle once.
			for e, st := range state {
				if st == visiting {
					c := byEmail[e]
					v.problem(SheetEmployees, c.Row, "Manager Email", c.ManagerEmail, "manager_cycle")
					state[e] = done
				}
			}
		}
	}
}

func (v *validator) compareEmployee(r EmployeeRow, ex Employee) {
	if ex.Status != "ACTIVE" {
		v.warning(SheetEmployees, r.Row, "Email", r.Email, "employee_not_active", ex.Status)
	}
	manager := r.ManagerEmail
	if m, err := auth.NormalizeEmail(manager); err == nil {
		manager = m
	}
	for _, c := range []struct{ column, got, want string }{
		{"Full Name", r.FullName, ex.FullName}, {"Department", r.Department, ex.Department},
		{"Job Position", r.JobPosition, ex.JobTitle}, {"Manager Email", manager, ex.ManagerEmail},
	} {
		if c.got != c.want {
			v.warning(SheetEmployees, r.Row, c.column, c.got, "employee_differs", c.want)
		}
	}
}

// methodName is the workbook spelling of a stored fulfillment method.
func methodName(stored string) string {
	for n, m := range Methods {
		if m == stored {
			return n
		}
	}
	return stored
}

func (v *validator) toolRows(rows []ToolRow) {
	for _, r := range rows {
		ok := v.text(SheetTools, r.Row, "Tool", r.Tool, true, 100)
		if ok && (r.Tool == SampleTool || strings.HasSuffix(lower(r.OwnerEmail), sampleDomain)) {
			v.problem(SheetTools, r.Row, "Tool", r.Tool, "sample_row")
			continue
		}
		for _, c := range []struct{ column, value string }{{"Fulfillment Method", r.Method}, {"Tool Owner Email", r.OwnerEmail},
			{"Admin Group", r.AdminGroup}, {"Approval Steps", r.Steps}} {
			ok = v.text(SheetTools, r.Row, c.column, c.value, true, 200) && ok
		}
		ok = v.text(SheetTools, r.Row, "Access Level", r.Level, true, 100) && ok
		if !ok {
			continue
		}
		t := v.tools[lower(r.Tool)]
		if t == nil {
			if t = v.newSheetTool(r); t == nil {
				if failed := v.tools[lower(r.Tool)]; failed != nil {
					failed.levels[lower(r.Level)] = r.Level
				}
				continue
			}
		} else if !v.sameTool(t, r) {
			t.levels[lower(r.Level)] = r.Level // its access rows are not reported again
			continue
		}
		v.levelRow(t, r)
	}
}

// newSheetTool starts a tool at its first row: an existing tool (compared, never changed)
// or a new one (fully checked). Nil after a problem.
func (v *validator) newSheetTool(r ToolRow) *sheetTool {
	t := &sheetTool{first: r, levels: map[string]string{}, byLevel: map[string]int{}}
	if ex, ok := v.s.Tools[lower(r.Tool)]; ok {
		switch {
		case ex.Name != r.Tool:
			v.problem(SheetTools, r.Row, "Tool", r.Tool, "unknown_tool").Expected = ex.Name
			return nil
		case ex.Status != "ACTIVE":
			v.problem(SheetTools, r.Row, "Tool", r.Tool, "retired_tool")
			return nil
		}
		t.existing, t.method = &ex, ex.Method
		for _, c := range []struct{ column, got, want string }{
			{"Fulfillment Method", r.Method, methodName(ex.Method)}, {"Tool Owner Email", lower(r.OwnerEmail), ex.OwnerEmail},
			{"Admin Group", r.AdminGroup, ex.AdminGroup},
		} {
			if c.got != c.want {
				v.warning(SheetTools, r.Row, c.column, c.got, "tool_differs", c.want)
			}
		}
		v.p.Summary.ToolsExisting++
		v.tools[lower(r.Tool)] = t
		return t
	}
	ok := true
	if t.method = Methods[r.Method]; t.method == "" {
		v.problem(SheetTools, r.Row, "Fulfillment Method", r.Method, "invalid_fulfillment_method").Expected = "Manual, Vendor, API"
		ok = false
	}
	if _, found := v.s.Groups[r.AdminGroup]; !found {
		p := v.problem(SheetTools, r.Row, "Admin Group", r.AdminGroup, "unknown_admin_group")
		for g := range v.s.Groups {
			if strings.EqualFold(g, r.AdminGroup) {
				p.Expected = g
			}
		}
		ok = false
	}
	owner, err := auth.NormalizeEmail(r.OwnerEmail)
	if err != nil {
		v.problem(SheetTools, r.Row, "Tool Owner Email", r.OwnerEmail, "invalid_email")
		ok = false
	} else if p, found := v.person(owner); !found {
		v.problem(SheetTools, r.Row, "Tool Owner Email", r.OwnerEmail, "unknown_owner")
		ok = false
	} else if p.status != "ACTIVE" {
		v.problem(SheetTools, r.Row, "Tool Owner Email", r.OwnerEmail, "inactive_owner")
		ok = false
	}
	v.tools[lower(r.Tool)] = t // later rows of the tool are compared with this one, even when it failed
	if !ok {
		return nil
	}
	t.first.OwnerEmail = owner
	v.p.Summary.ToolsNew++
	v.p.Owners = append(v.p.Owners, Owner{r.Tool, owner})
	v.p.tools = append(v.p.tools, newTool{ToolRow: t.first})
	return t
}

// sameTool requires a tool's later rows to repeat its first row's tool fields.
func (v *validator) sameTool(t *sheetTool, r ToolRow) bool {
	for _, c := range []struct{ column, got, want string }{
		{"Tool", r.Tool, t.first.Tool}, {"Fulfillment Method", r.Method, t.first.Method},
		{"Tool Owner Email", lower(r.OwnerEmail), lower(t.first.OwnerEmail)}, {"Admin Group", r.AdminGroup, t.first.AdminGroup},
	} {
		if c.got != c.want {
			p := v.problem(SheetTools, r.Row, c.column, c.got, "conflicting_tool")
			p.Expected, p.OtherRow = c.want, t.first.Row
			return false
		}
	}
	return t.existing != nil || slices.ContainsFunc(v.p.tools, func(n newTool) bool { return n.Tool == t.first.Tool })
}

func (v *validator) levelRow(t *sheetTool, r ToolRow) {
	if _, ok := Steps[r.Steps]; !ok {
		v.problem(SheetTools, r.Row, "Approval Steps", r.Steps, "invalid_approval_steps").Expected = strings.Join(StepNames, ", ")
		return
	}
	if first, dup := t.byLevel[lower(r.Level)]; dup {
		v.problem(SheetTools, r.Row, "Access Level", r.Level, "duplicate_level").OtherRow = first
		return
	}
	t.byLevel[lower(r.Level)] = r.Row
	if t.existing != nil {
		if l, ok := t.existing.Levels[lower(r.Level)]; ok {
			switch {
			case l.Name != r.Level:
				v.problem(SheetTools, r.Row, "Access Level", r.Level, "unknown_level").Expected = l.Name
			case !l.Active:
				v.problem(SheetTools, r.Row, "Access Level", r.Level, "retired_level")
			default:
				if l.Steps != r.Steps {
					v.warning(SheetTools, r.Row, "Approval Steps", r.Steps, "level_differs", l.Steps)
				}
				v.p.Summary.LevelsExisting++
				t.levels[lower(r.Level)] = r.Level
			}
			return
		}
		v.p.levels = append(v.p.levels, r)
	} else {
		n := &v.p.tools[len(v.p.tools)-1]
		for i := range v.p.tools {
			if v.p.tools[i].Tool == t.first.Tool {
				n = &v.p.tools[i]
			}
		}
		n.levels = append(n.levels, r)
	}
	v.p.Summary.LevelsNew++
	t.levels[lower(r.Level)] = r.Level
}

// person is an employee of the workbook or of AccessFlow.
func (v *validator) person(email string) (person, bool) {
	if p, ok := v.people[email]; ok {
		return p, true
	}
	if ex, ok := v.s.Employees[email]; ok {
		return person{status: ex.Status, existing: &ex}, true
	}
	return person{}, false
}

func (v *validator) accessRows(rows []AccessRow) {
	seen := map[[3]string]AccessRow{}
	held := map[[3]string]AccessRow{} // an employee holds a level once; a second account would be dropped
	for _, r := range rows {
		email, ok := v.email(SheetAccess, r.Row, "Employee Email", r.EmployeeEmail)
		ok = v.text(SheetAccess, r.Row, "Tool", r.Tool, true, 100) && ok
		ok = v.text(SheetAccess, r.Row, "Access Level", r.Level, true, 100) && ok
		ok = v.text(SheetAccess, r.Row, "Account", r.Account, false, 200) && ok
		if !ok {
			continue
		}
		who, found := v.person(email)
		if !found {
			v.problem(SheetAccess, r.Row, "Employee Email", r.EmployeeEmail, "unknown_employee")
			continue
		}
		toolID, levelID, method, ok := v.resolveAccess(r)
		if !ok {
			continue
		}
		account := r.Account
		if account == "" {
			account = email
		}
		if method == "API" {
			// The tool read matches API accounts by email; another account would make a
			// later removal revoke someone else's access in the tool.
			if lower(account) != email {
				v.problem(SheetAccess, r.Row, "Account", r.Account, "api_account_mismatch").Expected = email
				continue
			}
			account = email
		}
		key := [3]string{lower(r.Tool), lower(r.Level), lower(account)}
		if first, dup := seen[key]; dup {
			reason := "conflicting_access"
			if lower(first.EmployeeEmail) == email {
				reason = "duplicate_access"
			}
			v.problem(SheetAccess, r.Row, "Account", r.Account, reason).OtherRow = first.Row
			continue
		}
		holding := [3]string{email, lower(r.Tool), lower(r.Level)}
		if first, dup := held[holding]; dup {
			v.problem(SheetAccess, r.Row, "Account", r.Account, "duplicate_access").OtherRow = first.Row
			continue
		}
		seen[key], held[holding] = r, r
		if who.existing != nil && who.existing.Status == "TERMINATED" {
			v.warning(SheetAccess, r.Row, "Employee Email", r.EmployeeEmail, "employee_terminated", "")
		}
		r.EmployeeEmail, r.Account = email, account
		v.p.access = append(v.p.access, r)
		v.countAccess(r, who, toolID, levelID)
	}
}

// resolveAccess finds the tool and level a row names: of the Tools sheet or of AccessFlow,
// spelled exactly. The IDs are nil for a tool or level the import creates.
func (v *validator) resolveAccess(r AccessRow) (tool, level *uuid.UUID, method string, ok bool) {
	levels := map[string]string{} // lowercase -> exact
	var ex *Tool
	name := ""
	if t := v.tools[lower(r.Tool)]; t != nil {
		name, ex, method = t.first.Tool, t.existing, t.method
		maps.Copy(levels, t.levels)
	} else if e, found := v.s.Tools[lower(r.Tool)]; found && e.Status == "ACTIVE" {
		name, ex, method = e.Name, &e, e.Method
	}
	if name != r.Tool {
		p := v.problem(SheetAccess, r.Row, "Tool", r.Tool, "unknown_tool")
		if name != "" {
			p.Expected = name
		}
		return nil, nil, "", false
	}
	if ex != nil && ex.AccountKind != "" {
		// Such access is requested with the account and its GitHub id (A1 note 2026-11-01);
		// a workbook row has neither, and the tool read cannot check it.
		v.problem(SheetAccess, r.Row, "Tool", r.Tool, "account_kind_tool")
		return nil, nil, "", false
	}
	if ex != nil {
		tool = &ex.ID
		for k, l := range ex.Levels {
			if l.Active {
				levels[k] = l.Name
			}
		}
	}
	if levels[lower(r.Level)] != r.Level {
		p := v.problem(SheetAccess, r.Row, "Access Level", r.Level, "unknown_level")
		if exact, found := levels[lower(r.Level)]; found {
			p.Expected = exact
		} else {
			names := slices.Sorted(maps.Values(levels))
			p.Expected = strings.Join(names, ", ")
		}
		return nil, nil, "", false
	}
	if ex != nil {
		if l, found := ex.Levels[lower(r.Level)]; found {
			level = &l.ID
		}
	}
	return tool, level, method, true
}

// countAccess predicts what the D10 code will do with the row: create it, or skip what
// AccessFlow already records (with a warning when the record is not this employee's).
func (v *validator) countAccess(r AccessRow, who person, tool, level *uuid.UUID) {
	if tool == nil || level == nil {
		v.p.Summary.AccessNew++
		return
	}
	var id *uuid.UUID
	if who.existing != nil {
		id = &who.existing.ID
	}
	if imp, ok := v.s.Imported[Key{*tool, *level, lower(r.Account)}]; ok {
		v.p.Summary.AccessExisting++
		switch {
		case !imp.Open:
			v.warning(SheetAccess, r.Row, "Account", r.Account, "previously_removed", "")
		case imp.Employee == nil || id == nil || *imp.Employee != *id:
			v.warning(SheetAccess, r.Row, "Account", r.Account, "held_by_other", "")
		}
		return
	}
	if id != nil && v.s.Held[[2]uuid.UUID{*id, *level}] {
		v.p.Summary.AccessExisting++
		return
	}
	v.p.Summary.AccessNew++
}
