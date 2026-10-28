package platform_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

func TestHealthHandler(t *testing.T) {
	cases := []struct {
		name       string
		ping       error
		wantStatus int
		wantBody   string
	}{
		{"database reachable", nil, http.StatusOK, `{"status":"ok"}`},
		{"database down", errors.New("connection refused"), http.StatusServiceUnavailable, `{"error":"database unavailable"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := platform.HealthHandler(func(context.Context) error { return tc.ping })
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
			assertJSON(t, rec, tc.wantStatus, tc.wantBody)
		})
	}
}

func TestWriteErrorMapsTypedErrorsAndHidesInternalDetails(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{"not found", fmt.Errorf("load grant: %w", platform.ErrNotFound), http.StatusNotFound, `{"error":"not found"}`},
		{"anything else", errors.New("pq: password=secret failed"), http.StatusInternalServerError, `{"error":"internal error"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			platform.WriteError(rec, httptest.NewRequest(http.MethodGet, "/x", nil), tc.err)
			assertJSON(t, rec, tc.wantStatus, tc.wantBody)
		})
	}
}

func TestHealthcheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ok.Close()
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer down.Close()
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()

	// The probe gets the listen address, e.g. ":8080" or "0.0.0.0:8080", and calls localhost.
	if err := platform.Healthcheck(":"+port(t, ok.URL), "/health"); err != nil {
		t.Errorf("healthy server: %v", err)
	}
	if err := platform.Healthcheck("0.0.0.0:"+port(t, ok.URL), "/health"); err != nil {
		t.Errorf("healthy server, host in address: %v", err)
	}
	if err := platform.Healthcheck(":"+port(t, ok.URL), "/other"); err == nil {
		t.Error("404: want an error")
	}
	if err := platform.Healthcheck(":"+port(t, down.URL), "/health"); err == nil {
		t.Error("503: want an error")
	}
	if err := platform.Healthcheck(":"+port(t, gone.URL), "/health"); err == nil {
		t.Error("unreachable: want an error")
	}
}

func TestServeShutsDownWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()}
	done := make(chan error, 1)
	go func() { done <- platform.Serve(ctx, srv) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve = %v; want nil after shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after the context was cancelled")
	}
}

func port(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Port()
}

func assertJSON(t *testing.T, rec *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	if rec.Code != status {
		t.Errorf("status = %d; want %d", rec.Code, status)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", ct)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != body {
		t.Errorf("body = %s; want %s", got, body)
	}
}
