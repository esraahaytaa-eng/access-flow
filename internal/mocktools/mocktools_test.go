package mocktools_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/mocktools"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

type mock struct {
	t    *testing.T
	pool *pgxpool.Pool
	srv  *httptest.Server
}

func newMock(t *testing.T) *mock {
	pool := testutil.NewMockToolsDB(t, mocktools.Migrations)
	srv := httptest.NewServer(mocktools.Handler(pool))
	t.Cleanup(srv.Close)
	return &mock{t, pool, srv}
}

func (m *mock) post(body string) (int, map[string]any) {
	m.t.Helper()
	resp, err := http.Post(m.srv.URL+"/v1/operations", "application/json", bytes.NewBufferString(body))
	if err != nil {
		m.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func (m *mock) op(id uuid.UUID, typ, account, level string) (int, map[string]any) {
	m.t.Helper()
	b, _ := json.Marshal(map[string]any{"operationId": id, "type": typ, "tool": "jira", "account": account, "level": level})
	return m.post(string(b))
}

// apply sends an operation that must succeed and returns its result.
func (m *mock) apply(typ, account, level string) string {
	m.t.Helper()
	status, body := m.op(uuid.New(), typ, account, level)
	if status != 200 {
		m.t.Fatalf("%s %s/%s: status %d %v", typ, account, level, status, body)
	}
	return body["result"].(string)
}

// levels returns "level=STATUS" pairs for the account, and whether it exists.
func (m *mock) levels(account string) (bool, []string) {
	m.t.Helper()
	resp, err := http.Get(m.srv.URL + "/v1/tools/jira/accounts/" + url.PathEscape(account))
	if err != nil {
		m.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		m.t.Fatalf("lookup status %d", resp.StatusCode)
	}
	var body struct {
		Tool, Account string
		Exists        bool
		Levels        []struct{ Level, Status string }
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		m.t.Fatal(err)
	}
	if body.Tool != "jira" || body.Account != account {
		m.t.Fatalf("lookup echoed %s/%s", body.Tool, body.Account)
	}
	out := []string{}
	for _, l := range body.Levels {
		out = append(out, l.Level+"="+l.Status)
	}
	return body.Exists, out
}

func (m *mock) count(sql string) int {
	m.t.Helper()
	var n int
	if err := m.pool.QueryRow(m.t.Context(), sql).Scan(&n); err != nil {
		m.t.Fatal(err)
	}
	return n
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestGrantCreatesAccountAndLevel(t *testing.T) {
	m := newMock(t)
	if got := m.apply("GRANT", "ada@example.com", "viewer"); got != "APPLIED" {
		t.Fatalf("result = %s", got)
	}
	if exists, levels := m.levels("ada@example.com"); !exists || !equal(levels, []string{"viewer=ACTIVE"}) {
		t.Fatalf("exists %v levels %v", exists, levels)
	}
}

func TestRevokeKeepsOtherLevelsAndTheAccount(t *testing.T) {
	m := newMock(t)
	m.apply("GRANT", "ada@example.com", "admin")
	m.apply("GRANT", "ada@example.com", "viewer")
	m.apply("REVOKE", "ada@example.com", "admin")
	if exists, levels := m.levels("ada@example.com"); !exists || !equal(levels, []string{"viewer=ACTIVE"}) {
		t.Fatalf("exists %v levels %v; want the account with viewer only", exists, levels)
	}
	m.apply("REVOKE", "ada@example.com", "viewer")
	if exists, levels := m.levels("ada@example.com"); !exists || len(levels) != 0 {
		t.Fatalf("exists %v levels %v; want the account kept with no levels", exists, levels)
	}
}

func TestSuspendAndResumeOneLevel(t *testing.T) {
	m := newMock(t)
	m.apply("GRANT", "ada@example.com", "admin")
	m.apply("GRANT", "ada@example.com", "viewer")
	m.apply("SUSPEND", "ada@example.com", "viewer")
	if _, levels := m.levels("ada@example.com"); !equal(levels, []string{"admin=ACTIVE", "viewer=SUSPENDED"}) {
		t.Fatalf("levels %v", levels)
	}
	if got := m.apply("SUSPEND", "ada@example.com", "viewer"); got != "NO_CHANGE" {
		t.Fatalf("second suspend = %s", got)
	}
	m.apply("RESUME", "ada@example.com", "viewer")
	if _, levels := m.levels("ada@example.com"); !equal(levels, []string{"admin=ACTIVE", "viewer=ACTIVE"}) {
		t.Fatalf("levels %v", levels)
	}
	for _, typ := range []string{"SUSPEND", "RESUME"} {
		if status, body := m.op(uuid.New(), typ, "ada@example.com", "editor"); status != 409 || body["error"] != "not_granted" {
			t.Fatalf("%s of a level not held: %d %v", typ, status, body)
		}
	}
}

func TestIdempotentDesiredState(t *testing.T) {
	m := newMock(t)
	m.apply("GRANT", "ada@example.com", "viewer")
	if got := m.apply("GRANT", "ada@example.com", "viewer"); got != "NO_CHANGE" {
		t.Fatalf("grant of a held level = %s", got)
	}
	if got := m.apply("REVOKE", "bob@example.com", "viewer"); got != "NO_CHANGE" {
		t.Fatalf("revoke of an absent level = %s", got)
	}
	m.apply("SUSPEND", "ada@example.com", "viewer")
	if status, body := m.op(uuid.New(), "GRANT", "ada@example.com", "viewer"); status != 409 || body["error"] != "level_suspended" {
		t.Fatalf("grant of a suspended level: %d %v; want 409 level_suspended", status, body)
	}
}

func TestRepeatedOperationIDHasNoExtraEffect(t *testing.T) {
	m := newMock(t)
	grant := uuid.New()
	_, first := m.op(grant, "GRANT", "ada@example.com", "viewer")
	status, again := m.op(grant, "GRANT", "ada@example.com", "viewer")
	if status != 200 || again["result"] != first["result"] || first["result"] != "APPLIED" {
		t.Fatalf("repeat: %d %v; first %v", status, again, first)
	}
	m.apply("REVOKE", "ada@example.com", "viewer")
	// A late duplicate of the original grant returns its stored result and does not re-grant.
	if status, late := m.op(grant, "GRANT", "ada@example.com", "viewer"); status != 200 || late["result"] != "APPLIED" {
		t.Fatalf("late repeat: %d %v", status, late)
	}
	if _, levels := m.levels("ada@example.com"); len(levels) != 0 {
		t.Fatalf("levels %v; the repeated grant must not apply again", levels)
	}
	if n := m.count("SELECT count(*) FROM operations"); n != 2 {
		t.Fatalf("operations = %d; want 2", n)
	}
}

func TestReusedOperationIDWithDifferentContent(t *testing.T) {
	m := newMock(t)
	id := uuid.New()
	m.op(id, "GRANT", "ada@example.com", "viewer")
	if status, body := m.op(id, "GRANT", "ada@example.com", "admin"); status != 409 || body["error"] != "operation_id_reused" {
		t.Fatalf("reused id: %d %v", status, body)
	}
	if _, levels := m.levels("ada@example.com"); !equal(levels, []string{"viewer=ACTIVE"}) {
		t.Fatalf("levels %v", levels)
	}
}

func TestConcurrentSameOperation(t *testing.T) {
	m := newMock(t)
	id := uuid.New()
	var wg sync.WaitGroup
	results := make(chan string, 10)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, body := m.op(id, "GRANT", "ada@example.com", "viewer")
			results <- fmt.Sprint(status, "/", body["result"], body["error"])
		}()
	}
	wg.Wait()
	close(results)
	for r := range results {
		if r != "200/APPLIED<nil>" {
			t.Fatalf("a concurrent repeat got %s; want the stored APPLIED", r)
		}
	}
	if n := m.count("SELECT count(*) FROM permissions"); n != 1 {
		t.Fatalf("permissions = %d", n)
	}
}

func TestConcurrentGrantsOfOneLevel(t *testing.T) {
	m := newMock(t)
	var wg sync.WaitGroup
	results := make(chan string, 10)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, body := m.op(uuid.New(), "GRANT", "ada@example.com", "viewer")
			results <- fmt.Sprint(status, "/", body["result"], body["error"])
		}()
	}
	wg.Wait()
	close(results)
	got := map[string]int{}
	for r := range results {
		got[r]++
	}
	if got["200/APPLIED<nil>"] != 1 || got["200/NO_CHANGE<nil>"] != 9 {
		t.Fatalf("results %v; want one APPLIED and nine NO_CHANGE", got)
	}
}

func TestInvalidOperations(t *testing.T) {
	m := newMock(t)
	long := string(bytes.Repeat([]byte("a"), 255))
	id := uuid.NewString()
	for name, body := range map[string]string{
		"missing account":  `{"operationId":"` + id + `","type":"GRANT","tool":"jira","level":"viewer"}`,
		"unknown type":     `{"operationId":"` + id + `","type":"DELETE","tool":"jira","account":"a","level":"viewer"}`,
		"non-UUID id":      `{"operationId":"op-1","type":"GRANT","tool":"jira","account":"a","level":"viewer"}`,
		"missing id":       `{"type":"GRANT","tool":"jira","account":"a","level":"viewer"}`,
		"account too long": `{"operationId":"` + id + `","type":"GRANT","tool":"jira","account":"` + long + `","level":"viewer"}`,
		"trailing JSON":    `{"operationId":"` + id + `","type":"GRANT","tool":"jira","account":"a","level":"viewer"} {}`,
		"unknown field":    `{"operationId":"` + id + `","type":"GRANT","tool":"jira","account":"a","level":"viewer","force":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			if status, _ := m.post(body); status != 400 {
				t.Fatalf("status %d; want 400", status)
			}
		})
	}
	if n := m.count("SELECT count(*) FROM operations"); n != 0 {
		t.Fatalf("operations = %d", n)
	}
}

func TestUnknownAccountLookup(t *testing.T) {
	m := newMock(t)
	if exists, levels := m.levels("nobody@example.com"); exists || len(levels) != 0 {
		t.Fatalf("exists %v levels %v", exists, levels)
	}
}

func TestAccountNamesWithURLCharacters(t *testing.T) {
	m := newMock(t)
	odd := "a/b?c#d%e@x.com"
	m.apply("GRANT", odd, "viewer")
	if exists, levels := m.levels(odd); !exists || !equal(levels, []string{"viewer=ACTIVE"}) {
		t.Fatalf("exists %v levels %v", exists, levels)
	}
}
