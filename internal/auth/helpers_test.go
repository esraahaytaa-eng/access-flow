package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

const goodPassword = "correct horse battery"

// testEnv is a real database plus an HTTP server mounting the auth routes and three
// probe routes standing in for later business endpoints.
type testEnv struct {
	t    *testing.T
	db   testutil.DB
	svc  *Service
	srv  *httptest.Server
	logs *bytes.Buffer
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	db := testutil.NewDB(t)
	logs := &bytes.Buffer{}
	svc, err := NewService(db.App, Options{
		Secret:        testSecret,
		BootstrapFile: filepath.Join(t.TempDir(), "bootstrap-admin.txt"),
		Logger:        slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	ok := func(w http.ResponseWriter, _ *http.Request) {
		platform.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
	r := chi.NewRouter()
	r.Mount("/api/v1/auth", svc.Routes())
	r.With(svc.RequireSession).Get("/api/v1/probe", ok)
	r.With(svc.RequireSession).Post("/api/v1/probe", ok)
	r.With(svc.RequireSession, RequireRole("SUPER_ADMIN")).Get("/api/v1/probe/admin", ok)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &testEnv{t: t, db: db, svc: svc, srv: srv, logs: logs}
}

type employee struct {
	ID       uuid.UUID
	Email    string
	Password string
}

// employee creates an active employee with a permanent password.
func (e *testEnv) employee(roles ...string) employee {
	e.t.Helper()
	emp := testutil.CreateEmployee(e.t, e.db, testutil.EmployeeSpec{Roles: roles})
	e.setPassword(emp.ID, goodPassword, false, nil)
	return employee{ID: emp.ID, Email: emp.Email, Password: goodPassword}
}

func (e *testEnv) setPassword(id uuid.UUID, pw string, temporary bool, expiresAt *time.Time) {
	e.t.Helper()
	hash, err := HashPassword(pw)
	if err != nil {
		e.t.Fatal(err)
	}
	_, err = e.db.App.Exec(context.Background(), `INSERT INTO password_credentials (employee_id, password_hash, is_temporary, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (employee_id) DO UPDATE SET password_hash = $2, is_temporary = $3, expires_at = $4`,
		id, hash, temporary, expiresAt)
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.db.App.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

func (e *testEnv) auditCount(action string, subject uuid.UUID) int {
	e.t.Helper()
	var n int
	err := e.db.App.QueryRow(context.Background(),
		"SELECT count(*) FROM audit_events WHERE action = $1 AND subject_employee_id IS NOT DISTINCT FROM $2",
		action, nullable(subject)).Scan(&n)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func nullable(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// client is a browser-like client: it keeps cookies and sends the CSRF header.
type client struct {
	env  *testEnv
	http *http.Client
	csrf bool
}

func (e *testEnv) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{env: e, http: &http.Client{Jar: jar}, csrf: true}
}

type response struct {
	status int
	body   map[string]any
	header http.Header
}

func (c *client) do(method, path string, body any) response {
	c.env.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.env.srv.URL+path, r)
	if c.csrf {
		if v := c.cookie(csrfCookie); v != "" {
			req.Header.Set("X-CSRF-Token", v)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.env.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := response{status: resp.StatusCode, header: resp.Header}
	_ = json.NewDecoder(resp.Body).Decode(&out.body)
	return out
}

func (c *client) cookie(name string) string {
	u, _ := url.Parse(c.env.srv.URL)
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == name {
			return ck.Value
		}
	}
	return ""
}

func (c *client) setCookie(name, value string) {
	u, _ := url.Parse(c.env.srv.URL)
	c.http.Jar.SetCookies(u, []*http.Cookie{{Name: name, Value: value, Path: "/"}})
}

func (c *client) login(email, password string) response {
	c.env.t.Helper()
	return c.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": password})
}

func requireStatus(t *testing.T, r response, status int, code string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status = %d (body %v); want %d", r.status, r.body, status)
	}
	if code != "" && r.body["error"] != code {
		t.Fatalf("error = %v; want %q", r.body["error"], code)
	}
}
