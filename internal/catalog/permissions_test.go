package catalog

import (
	"testing"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// Only a Super Admin changes the catalog (F2); everyone else gets 403 and nothing is written.
func TestCatalogPermissionMatrix(t *testing.T) {
	env := newEnv(t)
	_, admin := env.user("SUPER_ADMIN")
	tool := env.newTool(admin, "Jira", "API")
	level := env.newLevel(admin, tool, "Viewer", false, false)["id"].(string)
	owner := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{}).ID
	group := testutil.CreateGroup(t, env.db)
	entry := admin.Do("POST", "/api/v1/starter-packages", map[string]any{"department": "Engineering", "accessLevelId": level})
	testutil.RequireStatus(t, entry, 201, "")
	before := env.count("SELECT count(*) FROM audit_events WHERE action NOT LIKE 'LOGIN%'")

	writes := []struct {
		method, path string
		body         map[string]any
	}{
		{"POST", "/api/v1/tools", map[string]any{"name": "X", "fulfillmentMethod": "API", "ownerId": owner, "adminGroupId": group}},
		{"PUT", "/api/v1/tools/" + tool, map[string]any{"name": "X", "ownerId": owner, "adminGroupId": group, "version": 1}},
		{"POST", "/api/v1/tools/" + tool + "/retire", map[string]any{"version": 1}},
		{"POST", "/api/v1/tools/" + tool + "/levels", map[string]any{"name": "X", "requiresToolOwner": false, "requiresInfosec": false}},
		{"PUT", "/api/v1/tools/" + tool + "/levels/" + level, map[string]any{"name": "X", "requiresToolOwner": false, "requiresInfosec": false, "version": 1}},
		{"POST", "/api/v1/tools/" + tool + "/levels/" + level + "/retire", map[string]any{"version": 1}},
		{"POST", "/api/v1/starter-packages", map[string]any{"department": "Finance", "accessLevelId": level}},
		{"DELETE", "/api/v1/starter-packages/" + entry.Body["id"].(string), nil},
	}
	for _, role := range []string{"", "HR_MANAGER", "INFOSEC", "AUDITOR"} {
		var c *testutil.Client
		if role == "" {
			_, c = env.user()
		} else {
			_, c = env.user(role)
		}
		for _, w := range writes {
			t.Run(role+" "+w.method+" "+w.path, func(t *testing.T) {
				testutil.RequireStatus(t, c.Do(w.method, w.path, w.body), 403, "forbidden")
			})
		}
	}
	if after := env.count("SELECT count(*) FROM audit_events WHERE action NOT LIKE 'LOGIN%'"); after != before {
		t.Fatalf("audit events %d → %d; denied writes must write nothing", before, after)
	}
	if n := env.count("SELECT count(*) FROM tools"); n != 1 {
		t.Fatalf("tools = %d", n)
	}
	if n := env.count("SELECT count(*) FROM starter_packages WHERE removed_at IS NULL"); n != 1 {
		t.Fatalf("open starter package entries = %d; want 1", n)
	}
}
