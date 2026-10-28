package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func TestRouter(t *testing.T) {
	db := testutil.NewDB(t)
	dist := fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}}
	router := newRouter(db.App, dist)

	cases := []struct {
		path, wantType, wantBody string
		wantStatus               int
	}{
		{"/api/v1/health", "application/json", `{"status":"ok"}`, http.StatusOK},
		// Unknown API paths must never fall through to the web page.
		{"/api/v1/nope", "application/json", `{"error":"not found"}`, http.StatusNotFound},
		{"/api/nope", "application/json", `{"error":"not found"}`, http.StatusNotFound},
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
	db := testutil.NewDB(t)
	rec := httptest.NewRecorder()
	newRouter(db.App, fstest.MapFS{}).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/health", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d; want 405", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", ct)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":"method not allowed"}` {
		t.Errorf("body = %q", got)
	}
}
