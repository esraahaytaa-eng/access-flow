package requests

import (
	"bytes"
	"context"
	"encoding/csv"
	"net/http"
	"slices"
	"testing"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// accessOf is the "tool / level / account" of each access an audit event is about.
func accessOf(e map[string]any) (out []string) {
	for _, a := range e["access"].([]any) {
		a := a.(map[string]any)
		acc, _ := a["account"].(string)
		lvl, _ := a["level"].(string)
		out = append(out, a["tool"].(string)+" / "+lvl+" / "+acc)
	}
	return out
}

// Every access event names its tool and level as they were called when it happened, and the
// external account when there is one; the page, the tool filter and the CSV export agree.
func TestAuditEventsNameTheAccessTheyAreAbout(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer) // request, manager approval, grant, provisioning, verified activation
	tl.drain(t)
	g := w.myGrant(w.dev.c, w.jiraViewer)
	testutil.RequireStatus(t, revoke(w.dev.c, g, "Proje bitti"), http.StatusOK, "") // voluntary
	tl.drain(t)                                                                     // verified removal

	// Renamed afterwards: the earlier events keep the names they were recorded under.
	var group string
	if err := w.db.App.QueryRow(context.Background(), "SELECT admin_group_id::text FROM tools WHERE id = $1", w.jira).Scan(&group); err != nil {
		t.Fatal(err)
	}
	testutil.RequireStatus(t, w.admin.c.Do(http.MethodPut, "/api/v1/tools/"+w.jira.String()+"/levels/"+w.jiraViewer.String(),
		map[string]any{"name": "Okuyucu", "requiresToolOwner": false, "requiresInfosec": false, "version": 1}), http.StatusOK, "")
	testutil.RequireStatus(t, w.admin.c.Do(http.MethodPut, "/api/v1/tools/"+w.jira.String(),
		map[string]any{"name": "Jira Cloud", "ownerId": w.owner.ID, "adminGroupId": group, "version": 1}), http.StatusOK, "")

	auditor := w.auditorOf()
	events, _ := w.eventsOf(auditor, "tool="+w.jira.String())
	got := map[string][]string{}
	for _, e := range events {
		got[e["action"].(string)] = accessOf(e)
	}
	want := []string{"Jira / Viewer / "}
	for _, action := range []string{"REQUEST_SUBMITTED", "STEP_APPROVED", "GRANT_CREATED", "GRANT_ACTIVATED",
		"GRANT_REVOCATION_REQUESTED", "GRANT_REVOKED"} {
		if !slices.Equal(got[action], want) {
			t.Errorf("%s: access %v, want %v", action, got[action], want)
		}
	}
	if a := got["ACCESS_LEVEL_UPDATED"]; !slices.Equal(a, []string{"Jira / Okuyucu / "}) {
		t.Errorf("level rename: access %v", a) // named as it is after its own rename
	}
	if a := got["TOOL_UPDATED"]; !slices.Equal(a, []string{"Jira Cloud /  / "}) {
		t.Errorf("tool rename: access %v", a)
	}

	// An event about no access has an empty list, not null.
	w.exec(`INSERT INTO audit_events (action, entity_type) VALUES ('QA_PLAIN', 'test_entity')`)
	if plain, _ := w.eventsOf(auditor, "action=QA_PLAIN"); len(plain) != 1 || len(plain[0]["access"].([]any)) != 0 {
		t.Fatalf("plain event = %v", plain)
	}

	// The CSV carries the same tool, level and account columns.
	r := w.get(auditor.c, "/api/v1/audit/events/export?action=GRANT_REVOCATION_REQUESTED&tool="+w.jira.String())
	testutil.RequireStatus(t, r, http.StatusOK, "")
	rows, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(r.Raw, []byte("\xef\xbb\xbf")))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[h] = i
	}
	if len(rows) != 2 || rows[1][col["tool"]] != "Jira" || rows[1][col["access_level"]] != "Viewer" || rows[1][col["account"]] != "" ||
		rows[1][col["reason"]] != "Proje bitti" {
		t.Fatalf("export = %v", rows)
	}
}

// An imported grant names the external account it was found under.
func TestAuditEventsNameTheExternalAccount(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.fulfillers()
	r := w.admin.c.Do(http.MethodPost, "/api/v1/imports/csv", map[string]any{"fileName": "figma.csv",
		"content": "eposta;arac;seviye;hesap\n" + w.mgr.Email + ";Figma;Editor;mgr.figma\n"})
	testutil.RequireStatus(t, r, http.StatusCreated, "")
	events, _ := w.eventsOf(w.auditorOf(), "action=GRANT_IMPORTED")
	if len(events) != 1 || !slices.Equal(accessOf(events[0]), []string{"Figma / Editor / mgr.figma"}) {
		t.Fatalf("imported = %v", events)
	}
}
