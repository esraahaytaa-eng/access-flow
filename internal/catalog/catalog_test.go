package catalog

import (
	"maps"
	"slices"
	"sort"
	"testing"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// levelsOf maps "Tool/Level" to the level's JSON object in a GET /tools response.
func levelsOf(t *testing.T, r testutil.Response) map[string]map[string]any {
	t.Helper()
	testutil.RequireStatus(t, r, 200, "")
	out := map[string]map[string]any{}
	for _, tool := range r.Body["tools"].([]any) {
		tm := tool.(map[string]any)
		for _, l := range tm["levels"].([]any) {
			lm := l.(map[string]any)
			out[tm["name"].(string)+"/"+lm["name"].(string)] = lm
		}
	}
	return out
}

func keys(m map[string]any) []string { return slices.Sorted(maps.Keys(m)) }

func TestCatalogHidesRetired(t *testing.T) {
	env := newEnv(t)
	_, admin := env.user("SUPER_ADMIN")
	_, employee := env.user()
	_, auditor := env.user("AUDITOR")

	jira := env.newTool(admin, "Jira", "API")
	env.newLevel(admin, jira, "Viewer", false, false)
	old := env.newLevel(admin, jira, "Legacy", false, false)
	testutil.RequireStatus(t, admin.Do("POST", "/api/v1/tools/"+jira+"/levels/"+old["id"].(string)+"/retire",
		map[string]any{"version": old["version"]}), 200, "")
	gone := env.newTool(admin, "Gone", "MANUAL")
	env.newLevel(admin, gone, "User", false, false)
	testutil.RequireStatus(t, admin.Do("POST", "/api/v1/tools/"+gone+"/retire", map[string]any{"version": 1}), 200, "")
	empty := env.newTool(admin, "Empty", "VENDOR")
	only := env.newLevel(admin, empty, "User", false, false)
	testutil.RequireStatus(t, admin.Do("POST", "/api/v1/tools/"+empty+"/levels/"+only["id"].(string)+"/retire",
		map[string]any{"version": only["version"]}), 200, "")

	plain := levelsOf(t, employee.Do("GET", "/api/v1/tools", nil))
	if got := slices.Sorted(maps.Keys(plain)); !slices.Equal(got, []string{"Jira/Viewer"}) {
		t.Fatalf("employee sees %v; want only Jira/Viewer", got)
	}
	if n := len(employee.Do("GET", "/api/v1/tools", nil).Body["tools"].([]any)); n != 1 {
		t.Fatalf("employee sees %d tools; want 1 (tools without a requestable level are hidden)", n)
	}

	full := levelsOf(t, auditor.Do("GET", "/api/v1/tools", nil))
	for name, want := range map[string]bool{"Jira/Viewer": true, "Jira/Legacy": false, "Gone/User": false, "Empty/User": false} {
		if full[name] == nil || full[name]["requestable"] != want {
			t.Errorf("auditor %s = %v; want requestable %v", name, full[name], want)
		}
	}
}

func TestCatalogFieldsForPlainEmployees(t *testing.T) {
	env := newEnv(t)
	_, admin := env.user("SUPER_ADMIN")
	_, employee := env.user()
	jira := env.newTool(admin, "Jira", "API")
	env.newLevel(admin, jira, "Admin", true, true)

	r := employee.Do("GET", "/api/v1/tools", nil)
	level := levelsOf(t, r)["Jira/Admin"]
	if got := keys(level); !slices.Equal(got, []string{"id", "name", "recommended", "requestable", "steps"}) {
		t.Fatalf("level keys = %v", got)
	}
	if got := steps(level); !slices.Equal(got, []string{"MANAGER", "TOOL_OWNER", "INFOSEC"}) {
		t.Fatalf("steps = %v", got)
	}
	tool := r.Body["tools"].([]any)[0].(map[string]any)
	if got := keys(tool); !slices.Equal(got, []string{"adminGroup", "fulfillmentMethod", "id", "levels", "name", "owner"}) {
		t.Fatalf("tool keys = %v", got)
	}
	if keys(tool["owner"].(map[string]any))[1] != "id" || len(keys(tool["owner"].(map[string]any))) != 2 {
		t.Fatalf("owner = %v; want id and fullName only", tool["owner"])
	}
}

func TestRecommendedByDepartmentAndJobTitle(t *testing.T) {
	env := newEnv(t)
	_, admin := env.user("SUPER_ADMIN")
	jira := env.newTool(admin, "Jira", "API")
	viewer := env.newLevel(admin, jira, "Viewer", false, false)["id"]
	git := env.newTool(admin, "GitHub", "MANUAL")
	dev := env.newLevel(admin, git, "Developer", true, false)["id"]
	aws := env.newTool(admin, "AWS", "API")
	awsRead := env.newLevel(admin, aws, "Read", false, false)["id"]

	testutil.RequireStatus(t, admin.Do("POST", "/api/v1/starter-packages", map[string]any{"department": "Engineering", "accessLevelId": viewer}), 201, "")
	testutil.RequireStatus(t, admin.Do("POST", "/api/v1/starter-packages",
		map[string]any{"department": "Engineering", "jobTitle": "Backend Developer", "accessLevelId": dev}), 201, "")
	removed := admin.Do("POST", "/api/v1/starter-packages", map[string]any{"department": "Engineering", "accessLevelId": awsRead})
	testutil.RequireStatus(t, removed, 201, "")
	testutil.RequireStatus(t, admin.Do("DELETE", "/api/v1/starter-packages/"+removed.Body["id"].(string), nil), 204, "")

	for _, tc := range []struct {
		name, dept, title string
		want              []string
	}{
		{"department matches ignoring case", "engineering", "QA", []string{"Jira/Viewer"}},
		{"job title narrows an entry", "Engineering", "backend developer", []string{"GitHub/Developer", "Jira/Viewer"}},
		{"other department", "Finance", "Backend Developer", nil},
		{"no department", "", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, c := env.login(testutil.EmployeeSpec{Department: tc.dept, JobTitle: tc.title})
			var got []string
			for name, l := range levelsOf(t, c.Do("GET", "/api/v1/tools", nil)) {
				if l["recommended"] == true {
					got = append(got, name)
				}
			}
			sort.Strings(got)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("recommended = %v; want %v", got, tc.want)
			}
		})
	}
}

func TestStarterPackageAdministration(t *testing.T) {
	env := newEnv(t)
	_, admin := env.user("SUPER_ADMIN")
	_, employee := env.user()
	jira := env.newTool(admin, "Jira", "API")
	viewer := env.newLevel(admin, jira, "Viewer", false, false)
	old := env.newLevel(admin, jira, "Old", false, false)
	testutil.RequireStatus(t, admin.Do("POST", "/api/v1/tools/"+jira+"/levels/"+old["id"].(string)+"/retire",
		map[string]any{"version": old["version"]}), 200, "")

	entry := map[string]any{"department": " Engineering ", "accessLevelId": viewer["id"]}
	r := admin.Do("POST", "/api/v1/starter-packages", entry)
	testutil.RequireStatus(t, r, 201, "")
	if r.Body["department"] != "Engineering" {
		t.Fatalf("department = %v; want trimmed", r.Body["department"])
	}
	testutil.RequireStatus(t, admin.Do("POST", "/api/v1/starter-packages", map[string]any{"department": "ENGINEERING",
		"accessLevelId": viewer["id"]}), 409, "package_entry_exists")
	testutil.RequireStatus(t, admin.Do("POST", "/api/v1/starter-packages", map[string]any{"department": "Engineering",
		"accessLevelId": old["id"]}), 409, "level_not_requestable")
	testutil.RequireStatus(t, admin.Do("POST", "/api/v1/starter-packages", map[string]any{"department": " ",
		"accessLevelId": viewer["id"]}), 400, "department_required")

	list := employee.Do("GET", "/api/v1/starter-packages", nil)
	testutil.RequireStatus(t, list, 200, "")
	if n := len(list.Body["entries"].([]any)); n != 1 || list.Body["entries"].([]any)[0].(map[string]any)["requestable"] != true {
		t.Fatalf("entries = %v; want one requestable entry", list.Body["entries"])
	}
	// An entry whose level is retired later stays listed, marked not requestable.
	other := env.newLevel(admin, jira, "Editor", false, false)
	testutil.RequireStatus(t, admin.Do("POST", "/api/v1/starter-packages", map[string]any{"department": "QA", "accessLevelId": other["id"]}), 201, "")
	testutil.RequireStatus(t, admin.Do("POST", "/api/v1/tools/"+jira+"/levels/"+other["id"].(string)+"/retire",
		map[string]any{"version": other["version"]}), 200, "")
	for _, e := range admin.Do("GET", "/api/v1/starter-packages", nil).Body["entries"].([]any) {
		if em := e.(map[string]any); em["department"] == "QA" && em["requestable"] != false {
			t.Fatalf("entry for a retired level = %v; want requestable false", em)
		}
	}
	path := "/api/v1/starter-packages/" + r.Body["id"].(string)
	testutil.RequireStatus(t, employee.Do("DELETE", path, nil), 403, "forbidden")
	testutil.RequireStatus(t, admin.Do("DELETE", path, nil), 204, "")
	testutil.RequireStatus(t, admin.Do("DELETE", path, nil), 204, "")
	if env.audits("STARTER_PACKAGE_ENTRY_ADDED") != 2 || env.audits("STARTER_PACKAGE_ENTRY_REMOVED") != 1 {
		t.Fatal("want two added and one removed audit event")
	}
	if n := len(employee.Do("GET", "/api/v1/starter-packages", nil).Body["entries"].([]any)); n != 1 {
		t.Fatalf("removed entry still listed")
	}
	if n := env.count("SELECT count(*) FROM starter_packages"); n != 2 {
		t.Fatal("removal must keep the row")
	}
}
