package catalog

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func steps(body map[string]any) []string {
	var out []string
	for _, s := range body["steps"].([]any) {
		out = append(out, s.(string))
	}
	return out
}

func TestCreateToolAndLevel(t *testing.T) {
	env := newEnv(t)
	_, admin := env.user("SUPER_ADMIN")
	infosec, _ := env.user("INFOSEC")

	tool := env.newTool(admin, "Jira", "API")
	level := env.newLevel(admin, tool, "Viewer", false, false)
	if got := steps(level); !slices.Equal(got, []string{"MANAGER"}) {
		t.Fatalf("steps = %v; want [MANAGER]", got)
	}
	if env.audits("TOOL_CREATED") != 1 || env.audits("ACCESS_LEVEL_CREATED") != 1 {
		t.Fatal("want one TOOL_CREATED and one ACCESS_LEVEL_CREATED audit event")
	}
	if n := env.notifications(infosec.ID); n != 1 {
		t.Fatalf("InfoSec notifications = %d; want 1 (creating a level sets its approval policy)", n)
	}
}

func TestToolValidation(t *testing.T) {
	env := newEnv(t)
	_, admin := env.user("SUPER_ADMIN")
	env.newTool(admin, "Jira", "API")
	active := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{}).ID
	terminated := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{Status: "TERMINATED"}).ID
	group := testutil.CreateGroup(t, env.db)

	for _, tc := range []struct {
		name   string
		body   map[string]any
		status int
		code   string
	}{
		{"blank name", map[string]any{"name": " ", "fulfillmentMethod": "API", "ownerId": active, "adminGroupId": group}, 400, "name_required"},
		{"name too long", map[string]any{"name": strings.Repeat("a", 101), "fulfillmentMethod": "API", "ownerId": active, "adminGroupId": group}, 400, "value_too_long"},
		{"unknown method", map[string]any{"name": "X", "fulfillmentMethod": "SCRIPT", "ownerId": active, "adminGroupId": group}, 400, "invalid_fulfillment_method"},
		{"terminated owner", map[string]any{"name": "X", "fulfillmentMethod": "API", "ownerId": terminated, "adminGroupId": group}, 400, "owner_not_active"},
		{"unknown owner", map[string]any{"name": "X", "fulfillmentMethod": "API", "ownerId": uuid.New(), "adminGroupId": group}, 400, "owner_not_active"},
		{"unknown group", map[string]any{"name": "X", "fulfillmentMethod": "API", "ownerId": active, "adminGroupId": uuid.New()}, 400, "admin_group_not_found"},
		{"duplicate name in another case", map[string]any{"name": "JIRA", "fulfillmentMethod": "MANUAL", "ownerId": active, "adminGroupId": group}, 409, "tool_name_taken"},
		{"missing owner", map[string]any{"name": "X", "fulfillmentMethod": "API", "adminGroupId": group}, 400, "field_required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.RequireStatus(t, admin.Do("POST", "/api/v1/tools", tc.body), tc.status, tc.code)
		})
	}
	if n := env.count("SELECT count(*) FROM tools"); n != 1 {
		t.Fatalf("tools = %d; want 1", n)
	}
	if n := env.audits("TOOL_CREATED"); n != 1 {
		t.Fatalf("TOOL_CREATED = %d; want 1", n)
	}
}

// A tool may name its accounts by a GitHub username instead of the email (A1 note 2026-11-01).
func TestToolAccountKind(t *testing.T) {
	env := newEnv(t)
	_, admin := env.user("SUPER_ADMIN")
	owner := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{}).ID
	group := testutil.CreateGroup(t, env.db)
	body := func(name, method string, kind any) map[string]any {
		return map[string]any{"name": name, "fulfillmentMethod": method, "ownerId": owner, "adminGroupId": group, "accountKind": kind}
	}

	r := admin.Do("POST", "/api/v1/tools", body("GitHub", "API", "GITHUB_LOGIN"))
	testutil.RequireStatus(t, r, 201, "")
	if r.Body["accountKind"] != "GITHUB_LOGIN" {
		t.Fatalf("accountKind = %v", r.Body["accountKind"])
	}
	var audited string
	env.db.App.QueryRow(t.Context(), "SELECT details->>'accountKind' FROM audit_events WHERE action = 'TOOL_CREATED'").Scan(&audited)
	if audited != "GITHUB_LOGIN" {
		t.Fatalf("audited accountKind = %q", audited)
	}
	r = admin.Do("POST", "/api/v1/tools", body("Jira", "API", nil))
	testutil.RequireStatus(t, r, 201, "")
	if _, ok := r.Body["accountKind"]; ok {
		t.Fatalf("an email-account tool reports accountKind %v", r.Body["accountKind"])
	}
	testutil.RequireStatus(t, admin.Do("POST", "/api/v1/tools", body("X", "API", "SLACK_ID")), 400, "invalid_account_kind")
	// Only an API tool looks the account up and acts on it; Manual and Vendor work by email.
	testutil.RequireStatus(t, admin.Do("POST", "/api/v1/tools", body("Y", "MANUAL", "GITHUB_LOGIN")), 400, "account_kind_requires_api")

	kinds := map[string]any{}
	for _, tool := range admin.Do("GET", "/api/v1/tools", nil).Body["tools"].([]any) {
		kinds[tool.(map[string]any)["name"].(string)] = tool.(map[string]any)["accountKind"]
	}
	if kinds["GitHub"] != "GITHUB_LOGIN" || kinds["Jira"] != nil {
		t.Fatalf("catalog account kinds = %v", kinds)
	}
}

func TestAddingAStepNotifiesInfoSec(t *testing.T) {
	env := newEnv(t)
	_, admin := env.user("SUPER_ADMIN")
	infosec1, _ := env.user("INFOSEC")
	infosec2, _ := env.user("INFOSEC")
	suspended := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{Status: "SUSPENDED", Roles: []string{"INFOSEC"}})
	former := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{Roles: []string{"INFOSEC"}})
	env.count("WITH u AS (UPDATE role_assignments SET valid_to = now() WHERE employee_id = $1 RETURNING 1) SELECT count(*) FROM u", former.ID)

	tool := env.newTool(admin, "Jira", "API")
	level := env.newLevel(admin, tool, "Viewer", false, false)
	r := admin.Do("PUT", "/api/v1/tools/"+tool+"/levels/"+level["id"].(string),
		map[string]any{"name": "Viewer", "requiresToolOwner": false, "requiresInfosec": true, "version": level["version"]})
	testutil.RequireStatus(t, r, 200, "")
	if got := steps(r.Body); !slices.Equal(got, []string{"MANAGER", "INFOSEC"}) {
		t.Fatalf("steps = %v", got)
	}
	var details string
	env.db.App.QueryRow(t.Context(), "SELECT details->>'steps' FROM audit_events WHERE action = 'ACCESS_LEVEL_UPDATED'").Scan(&details)
	if details != `{"to": ["MANAGER", "INFOSEC"], "from": ["MANAGER"]}` {
		t.Fatalf("audit steps = %s", details)
	}
	for _, id := range []uuid.UUID{infosec1.ID, infosec2.ID} {
		if n := env.notifications(id); n != 2 { // creation + the added step
			t.Fatalf("active InfoSec notifications = %d; want 2", n)
		}
	}
	for _, id := range []uuid.UUID{suspended.ID, former.ID} {
		if n := env.notifications(id); n != 0 {
			t.Fatalf("inactive or former InfoSec got %d notifications", n)
		}
	}
}

func TestRemovingAStepIsAlsoAPolicyChange(t *testing.T) {
	env := newEnv(t)
	_, admin := env.user("SUPER_ADMIN")
	infosec, _ := env.user("INFOSEC")
	tool := env.newTool(admin, "AWS", "API")
	level := env.newLevel(admin, tool, "Admin", true, true)
	testutil.RequireStatus(t, admin.Do("PUT", "/api/v1/tools/"+tool+"/levels/"+level["id"].(string),
		map[string]any{"name": "Admin", "requiresToolOwner": true, "requiresInfosec": false, "version": level["version"]}), 200, "")
	if n := env.notifications(infosec.ID); n != 2 {
		t.Fatalf("notifications = %d; want 2", n)
	}
}

func TestRenameIsAuditedNotNotified(t *testing.T) {
	env := newEnv(t)
	_, admin := env.user("SUPER_ADMIN")
	infosec, _ := env.user("INFOSEC")
	tool := env.newTool(admin, "Jira", "API")
	level := env.newLevel(admin, tool, "Viewer", false, false)
	testutil.RequireStatus(t, admin.Do("PUT", "/api/v1/tools/"+tool+"/levels/"+level["id"].(string),
		map[string]any{"name": "Reader", "requiresToolOwner": false, "requiresInfosec": false, "version": level["version"]}), 200, "")
	if env.audits("ACCESS_LEVEL_UPDATED") != 1 || env.notifications(infosec.ID) != 1 {
		t.Fatal("a rename is audited and not notified")
	}
}

func TestUnchangedEditDoesNothing(t *testing.T) {
	env := newEnv(t)
	_, admin := env.user("SUPER_ADMIN")
	tool := env.newTool(admin, "Jira", "API")
	level := env.newLevel(admin, tool, "Viewer", false, false)
	r := admin.Do("PUT", "/api/v1/tools/"+tool+"/levels/"+level["id"].(string),
		map[string]any{"name": "Viewer", "requiresToolOwner": false, "requiresInfosec": false, "version": level["version"]})
	testutil.RequireStatus(t, r, 200, "")
	if r.Body["version"] != level["version"] || env.audits("ACCESS_LEVEL_UPDATED") != 0 {
		t.Fatal("an edit that changes nothing must not bump the version or audit")
	}
}

func TestToolEdit(t *testing.T) {
	env := newEnv(t)
	_, admin := env.user("SUPER_ADMIN")
	tool := env.newTool(admin, "Jira", "API")
	newOwner := testutil.CreateEmployee(t, env.db, testutil.EmployeeSpec{}).ID
	group := testutil.CreateGroup(t, env.db)
	r := admin.Do("PUT", "/api/v1/tools/"+tool, map[string]any{"name": "Jira Cloud", "ownerId": newOwner, "adminGroupId": group, "version": 1})
	testutil.RequireStatus(t, r, 200, "")
	if r.Body["name"] != "Jira Cloud" || r.Body["ownerId"] != newOwner.String() || r.Body["version"] != 2.0 {
		t.Fatalf("body = %v", r.Body)
	}
	if env.audits("TOOL_UPDATED") != 1 {
		t.Fatal("want one TOOL_UPDATED")
	}
	// The method cannot change: it is not an accepted field.
	testutil.RequireStatus(t, admin.Do("PUT", "/api/v1/tools/"+tool, map[string]any{"name": "Jira Cloud", "ownerId": newOwner,
		"adminGroupId": group, "version": 2, "fulfillmentMethod": "MANUAL"}), 400, "invalid_json")
	// Every field is required, so a forgotten key never changes a value.
	testutil.RequireStatus(t, admin.Do("PUT", "/api/v1/tools/"+tool, map[string]any{"name": "Jira Cloud", "ownerId": newOwner, "version": 2}),
		400, "field_required")
	testutil.RequireStatus(t, admin.Do("PUT", "/api/v1/tools/"+tool, map[string]any{"name": "Jira Cloud", "ownerId": newOwner,
		"adminGroupId": group, "version": 1}), 409, "version_conflict")
	if m := env.count("SELECT count(*) FROM tools WHERE fulfillment_method = 'API'"); m != 1 {
		t.Fatal("method changed")
	}
}

func TestLevelEditRequiresEveryField(t *testing.T) {
	env := newEnv(t)
	_, admin := env.user("SUPER_ADMIN")
	tool := env.newTool(admin, "AWS", "API")
	level := env.newLevel(admin, tool, "Admin", true, true)
	// A missing step flag must not silently drop the step.
	testutil.RequireStatus(t, admin.Do("PUT", "/api/v1/tools/"+tool+"/levels/"+level["id"].(string),
		map[string]any{"name": "Admin", "requiresToolOwner": true, "version": level["version"]}), 400, "field_required")
	testutil.RequireStatus(t, admin.Do("POST", "/api/v1/tools/"+tool+"/levels",
		map[string]any{"name": "Viewer", "requiresToolOwner": false}), 400, "field_required")
}

func TestRetire(t *testing.T) {
	env := newEnv(t)
	_, admin := env.user("SUPER_ADMIN")
	tool := env.newTool(admin, "Jira", "API")
	level := env.newLevel(admin, tool, "Viewer", false, false)
	levelPath := "/api/v1/tools/" + tool + "/levels/" + level["id"].(string)

	r := admin.Do("POST", levelPath+"/retire", map[string]any{"version": level["version"]})
	testutil.RequireStatus(t, r, 200, "")
	if r.Body["status"] != "RETIRED" {
		t.Fatalf("status = %v", r.Body["status"])
	}
	testutil.RequireStatus(t, admin.Do("POST", levelPath+"/retire", map[string]any{"version": level["version"]}), 200, "")
	if env.audits("ACCESS_LEVEL_RETIRED") != 1 {
		t.Fatal("retiring twice must audit once")
	}
	testutil.RequireStatus(t, admin.Do("PUT", levelPath, map[string]any{"name": "V", "requiresToolOwner": false,
		"requiresInfosec": false, "version": r.Body["version"]}), 409, "level_retired")

	testutil.RequireStatus(t, admin.Do("POST", "/api/v1/tools/"+tool+"/retire", map[string]any{"version": 1}), 200, "")
	testutil.RequireStatus(t, admin.Do("POST", "/api/v1/tools/"+tool+"/levels", map[string]any{"name": "Editor",
		"requiresToolOwner": false, "requiresInfosec": false}), 409, "tool_retired")
	testutil.RequireStatus(t, admin.Do("PUT", "/api/v1/tools/"+tool, map[string]any{"name": "J",
		"ownerId": uuid.New(), "adminGroupId": uuid.New(), "version": 2}), 409, "tool_retired")
	if env.audits("TOOL_RETIRED") != 1 {
		t.Fatal("want one TOOL_RETIRED")
	}
	testutil.RequireStatus(t, admin.Do("POST", "/api/v1/tools/"+uuid.NewString()+"/retire", map[string]any{"version": 1}), 404, "not_found")
}

func TestConcurrentLevelEditsWithTheSameVersion(t *testing.T) {
	env := newEnv(t)
	_, admin := env.user("SUPER_ADMIN")
	infosec, _ := env.user("INFOSEC")
	tool := env.newTool(admin, "Jira", "API")
	level := env.newLevel(admin, tool, "Viewer", false, false)

	var wg sync.WaitGroup
	statuses := make(chan int, 10)
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := admin.Do("PUT", "/api/v1/tools/"+tool+"/levels/"+level["id"].(string), map[string]any{
				"name": fmt.Sprintf("Viewer %d", i), "requiresToolOwner": false, "requiresInfosec": true, "version": level["version"]})
			statuses <- r.Status
		}()
	}
	wg.Wait()
	close(statuses)
	got := map[int]int{}
	for s := range statuses {
		got[s]++
	}
	if got[http.StatusOK] != 1 || got[http.StatusConflict] != 9 {
		t.Fatalf("statuses = %v; want one 200 and nine 409", got)
	}
	if env.audits("ACCESS_LEVEL_UPDATED") != 1 || env.notifications(infosec.ID) != 2 {
		t.Fatal("want one update audit and one policy notification for the winning edit")
	}
}

// A retirement committed while a write waited for the tool's lock must be seen.
func TestWritesSeeARetirementCommittedWhileWaiting(t *testing.T) {
	env := newEnv(t)
	_, admin := env.user("SUPER_ADMIN")
	tool := env.newTool(admin, "Jira", "API")
	level := env.newLevel(admin, tool, "Viewer", false, false)["id"].(string)
	retire := func(sql string) func(context.Context, pgx.Tx) error {
		return func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "SELECT 1 FROM tools WHERE id = $1 FOR UPDATE", tool); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, sql, level)
			return err
		}
	}
	codeOf := func(r testutil.Response) error {
		if r.Status == 409 {
			return fmt.Errorf("%v", r.Body["error"])
		}
		return fmt.Errorf("status %d", r.Status)
	}

	err := testutil.WhileLocked(t, env.db.App, retire("UPDATE access_levels SET status = 'RETIRED' WHERE id = $1"), func() error {
		return codeOf(admin.Do("POST", "/api/v1/starter-packages", map[string]any{"department": "Engineering", "accessLevelId": level}))
	})
	if err == nil || err.Error() != "level_not_requestable" {
		t.Fatalf("starter package for a level retired meanwhile: %v; want level_not_requestable", err)
	}
	err = testutil.WhileLocked(t, env.db.App, retire("UPDATE tools SET status = 'RETIRED' WHERE id = (SELECT tool_id FROM access_levels WHERE id = $1)"), func() error {
		return codeOf(admin.Do("POST", "/api/v1/tools/"+tool+"/levels", map[string]any{"name": "Editor", "requiresToolOwner": false, "requiresInfosec": false}))
	})
	if err == nil || err.Error() != "tool_retired" {
		t.Fatalf("level added to a tool retired meanwhile: %v; want tool_retired", err)
	}
}
