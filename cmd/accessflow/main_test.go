package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func testRouter(t *testing.T, dist fstest.MapFS) http.Handler {
	t.Helper()
	db := testutil.NewDB(t)
	svc, err := auth.NewService(db.App, auth.Options{Secret: []byte(strings.Repeat("k", 32)), BootstrapFile: t.TempDir() + "/admin.txt"})
	if err != nil {
		t.Fatal(err)
	}
	return newRouter(db.App, svc, time.UTC, dist)
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
		})
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
