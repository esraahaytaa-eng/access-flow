package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/demo"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
	"github.com/go-chi/chi/v5"
)

// testRouter serves dist; demo accounts turn on demo mode.
func testRouter(t *testing.T, dist fstest.MapFS, demoAccounts ...demo.Account) http.Handler {
	t.Helper()
	db := testutil.NewDB(t)
	svc, err := auth.NewService(db.App, auth.Options{Secret: []byte(strings.Repeat("k", 32)), BootstrapFile: t.TempDir() + "/admin.txt", LoginRateLimit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	return newRouter(db.App, svc, time.UTC, access.NewHTTPToolClient("http://127.0.0.1:1", time.Second), dist, demoAccounts)
}

func TestRouter(t *testing.T) {
	router := testRouter(t, fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}})

	cases := []struct {
		path, wantType, wantBody string
		wantStatus               int
	}{
		{"/api/v1/health", "application/json", `{"status":"ok"}`, http.StatusOK},
		{"/api/v1/auth/me", "application/json", `{"error":"authentication_required"}`, http.StatusUnauthorized},
		{"/api/v1/employees", "application/json", `{"error":"authentication_required"}`, http.StatusUnauthorized},
		{"/api/v1/tools", "application/json", `{"error":"authentication_required"}`, http.StatusUnauthorized},
		{"/api/v1/notifications", "application/json", `{"error":"authentication_required"}`, http.StatusUnauthorized},
		{"/api/v1/requests", "application/json", `{"error":"authentication_required"}`, http.StatusUnauthorized},
		{"/api/v1/approvals", "application/json", `{"error":"authentication_required"}`, http.StatusUnauthorized},
		{"/api/v1/access", "application/json", `{"error":"authentication_required"}`, http.StatusUnauthorized},
		{"/api/v1/exceptions", "application/json", `{"error":"authentication_required"}`, http.StatusUnauthorized},
		{"/api/v1/fulfillment-tasks", "application/json", `{"error":"authentication_required"}`, http.StatusUnauthorized},
		// Unknown API paths must never fall through to the web page.
		{"/api/v1/nope", "application/json", `{"error":"not_found"}`, http.StatusNotFound},
		{"/api/v1/demo", "application/json", `{"error":"not_found"}`, http.StatusNotFound}, // demo mode off
		{"/api/nope", "application/json", `{"error":"not_found"}`, http.StatusNotFound},
		{"/", "text/html; charset=utf-8", "<html>app</html>", http.StatusOK},
		{"/any/route", "text/html; charset=utf-8", "<html>app</html>", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d; want %d", rec.Code, tc.wantStatus)
			}
			if ct := rec.Header().Get("Content-Type"); ct != tc.wantType {
				t.Errorf("Content-Type = %q; want %q", ct, tc.wantType)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tc.wantBody {
				t.Errorf("body = %q; want %q", got, tc.wantBody)
			}
			// Regression (security F-4): no framing, no MIME sniffing, no referrer.
			for h, want := range map[string]string{"X-Frame-Options": "DENY", "Content-Security-Policy": "frame-ancestors 'none'",
				"X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer"} {
				if got := rec.Header().Get(h); got != want {
					t.Errorf("%s = %q; want %q", h, got, want)
				}
			}
		})
	}
}

// In demo mode the sign-in page lists the demo accounts, without any password (A4 note 2026-10-29).
func TestRouterListsTheDemoAccounts(t *testing.T) {
	rec := httptest.NewRecorder()
	testRouter(t, fstest.MapFS{}, demo.Accounts...).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/demo", nil))

	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `"email":"admin@accessflow.local"`) || !strings.Contains(body, `"role":"Employee"`) {
		t.Fatalf("status = %d %s; want 200 with the accounts", rec.Code, body)
	}
	if strings.Contains(strings.ToLower(body), "pass") {
		t.Fatalf("the demo listing mentions passwords: %s", body)
	}
}

// R-7: the API documentation is served only in the local demo setup; elsewhere it is not found.
func TestSwaggerIsServedOnlyInDemoMode(t *testing.T) {
	dist := fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")},
		"swagger/index.html": {Data: []byte("<html>swagger</html>")}, "swagger/openapi.json": {Data: []byte("{}")}}
	for _, demoMode := range []bool{false, true} {
		var accounts []demo.Account
		if demoMode {
			accounts = demo.Accounts
		}
		router := testRouter(t, dist, accounts...)
		for _, path := range []string{"/swagger", "/swagger/", "/swagger/index.html", "/swagger/openapi.json"} {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			body := rec.Body.String()
			served := rec.Code < 400 && !strings.Contains(body, "app")
			if served != demoMode || (!demoMode && rec.Code != http.StatusNotFound) {
				t.Errorf("demo=%v %s: %d %q", demoMode, path, rec.Code, body)
			}
		}
	}
}

// The change route (M8) exists and needs a session.
func TestRouterRequiresASessionToChangeAGrant(t *testing.T) {
	rec := httptest.NewRecorder()
	testRouter(t, fstest.MapFS{}).ServeHTTP(rec,
		httptest.NewRequest(http.MethodPost, "/api/v1/grants/01a18416-8264-781d-97fb-a3768f60d41d/change", strings.NewReader("{}")))
	if rec.Code != http.StatusUnauthorized || strings.TrimSpace(rec.Body.String()) != `{"error":"authentication_required"}` {
		t.Fatalf("status = %d %s; want 401", rec.Code, rec.Body)
	}
}

func TestRouterAnswersWrongMethodsOnTheAPIWithJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	testRouter(t, fstest.MapFS{}).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/health", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d; want 405", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", ct)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":"method_not_allowed"}` {
		t.Errorf("body = %q", got)
	}
}

// A4: browsers mark cross-site requests; the API rejects unsafe ones before any handler runs.
func TestRouterRejectsCrossOriginWrites(t *testing.T) {
	router := testRouter(t, fstest.MapFS{})
	body := `{"email":"a@example.com","password":"whatever-password"}`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || strings.TrimSpace(rec.Body.String()) != `{"error":"cross_origin_request"}` {
		t.Fatalf("cross-site login: %d %s; want 403 cross_origin_request", rec.Code, rec.Body)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("same-origin login: %d %s; want 401 (reaches the handler)", rec.Code, rec.Body)
	}
}

// The OpenAPI document behind the Swagger UI (/swagger/) lists exactly the API's routes,
// so it cannot document a route that does not exist or miss a new one, and its references resolve.
func TestOpenAPIDocumentMatchesTheRoutes(t *testing.T) {
	raw, err := os.ReadFile("../../web/public/swagger/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Servers []struct{ URL string }                `json:"servers"`
		Paths   map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("openapi.json is not valid JSON: %v", err)
	}
	if len(spec.Servers) != 1 || spec.Servers[0].URL != "/api/v1" {
		t.Fatalf("servers = %+v; want one, /api/v1", spec.Servers)
	}
	var documented []string
	for p, item := range spec.Paths {
		for method := range item {
			if method != "parameters" {
				documented = append(documented, strings.ToUpper(method)+" /api/v1"+p)
			}
		}
	}

	var routes []string
	err = chi.Walk(testRouter(t, fstest.MapFS{}, demo.Accounts...).(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/api/") {
			routes = append(routes, method+" "+strings.TrimSuffix(strings.ReplaceAll(route, "/*/", "/"), "/"))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(documented)
	slices.Sort(routes)
	for _, r := range routes {
		if !slices.Contains(documented, r) {
			t.Errorf("route not documented: %s", r)
		}
	}
	for _, d := range documented {
		if !slices.Contains(routes, d) {
			t.Errorf("documented route does not exist: %s", d)
		}
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`"\$ref":\s*"#/components/(\w+)/(\w+)"`).FindAllStringSubmatch(string(raw), -1) {
		if group, _ := doc["components"].(map[string]any)[m[1]].(map[string]any); group[m[2]] == nil {
			t.Errorf("unresolved $ref #/components/%s/%s", m[1], m[2])
		}
	}
}

// The distroless image has no cat, so the admin reads the password through the binary.
func TestPrintBootstrapPassword(t *testing.T) {
	file := filepath.Join(t.TempDir(), "admin.txt")
	if err := printBootstrapPassword(file, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "already changed") {
		t.Fatalf("missing file: err = %v; want it to say the password was already changed", err)
	}
	if err := os.WriteFile(file, []byte("password: abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := printBootstrapPassword(file, &out); err != nil || out.String() != "password: abc\n" {
		t.Fatalf("got %q, %v; want the file contents", out.String(), err)
	}
}

// An Auditor is an external, read-only auditor (DECISIONS.md O7 notes 2026-10-31): every
// business route but the audit events, point-in-time access and approval evidence refuses
// them, whatever other roles they also hold.
func TestAuditorReachesOnlyTheAuditEvidence(t *testing.T) {
	db := testutil.NewDB(t)
	svc, err := auth.NewService(db.App, auth.Options{Secret: []byte(strings.Repeat("k", 32)), BootstrapFile: t.TempDir() + "/admin.txt", LoginRateLimit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	router := newRouter(db.App, svc, time.UTC, access.NewHTTPToolClient("http://127.0.0.1:1", time.Second), fstest.MapFS{}, demo.Accounts)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	signIn := func(roles ...string) *testutil.Client {
		e := testutil.CreateEmployee(t, db, testutil.EmployeeSpec{Roles: roles, Password: true})
		c := testutil.NewClient(t, srv)
		testutil.RequireStatus(t, c.Login(e.Email, testutil.Password), http.StatusOK, "")
		return c
	}
	allowed := map[string]bool{"GET /api/v1/audit/events": true, "GET /api/v1/audit/events/export": true, "GET /api/v1/audit/filters": true,
		"GET /api/v1/audit/access": true, "GET /api/v1/audit/requests": true}
	var routes []string
	err = chi.Walk(router.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(strings.ReplaceAll(route, "/*/", "/"), "/")
		if strings.HasPrefix(route, "/api/v1/") && !strings.HasPrefix(route, "/api/v1/auth/") && route != "/api/v1/health" && route != "/api/v1/demo" {
			routes = append(routes, method+" "+route)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) < 60 {
		t.Fatalf("walked only %d routes", len(routes))
	}
	param := regexp.MustCompile(`\{[^}]+\}`)
	for _, c := range []*testutil.Client{signIn("AUDITOR"), signIn("AUDITOR", "SUPER_ADMIN", "INFOSEC", "HR_MANAGER")} {
		for _, rt := range routes {
			method, path, _ := strings.Cut(rt, " ")
			r := c.Do(method, param.ReplaceAllString(path, "00000000-0000-4000-8000-000000000001"), map[string]any{})
			if allowed[rt] {
				if r.Status != http.StatusOK {
					t.Errorf("%s: %d %s; want 200", rt, r.Status, r.Raw)
				}
			} else if r.Status != http.StatusForbidden || r.Body["error"] != "forbidden" {
				t.Errorf("%s: %d %s; want 403 forbidden", rt, r.Status, r.Raw)
			}
		}
		testutil.RequireStatus(t, c.Do(http.MethodGet, "/api/v1/auth/me", nil), http.StatusOK, "")
		testutil.RequireStatus(t, c.Do(http.MethodPost, "/api/v1/auth/logout", nil), http.StatusNoContent, "")
		testutil.RequireStatus(t, c.Do(http.MethodGet, "/api/v1/audit/events", nil), http.StatusUnauthorized, "authentication_required")
	}
	// Other roles keep their routes.
	admin := signIn("SUPER_ADMIN")
	for _, p := range []string{"/api/v1/exceptions", "/api/v1/employees", "/api/v1/audit/reports/privileged", "/api/v1/audit/access"} {
		testutil.RequireStatus(t, admin.Do(http.MethodGet, p, nil), http.StatusOK, "")
	}
}
