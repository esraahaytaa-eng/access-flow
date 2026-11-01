package requests

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// sandbox adds an API tool whose accounts are GitHub usernames, with a Read level that needs
// the Tool Owner, and a lookup that knows octocat (DECISIONS.md A1 note 2026-11-01).
func (w *world) sandbox() (tool, read uuid.UUID, lookups *int) {
	w.t.Helper()
	tool = testutil.CreateTool(w.t, w.db, testutil.ToolSpec{Name: "GitHub sandbox", OwnerID: w.owner.ID, AccountKind: "GITHUB_LOGIN"})
	read = testutil.CreateLevel(w.t, w.db, tool, "Read", true, false)
	lookups = new(int)
	w.svc.Accounts = func(_ context.Context, kind, name string) (access.ExternalAccount, error) {
		*lookups++
		switch {
		case kind != "GITHUB_LOGIN":
			return access.ExternalAccount{}, errors.New("unexpected kind " + kind)
		case name == "Octocat" || name == "octocat":
			return access.ExternalAccount{Name: "octocat", Ref: "583231"}, nil // GitHub's spelling and id
		case name == "github":
			return access.ExternalAccount{}, access.ErrAccountNotAllowed // an organization
		case name == "bad name":
			return access.ExternalAccount{}, access.ErrAccountInvalid
		case name == "down":
			return access.ExternalAccount{}, errors.New("github: 502")
		case name == "limited":
			return access.ExternalAccount{}, fmt.Errorf("github status 429: %w", access.ErrRateLimited)
		}
		return access.ExternalAccount{}, access.ErrAccountNotFound
	}
	return tool, read, lookups
}

func withAccount(it map[string]any, account string) map[string]any {
	it["account"] = account
	return it
}

func TestExternalAccountValidation(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, read, lookups := w.sandbox()
	for _, tc := range []struct {
		name, account string
		level         uuid.UUID
		status        int
		code          string
	}{
		{"required for a GitHub tool", "", read, 400, "account_required"},
		{"blank is missing", "  ", read, 400, "account_required"},
		{"not for an email tool", "octocat", w.jiraViewer, 400, "account_not_allowed"},
		{"invalid format", "bad name", read, 400, "account_invalid"},
		{"unknown account", "nobody-here", read, 400, "account_not_found"},
		{"an organization is not a user", "github", read, 400, "account_not_allowed"},
		{"GitHub unreachable", "down", read, 503, "account_check_unavailable"},
		{"GitHub rate limit", "limited", read, 429, "account_check_rate_limited"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := submit(w.dev.c, body(nil, withAccount(item(tc.level), tc.account)))
			testutil.RequireStatus(t, r, tc.status, tc.code)
			if r.Body["field"] != "items[0].account" { // shown next to the GitHub username field
				t.Fatalf("field = %v", r.Body["field"])
			}
		})
	}
	// The same level twice is refused (duplicate_tool) after one lookup, not one per item.
	before := *lookups
	r := submit(w.dev.c, body(nil, withAccount(item(read), "octocat"), withAccount(item(read), "octocat"), withAccount(item(read), "octocat")))
	testutil.RequireStatus(t, r, 400, "duplicate_tool")
	if *lookups-before != 1 {
		t.Fatalf("GitHub lookups for one repeated level = %d; want 1", *lookups-before)
	}
	w.svc.Accounts = nil // no GitHub adapter configured
	testutil.RequireStatus(t, submit(w.dev.c, body(nil, withAccount(item(read), "octocat"))), 503, "account_check_unavailable")
	// An email tool needs no lookup, with or without the adapter.
	w.mustSubmit(w.dev.c, body(nil, item(w.jiraViewer)))
	if n := w.count("SELECT count(*) FROM request_items WHERE account IS NOT NULL"); n != 0 {
		t.Fatalf("items with an account = %d", n)
	}
}

func TestExternalAccountThroughApproval(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	tool, read, lookups := w.sandbox()

	req := w.mustSubmit(w.dev.c, body(nil, withAccount(item(read), " Octocat ")))
	it := items(req)[0]
	want := map[string]any{"name": "octocat", "ref": "583231"}
	requireAccount := func(where string, got any) {
		t.Helper()
		if m, ok := got.(map[string]any); !ok || m["name"] != want["name"] || m["ref"] != want["ref"] {
			t.Fatalf("%s account = %v; want %v", where, got, want)
		}
	}
	requireAccount("submitted item", it["account"])
	if n := w.count(`SELECT count(*) FROM audit_events WHERE action = 'REQUEST_SUBMITTED'
		AND details->'items'->0->>'account' = 'octocat' AND details->'items'->0->>'accountRef' = '583231'`); n != 1 {
		t.Fatal("REQUEST_SUBMITTED does not record the account")
	}
	// Both approvers see the account they approve.
	for _, approver := range []person{w.mgr, w.owner} {
		inbox := w.inbox(approver.c)
		if len(inbox) != 1 {
			t.Fatalf("inbox = %v", inbox)
		}
		requireAccount("inbox item", inbox[0].(map[string]any)["item"].(map[string]any)["account"])
		w.approve(approver.c, open(t, w.get(w.dev.c, "/api/v1/requests/"+req["id"].(string)).Body["items"].([]any)[0].(map[string]any)))
	}
	if n := w.count("SELECT count(*) FROM grants WHERE account = 'octocat' AND account_ref = '583231'"); n != 1 {
		t.Fatal("the grant does not carry the requested account")
	}
	tl.drain(t) // the Mock Tool Service is told the GitHub login, not the email
	g := w.myGrant(w.dev.c, read)
	requireGrant(t, g, "ACTIVE", "ACTIVE / VERIFIED")
	requireAccount("my access", g["account"])
	if n := w.count("SELECT count(*) FROM grants g JOIN operations o ON o.grant_id = g.id WHERE g.account = 'octocat' AND o.status = 'SUCCEEDED'"); n != 1 {
		t.Fatal("the operation did not run for the account")
	}

	// A level change keeps the account: no new lookup, no way to name another one.
	write := testutil.CreateLevel(t, w.db, tool, "Write", false, false)
	before := *lookups
	change := w.mustChange(w.dev.c, g, changeBody(write, untilRevoked))
	requireAccount("level change item", items(change)[0]["account"])
	if *lookups != before {
		t.Fatal("a level change looked the account up again")
	}
	w.approveAll(items(change)[0])
	if n := w.count("SELECT count(*) FROM grants WHERE access_level_id = $1 AND account = 'octocat' AND account_ref = '583231'", write); n != 1 {
		t.Fatal("the new level's grant lost the account")
	}
}

func TestExternalAccountDirectGrant(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, read, _ := w.sandbox()
	testutil.RequireStatus(t, directGrant(w.admin, directBody(w.dev.ID, read, in(15))), 400, "account_required")
	b := directBody(w.dev.ID, read, in(15))
	b["account"] = "octocat"
	r := directGrant(w.admin, b)
	testutil.RequireStatus(t, r, http.StatusCreated, "")
	if a, _ := items(r.Body)[0]["account"].(map[string]any); a["name"] != "octocat" || a["ref"] != "583231" {
		t.Fatalf("direct grant account = %v", items(r.Body)[0]["account"])
	}
	if n := w.count(`SELECT count(*) FROM audit_events WHERE action = 'DIRECT_GRANT_CREATED'
		AND details->>'account' = 'octocat' AND details->>'accountRef' = '583231'`); n != 1 {
		t.Fatal("DIRECT_GRANT_CREATED does not record the account")
	}
	if n := w.count("SELECT count(*) FROM grants WHERE account = 'octocat' AND account_ref = '583231'"); n != 1 {
		t.Fatal("the direct grant does not carry the account")
	}
}

// The tool read asks the Mock Tool Service, which never holds a GitHub tool's accounts.
func TestToolReadSkipsGitHubTools(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tl := w.startTools()
	tool, read, _ := w.sandbox()
	tl.legacy(t, tool, read, w.dev.Email, "ACTIVE")
	r := w.admin.c.Do(http.MethodPost, "/api/v1/imports/mock", map[string]any{})
	testutil.RequireStatus(t, r, http.StatusCreated, "")
	if n := w.count("SELECT count(*) FROM grants WHERE tool_id = $1", tool); n != 0 {
		t.Fatalf("grants imported for the GitHub tool = %d", n)
	}
}

// A retried submission returns the stored request without asking GitHub again, so a retry
// still succeeds while GitHub is unreachable (A9).
func TestExternalAccountRetryNeedsNoLookup(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, read, lookups := w.sandbox()
	sub := body(nil, withAccount(item(read), "octocat"))
	first := w.mustSubmit(w.dev.c, sub)
	direct := directBody(w.mgr.ID, read, in(15))
	direct["account"] = "octocat"
	testutil.RequireStatus(t, directGrant(w.admin, direct), http.StatusCreated, "")

	before := *lookups
	sub["items"].([]map[string]any)[0]["account"] = "down" // GitHub would now fail the lookup
	direct["account"] = "down"
	r := submit(w.dev.c, sub)
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Body["id"] != first["id"] {
		t.Fatalf("retry returned request %v; want %v", r.Body["id"], first["id"])
	}
	testutil.RequireStatus(t, directGrant(w.admin, direct), http.StatusOK, "")
	if *lookups != before {
		t.Fatalf("retries looked the account up %d times; want 0", *lookups-before)
	}
}

// The integration status the catalog and the tool page show: any signed-in employee, no
// GitHub call, nothing but whether the adapter is on and which tool it serves.
func TestGitHubIntegrationStatus(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	tool, _, _ := w.sandbox()
	status := func(p person) testutil.Response {
		return p.c.Do(http.MethodGet, "/api/v1/integrations/github", nil)
	}
	everyone := []person{w.dev, w.owner, w.is1, w.admin}
	anonymous := testutil.NewClient(t, w.srv)
	testutil.RequireStatus(t, anonymous.Do(http.MethodGet, "/api/v1/integrations/github", nil), http.StatusUnauthorized, "authentication_required")
	// Off: the default setup has no adapter, so every API tool uses the Mock Tool Service.
	for _, p := range everyone {
		if r := status(p); r.Status != http.StatusOK || r.Body["enabled"] != false || r.Body["toolId"] != nil || len(r.Body) != 2 {
			t.Fatalf("status = %d %v", r.Status, r.Body)
		}
	}

	var calls atomic.Int32
	gh := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		rw.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(gh.Close)
	w.access.GitHub = access.NewGitHubToolClient(w.db.App, gh.URL, "sandbox/repo", "github_pat_secret", time.Second)
	w.access.GitHubTool = tool
	for _, p := range everyone {
		r := status(p)
		if r.Status != http.StatusOK || r.Body["enabled"] != true || r.Body["toolId"] != tool.String() || len(r.Body) != 2 {
			t.Fatalf("status = %d %v", r.Status, r.Body)
		}
		for _, secret := range []string{"github_pat_secret", "sandbox/repo", gh.URL} {
			if strings.Contains(string(r.Raw), secret) {
				t.Fatalf("the status leaks %q: %s", secret, r.Raw)
			}
		}
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("the status called GitHub %d times; want 0", n)
	}
}
