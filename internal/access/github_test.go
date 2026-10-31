package access_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

const (
	sandboxRepo  = "accessflow-sandbox/demo-repository"
	sandboxToken = "github_pat_never-printed-0123456789"
)

// fakeGitHub is the part of the GitHub REST API the adapter uses, for one repository. Any
// call outside that repository, or without the token, fails the test.
type fakeGitHub struct {
	t       *testing.T
	mu      sync.Mutex
	roles   map[string]string // lowercased login → role_name ("read", "write", ...)
	members map[string]bool   // org members: a PUT adds them directly, without an invitation
	invites []fakeInvite
	nextID  int64
	calls   []string // "METHOD path", in order
	replies []fakeReply
	private bool
	// beforeInviteDelete runs just before an invitation is deleted (a race: accepted meanwhile).
	beforeInviteDelete func(f *fakeGitHub, inv fakeInvite)
	// afterCollaboratorDelete runs after a collaborator is deleted (access remaining another way).
	afterCollaboratorDelete func(f *fakeGitHub, login string)
}

type fakeInvite struct {
	id      int64
	login   string
	perm    string
	expired bool
}

type fakeReply struct {
	status int
	header map[string]string
	body   string
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	return &fakeGitHub{t: t, roles: map[string]string{}, members: map[string]bool{}, nextID: 100, private: true}
}

// failNext answers the next n calls with status (and header), whatever they ask.
func (f *fakeGitHub) failNext(n, status int, header map[string]string, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for range n {
		f.replies = append(f.replies, fakeReply{status, header, body})
	}
}

// accept is the invitee accepting AccessFlow's (or anyone's) pending invitation.
func (f *fakeGitHub) accept(login string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acceptLocked(login)
}

func (f *fakeGitHub) acceptLocked(login string) {
	for i, inv := range f.invites {
		if strings.EqualFold(inv.login, login) {
			f.roles[strings.ToLower(login)] = inv.perm
			f.invites = append(f.invites[:i], f.invites[i+1:]...)
			return
		}
	}
	f.t.Fatalf("no invitation for %s to accept", login)
}

// decline is the invitee declining (or the invitation disappearing for another reason).
func (f *fakeGitHub) decline(login string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, inv := range f.invites {
		if strings.EqualFold(inv.login, login) {
			f.invites = append(f.invites[:i], f.invites[i+1:]...)
			return
		}
	}
}

func (f *fakeGitHub) setRole(login, role string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if role == "" {
		delete(f.roles, strings.ToLower(login))
		return
	}
	f.roles[strings.ToLower(login)] = role
}

func (f *fakeGitHub) role(login string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.roles[strings.ToLower(login)]
}

func (f *fakeGitHub) invite(login, perm string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.invites = append(f.invites, fakeInvite{id: f.nextID, login: login, perm: perm})
	return f.nextID
}

func (f *fakeGitHub) pendingInvites(login string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, inv := range f.invites {
		if strings.EqualFold(inv.login, login) {
			n++
		}
	}
	return n
}

// count is how many calls started with prefix ("PUT /repos/...").
func (f *fakeGitHub) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer "+sandboxToken {
		f.t.Errorf("%s %s without the token", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if len(f.replies) > 0 {
		reply := f.replies[0]
		f.replies = f.replies[1:]
		for k, v := range reply.header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(reply.status)
		fmt.Fprint(w, reply.body)
		return
	}
	base := "/repos/" + sandboxRepo
	path, ok := strings.CutPrefix(r.URL.Path, base)
	if !ok {
		f.t.Errorf("%s %s is outside the sandbox repository", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	send := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if v != nil {
			_ = json.NewEncoder(w).Encode(v)
		}
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case r.Method == http.MethodGet && path == "":
		send(200, map[string]any{"full_name": sandboxRepo, "private": f.private})
	case r.Method == http.MethodGet && len(parts) == 3 && parts[0] == "collaborators" && parts[2] == "permission":
		role := f.roles[strings.ToLower(parts[1])]
		if role == "" {
			role = "none"
		}
		send(200, map[string]any{"permission": role, "role_name": role, "user": map[string]any{"login": parts[1]}})
	case r.Method == http.MethodGet && path == "/invitations":
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		if page < 1 || perPage < 1 {
			f.t.Errorf("invitations listed without paging: %s", r.URL.RawQuery)
		}
		out := []map[string]any{}
		for i, inv := range f.invites {
			if i >= (page-1)*perPage && i < page*perPage {
				out = append(out, map[string]any{"id": inv.id, "permissions": inv.perm, "expired": inv.expired,
					"invitee": map[string]any{"login": inv.login}})
			}
		}
		send(200, out)
	case r.Method == http.MethodPut && len(parts) == 2 && parts[0] == "collaborators":
		var body struct{ Permission string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Permission != "pull" {
			f.t.Errorf("PUT collaborator with %+v; want permission pull", body)
		}
		login := parts[1]
		if f.roles[strings.ToLower(login)] != "" || f.members[strings.ToLower(login)] {
			f.roles[strings.ToLower(login)] = "read"
			send(204, nil)
			return
		}
		f.nextID++
		f.invites = append(f.invites, fakeInvite{id: f.nextID, login: login, perm: "read"})
		send(201, map[string]any{"id": f.nextID, "permissions": "read", "invitee": map[string]any{"login": login}})
	case r.Method == http.MethodDelete && len(parts) == 2 && parts[0] == "collaborators":
		delete(f.roles, strings.ToLower(parts[1]))
		if f.afterCollaboratorDelete != nil {
			f.afterCollaboratorDelete(f, parts[1])
		}
		send(204, nil)
	case r.Method == http.MethodDelete && len(parts) == 2 && parts[0] == "invitations":
		id, _ := strconv.ParseInt(parts[1], 10, 64)
		for i, inv := range f.invites {
			if inv.id == id {
				if f.beforeInviteDelete != nil {
					f.beforeInviteDelete(f, inv)
					if !slicesContainsInvite(f.invites, id) {
						send(404, map[string]any{"message": "Not Found"})
						return
					}
				}
				f.invites = append(f.invites[:i], f.invites[i+1:]...)
				send(204, nil)
				return
			}
		}
		send(404, map[string]any{"message": "Not Found"})
	default:
		f.t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
		send(404, map[string]any{"message": "Not Found"})
	}
}

func slicesContainsInvite(invites []fakeInvite, id int64) bool {
	for _, inv := range invites {
		if inv.id == id {
			return true
		}
	}
	return false
}

// ghRig is the runner with the GitHub adapter against fakeGitHub and a real database.
type ghRig struct {
	*rig
	gh     *fakeGitHub
	users  map[string]string
	client *access.GitHubToolClient
}

func newGitHubRig(t *testing.T) *ghRig {
	t.Helper()
	r := newRig(t)
	f := newFakeGitHub(t)
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	users := map[string]string{}
	client := access.NewGitHubToolClient(r.db.App, srv.URL, sandboxRepo, sandboxToken, users, time.Second)
	r.runner = access.NewRunner(r.db.App, client)
	return &ghRig{rig: r, gh: f, users: users, client: client}
}

// grantFor approves an API grant for a new employee mapped to the GitHub login.
func (g *ghRig) grantFor(login string) approvedGrant {
	g.t.Helper()
	a := g.grant("API")
	g.users[strings.ToLower(a.emp.Email)] = login
	return a
}

// active brings a grant to ACTIVE: invitation sent, accepted, verified.
func (g *ghRig) active(login string) approvedGrant {
	g.t.Helper()
	a := g.grantFor(login)
	g.runOnce(true)
	g.gh.accept(login)
	g.due(a.op)
	g.runOnce(true)
	g.requireState(a, "ACTIVE", "SUCCEEDED", 1)
	return a
}

func (g *ghRig) ledger(a approvedGrant) string {
	g.t.Helper()
	var state string
	err := g.db.App.QueryRow(context.Background(), "SELECT state FROM github_access WHERE grant_id = $1", a.id).Scan(&state)
	if err == pgx.ErrNoRows {
		return ""
	}
	if err != nil {
		g.t.Fatal(err)
	}
	return state
}

// inTx runs fn under the employee's lock, as the lifecycle services call access.
func (g *ghRig) inTx(a approvedGrant, fn func(ctx context.Context, tx pgx.Tx, now time.Time) error) {
	g.t.Helper()
	ctx := context.Background()
	if err := platform.InTx(ctx, g.db.App, func(tx pgx.Tx, now time.Time) error {
		if _, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", a.emp.ID); err != nil {
			return err
		}
		return fn(ctx, tx, now)
	}); err != nil {
		g.t.Fatal(err)
	}
}

func (g *ghRig) remove(a approvedGrant) {
	g.inTx(a, func(ctx context.Context, tx pgx.Tx, now time.Time) error {
		return access.Remove(ctx, tx, now, nil, a.id, "REVIEW", "not needed")
	})
}

// lastOp is the grant's newest operation of typ: its status and last error.
func (g *ghRig) lastOp(a approvedGrant, typ string) (status, lastError string) {
	g.t.Helper()
	g.scan("SELECT status, coalesce(last_error, '') FROM operations WHERE grant_id = $1 AND type = $2 ORDER BY id DESC LIMIT 1",
		[]any{a.id, typ}, &status, &lastError)
	return
}

func (g *ghRig) grantStatus(a approvedGrant) string {
	var s string
	g.scan("SELECT status FROM grants WHERE id = $1", []any{a.id}, &s)
	return s
}

func TestGitHubGrantWaitsForTheInvitationThenVerifiesRead(t *testing.T) {
	g := newGitHubRig(t)
	a := g.grantFor("ada-gh")

	g.runOnce(true)
	if g.gh.pendingInvites("ada-gh") != 1 || g.ledger(a) != "INVITED" {
		t.Fatalf("invitations %d, ledger %q; want one invitation, INVITED", g.gh.pendingInvites("ada-gh"), g.ledger(a))
	}
	// Waiting for a person is not a failed attempt: the retry budget is untouched.
	g.requireState(a, "PROVISIONING", "PENDING", 0)
	if n := g.count("SELECT count(*) FROM operations WHERE id = $1 AND next_attempt_at > now() + interval '50 seconds'", a.op); n != 1 {
		t.Fatal("the next check is not about a minute away")
	}
	for range 3 {
		g.due(a.op)
		g.runOnce(true)
	}
	g.requireState(a, "PROVISIONING", "PENDING", 0)
	if put, pending := g.gh.count("PUT "), g.audits("INVITATION_PENDING"); put != 1 || pending != 1 {
		t.Fatalf("%d invitations sent, %d INVITATION_PENDING events; want 1 and 1", put, pending)
	}
	if g.audits("OPERATION_ATTEMPT_FAILED") != 0 {
		t.Fatal("a pending invitation was audited as a failed attempt")
	}

	g.gh.accept("ada-gh")
	g.due(a.op)
	g.runOnce(true)
	g.requireState(a, "ACTIVE", "SUCCEEDED", 1)
	if g.ledger(a) != "ACTIVE" || g.audits("GRANT_ACTIVATED") != 1 {
		t.Fatalf("ledger %q, GRANT_ACTIVATED %d", g.ledger(a), g.audits("GRANT_ACTIVATED"))
	}
}

func TestGitHubGrantAddsAnOrgMemberDirectly(t *testing.T) {
	g := newGitHubRig(t)
	g.gh.members["ada-gh"] = true
	a := g.grantFor("ada-gh")
	g.runOnce(true)
	g.requireState(a, "ACTIVE", "SUCCEEDED", 1)
	if g.ledger(a) != "ACTIVE" || g.gh.role("ada-gh") != "read" {
		t.Fatalf("ledger %q, role %q", g.ledger(a), g.gh.role("ada-gh"))
	}
}

// Access AccessFlow did not grant is never taken over, so it can never be removed by it.
func TestGitHubGrantRefusesAccessThatAlreadyExists(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *fakeGitHub)
	}{
		{"a read collaborator", func(f *fakeGitHub) { f.setRole("ada-gh", "read") }},
		{"a write collaborator", func(f *fakeGitHub) { f.setRole("ada-gh", "write") }},
		{"an invitation sent by hand", func(f *fakeGitHub) { f.invite("ada-gh", "read") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newGitHubRig(t)
			tc.setup(g.gh)
			a := g.grantFor("ada-gh")
			g.runOnce(true)
			g.requireState(a, "FAILED", "FAILED", 1)
			if _, msg := g.lastOp(a, "GRANT"); !strings.Contains(msg, "AccessFlow did not grant") {
				t.Fatalf("error %q", msg)
			}
			if g.gh.count("PUT ") != 0 || g.ledger(a) != "" {
				t.Fatalf("PUT %d, ledger %q; want nothing sent or recorded", g.gh.count("PUT "), g.ledger(a))
			}
		})
	}
}

func TestGitHubGrantForAnUnmappedEmployeeIsRejectedWithoutACall(t *testing.T) {
	g := newGitHubRig(t)
	a := g.grant("API") // no GitHub login mapped
	g.runOnce(true)
	g.requireState(a, "FAILED", "FAILED", 1)
	if len(g.gh.calls) != 0 {
		t.Fatalf("calls %v", g.gh.calls)
	}
}

// A crash after the invitation was sent but before it was recorded never sends a second one.
func TestGitHubGrantRetryAdoptsItsOwnInvitation(t *testing.T) {
	g := newGitHubRig(t)
	a := g.grantFor("ada-gh")
	g.exec(`INSERT INTO github_access (grant_id, repository, account, login, level_id, state) VALUES ($1, $2, $3, 'ada-gh', $4, 'INVITING')`,
		a.id, sandboxRepo, a.emp.Email, a.level)
	id := g.gh.invite("ada-gh", "read")
	g.runOnce(true)
	g.requireState(a, "PROVISIONING", "PENDING", 0)
	var got int64
	g.scan("SELECT invitation_id FROM github_access WHERE grant_id = $1", []any{a.id}, &got)
	if g.gh.count("PUT ") != 0 || got != id || g.ledger(a) != "INVITED" {
		t.Fatalf("PUT %d, invitation %d (want %d), ledger %q", g.gh.count("PUT "), got, id, g.ledger(a))
	}
}

func TestGitHubInvitationNotAcceptedInSevenDaysFails(t *testing.T) {
	g := newGitHubRig(t)
	a := g.grantFor("ada-gh")
	g.runOnce(true)
	g.exec("UPDATE github_access SET invited_at = now() - interval '7 days 1 minute' WHERE grant_id = $1", a.id)
	g.due(a.op)
	g.runOnce(true)
	g.requireState(a, "FAILED", "FAILED", 1)
	if g.gh.pendingInvites("ada-gh") != 0 || g.ledger(a) != "REMOVED" {
		t.Fatalf("invitations %d, ledger %q; want the invitation cancelled", g.gh.pendingInvites("ada-gh"), g.ledger(a))
	}
}

func TestGitHubDeclinedInvitationFails(t *testing.T) {
	g := newGitHubRig(t)
	a := g.grantFor("ada-gh")
	g.runOnce(true)
	g.gh.decline("ada-gh")
	g.due(a.op)
	g.runOnce(true)
	g.requireState(a, "FAILED", "FAILED", 1)
	if g.ledger(a) != "REMOVED" || g.gh.role("ada-gh") != "" {
		t.Fatalf("ledger %q, role %q", g.ledger(a), g.gh.role("ada-gh"))
	}
}

// A Super Admin's retry of a declined invitation invites again (O3 manual retry).
func TestGitHubRetryAfterADeclinedInvitationInvitesAgain(t *testing.T) {
	g := newGitHubRig(t)
	a := g.grantFor("ada-gh")
	g.runOnce(true)
	g.gh.decline("ada-gh")
	g.due(a.op)
	g.runOnce(true)
	g.requireState(a, "FAILED", "FAILED", 1)

	admin := testutil.CreateEmployee(t, g.db, testutil.EmployeeSpec{Roles: []string{"SUPER_ADMIN"}})
	var version int
	g.scan("SELECT version FROM operations WHERE id = $1", []any{a.op}, &version)
	svc := access.NewService(g.db.App, access.NewHTTPToolClient(g.url, time.Second), time.UTC)
	if _, err := svc.Retry(context.Background(), auth.Principal{EmployeeID: admin.ID, Roles: []string{"SUPER_ADMIN"}}, a.op, version); err != nil {
		t.Fatal(err)
	}
	g.runOnce(true)
	if g.gh.pendingInvites("ada-gh") != 1 || g.ledger(a) != "INVITED" {
		t.Fatalf("invitations %d, ledger %q; want a new invitation", g.gh.pendingInvites("ada-gh"), g.ledger(a))
	}
	g.gh.accept("ada-gh")
	g.due(a.op)
	g.runOnce(true)
	g.requireState(a, "ACTIVE", "SUCCEEDED", 1)
}

func TestGitHubRemovalTakesAwayOnlyReadItGranted(t *testing.T) {
	g := newGitHubRig(t)
	a := g.active("ada-gh")
	g.remove(a)
	g.runOnce(true)
	if s, _ := g.lastOp(a, "REVOKE"); s != "SUCCEEDED" || g.grantStatus(a) != "REVOKED" {
		t.Fatalf("REVOKE %s, grant %s", s, g.grantStatus(a))
	}
	if g.gh.role("ada-gh") != "" || g.ledger(a) != "REMOVED" {
		t.Fatalf("role %q, ledger %q", g.gh.role("ada-gh"), g.ledger(a))
	}
}

// A permission someone changed by hand is not AccessFlow's to remove: a person decides.
func TestGitHubRemovalLeavesAChangedPermissionForAPerson(t *testing.T) {
	g := newGitHubRig(t)
	a := g.active("ada-gh")
	g.gh.setRole("ada-gh", "write")
	g.remove(a)
	g.runOnce(true)
	if s, msg := g.lastOp(a, "REVOKE"); s != "FAILED" || !strings.Contains(msg, "changed outside AccessFlow") {
		t.Fatalf("REVOKE %s (%q)", s, msg)
	}
	if g.grantStatus(a) != "REVOCATION_FAILED" || g.gh.role("ada-gh") != "write" || g.gh.count("DELETE ") != 0 {
		t.Fatalf("grant %s, role %q, deletes %d", g.grantStatus(a), g.gh.role("ada-gh"), g.gh.count("DELETE "))
	}
}

// Without a ledger record AccessFlow cannot prove it granted the access: it never removes it.
func TestGitHubRemovalWithoutALedgerRecordIsLeftForAPerson(t *testing.T) {
	g := newGitHubRig(t)
	a := g.active("ada-gh")
	// As if it never recorded it: test-only setup as the owner (the application cannot delete).
	if _, err := g.db.Owner.Exec(context.Background(), "DELETE FROM github_access WHERE grant_id = $1", a.id); err != nil {
		t.Fatal(err)
	}
	g.remove(a)
	g.runOnce(true)
	if s, msg := g.lastOp(a, "REVOKE"); s != "FAILED" || !strings.Contains(msg, "no record") {
		t.Fatalf("REVOKE %s (%q)", s, msg)
	}
	if g.gh.role("ada-gh") != "read" || g.gh.count("DELETE ") != 0 {
		t.Fatalf("role %q, deletes %d; want the access untouched", g.gh.role("ada-gh"), g.gh.count("DELETE "))
	}
}

func TestGitHubRemovalFailsWhileAccessRemainsAnotherWay(t *testing.T) {
	g := newGitHubRig(t)
	a := g.active("ada-gh")
	g.gh.afterCollaboratorDelete = func(f *fakeGitHub, login string) { f.roles[strings.ToLower(login)] = "read" } // a team
	g.remove(a)
	g.runOnce(true)
	if s, msg := g.lastOp(a, "REVOKE"); s != "FAILED" || !strings.Contains(msg, "AccessFlow did not grant") {
		t.Fatalf("REVOKE %s (%q)", s, msg)
	}
	if g.grantStatus(a) != "REVOCATION_FAILED" {
		t.Fatalf("grant %s", g.grantStatus(a))
	}
}

// Removal before acceptance: the waiting GRANT is cancelled at once and the invitation with it.
func TestGitHubRemovalBeforeAcceptanceCancelsTheInvitation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove func(g *ghRig, a approvedGrant)
	}{
		{"revoked", func(g *ghRig, a approvedGrant) { g.remove(a) }},
		{"expired", func(g *ghRig, a approvedGrant) {
			g.exec("UPDATE grants SET expires_on = current_date - 2 WHERE id = $1", a.id)
			if _, err := access.ExpireDue(context.Background(), g.db.App, time.UTC); err != nil {
				g.t.Fatal(err)
			}
		}},
		{"offboarded", func(g *ghRig, a approvedGrant) {
			g.inTx(a, func(ctx context.Context, tx pgx.Tx, now time.Time) error {
				return access.RemoveAll(ctx, tx, now, nil, a.emp.ID)
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newGitHubRig(t)
			a := g.grantFor("ada-gh")
			g.runOnce(true)
			tc.remove(g, a)
			if s, _ := g.lastOp(a, "GRANT"); s != "CANCELLED" {
				t.Fatalf("GRANT %s; want CANCELLED", s)
			}
			g.runOnce(true)
			if s, _ := g.lastOp(a, "REVOKE"); s != "SUCCEEDED" || g.grantStatus(a) != "REVOKED" {
				t.Fatalf("REVOKE %s, grant %s", s, g.grantStatus(a))
			}
			if g.gh.pendingInvites("ada-gh") != 0 || g.gh.role("ada-gh") != "" || g.ledger(a) != "REMOVED" {
				t.Fatalf("invitations %d, role %q, ledger %q", g.gh.pendingInvites("ada-gh"), g.gh.role("ada-gh"), g.ledger(a))
			}
			g.runOnce(false) // nothing re-applies the access
		})
	}
}

// Accepted in the instant before the cancellation: the access it created is removed too.
func TestGitHubRemovalRacingAcceptanceStillRemovesTheAccess(t *testing.T) {
	g := newGitHubRig(t)
	a := g.grantFor("ada-gh")
	g.runOnce(true)
	g.gh.beforeInviteDelete = func(f *fakeGitHub, inv fakeInvite) { f.acceptLocked(inv.login) }
	g.remove(a)
	g.runOnce(true)
	if s, _ := g.lastOp(a, "REVOKE"); s != "SUCCEEDED" || g.gh.role("ada-gh") != "" {
		t.Fatalf("REVOKE %s, role %q", s, g.gh.role("ada-gh"))
	}
}

// Suspension removes the access (GitHub has no suspended state); resuming invites again and
// the grant is ACTIVE only once the new invitation is accepted and Read verified.
func TestGitHubSuspensionRemovesAccessAndResumeInvitesAgain(t *testing.T) {
	g := newGitHubRig(t)
	a := g.active("ada-gh")
	g.inTx(a, func(ctx context.Context, tx pgx.Tx, now time.Time) error {
		return access.SuspendAll(ctx, tx, now, nil, a.emp.ID)
	})
	g.runOnce(true)
	if s, _ := g.lastOp(a, "SUSPEND"); s != "SUCCEEDED" || g.grantStatus(a) != "SUSPENDED" {
		t.Fatalf("SUSPEND %s, grant %s", s, g.grantStatus(a))
	}
	if g.gh.role("ada-gh") != "" || g.ledger(a) != "SUSPENDED" {
		t.Fatalf("role %q, ledger %q; want the access removed", g.gh.role("ada-gh"), g.ledger(a))
	}

	g.inTx(a, func(ctx context.Context, tx pgx.Tx, now time.Time) error {
		return access.ResumeAll(ctx, tx, now, time.UTC, nil, a.emp.ID)
	})
	g.runOnce(true)
	if s, _ := g.lastOp(a, "RESUME"); s != "PENDING" || g.grantStatus(a) != "SUSPENDED" || g.gh.pendingInvites("ada-gh") != 1 {
		t.Fatalf("RESUME %s, grant %s, invitations %d; want a pending invitation", s, g.grantStatus(a), g.gh.pendingInvites("ada-gh"))
	}
	g.gh.accept("ada-gh")
	var resume uuid.UUID
	g.scan("SELECT id FROM operations WHERE grant_id = $1 AND type = 'RESUME'", []any{a.id}, &resume)
	g.due(resume)
	g.runOnce(true)
	if s, _ := g.lastOp(a, "RESUME"); s != "SUCCEEDED" || g.grantStatus(a) != "ACTIVE" || g.ledger(a) != "ACTIVE" {
		t.Fatalf("RESUME %s, grant %s, ledger %q", s, g.grantStatus(a), g.ledger(a))
	}
}

// Offboarding a suspended employee ends the ledger record without touching GitHub again.
func TestGitHubOffboardingASuspendedEmployee(t *testing.T) {
	g := newGitHubRig(t)
	a := g.active("ada-gh")
	g.inTx(a, func(ctx context.Context, tx pgx.Tx, now time.Time) error {
		return access.SuspendAll(ctx, tx, now, nil, a.emp.ID)
	})
	g.runOnce(true)
	g.inTx(a, func(ctx context.Context, tx pgx.Tx, now time.Time) error {
		return access.RemoveAll(ctx, tx, now, nil, a.emp.ID)
	})
	g.runOnce(true)
	if s, _ := g.lastOp(a, "REVOKE"); s != "SUCCEEDED" || g.grantStatus(a) != "REVOKED" || g.ledger(a) != "REMOVED" {
		t.Fatalf("REVOKE %s, grant %s, ledger %q", s, g.grantStatus(a), g.ledger(a))
	}
}

func TestGitHubErrorsAreRetriedOrFinal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		header map[string]string
		final  bool
	}{
		{"server error", 502, nil, false},
		{"rate limited", 429, nil, false},
		{"secondary rate limit", 403, map[string]string{"Retry-After": "60"}, false},
		{"primary rate limit", 403, map[string]string{"X-RateLimit-Remaining": "0"}, false},
		{"forbidden", 403, nil, true},
		{"bad credentials", 401, nil, true},
		{"not found", 404, nil, true},
		{"validation failed", 422, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newGitHubRig(t)
			a := g.grantFor("ada-gh")
			g.gh.failNext(1, tc.status, tc.header, `{"message":"no"}`)
			g.runOnce(true)
			want := "PENDING"
			if tc.final {
				want = "FAILED"
			}
			if s, msg := g.lastOp(a, "GRANT"); s != want || !strings.Contains(msg, "github status "+strconv.Itoa(tc.status)) {
				t.Fatalf("GRANT %s (%q); want %s", s, msg, want)
			}
		})
	}
}

// Even if GitHub echoed the token, it would never be stored, audited or shown.
func TestGitHubTokenNeverReachesErrorsOrAudit(t *testing.T) {
	g := newGitHubRig(t)
	a := g.grantFor("ada-gh")
	g.gh.failNext(1, 500, nil, `{"message":"bad header Bearer `+sandboxToken+`"}`)
	g.runOnce(true)
	if _, msg := g.lastOp(a, "GRANT"); strings.Contains(msg, sandboxToken) || !strings.Contains(msg, "[redacted]") {
		t.Fatalf("error %q", msg)
	}
	if n := g.count("SELECT count(*) FROM audit_events WHERE details::text LIKE '%' || $1 || '%'", sandboxToken); n != 0 {
		t.Fatalf("%d audit events contain the token", n)
	}
}

func TestGitHubCheckRepository(t *testing.T) {
	g := newGitHubRig(t)
	if err := g.client.CheckRepository(context.Background()); err != nil {
		t.Fatalf("sandbox: %v", err)
	}
	g.gh.private = false
	if err := g.client.CheckRepository(context.Background()); err == nil {
		t.Fatal("a public repository was accepted")
	}
	g.gh.failNext(1, 404, nil, `{"message":"Not Found"}`)
	if err := g.client.CheckRepository(context.Background()); err == nil {
		t.Fatal("an unreachable repository was accepted")
	}
}

// GITHUB_TOOL_ID must name an existing API tool: the runner handles only API operations.
func TestGitHubCheckTool(t *testing.T) {
	g := newGitHubRig(t)
	ctx := context.Background()
	api := testutil.CreateTool(t, g.db, testutil.ToolSpec{Method: "API"})
	manual := testutil.CreateTool(t, g.db, testutil.ToolSpec{Method: "MANUAL"})
	if err := g.client.CheckTool(ctx, api); err != nil {
		t.Fatalf("API tool: %v", err)
	}
	if err := g.client.CheckTool(ctx, manual); err == nil {
		t.Fatal("a Manual tool was accepted")
	}
	if err := g.client.CheckTool(ctx, uuid.New()); err == nil {
		t.Fatal("a missing tool was accepted")
	}
}

func TestRouteToolSendsOneToolToGitHub(t *testing.T) {
	gh, mock := &recordingClient{}, &recordingClient{}
	tool := uuid.New()
	c := access.RouteTool(tool, gh, mock)
	if _, err := c.Apply(context.Background(), access.ToolOperation{Tool: tool.String()}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Account(context.Background(), uuid.NewString(), "x@y"); err != nil {
		t.Fatal(err)
	}
	if gh.calls != 1 || mock.calls != 1 {
		t.Fatalf("github %d, mock %d calls; want 1 and 1", gh.calls, mock.calls)
	}
}

type recordingClient struct{ calls int }

func (c *recordingClient) Apply(context.Context, access.ToolOperation) (access.ToolResult, error) {
	c.calls++
	return access.ToolResult{Result: "APPLIED"}, nil
}

func (c *recordingClient) Account(context.Context, string, string) (access.ToolAccount, error) {
	c.calls++
	return access.ToolAccount{Levels: []access.ToolLevel{}}, nil
}
