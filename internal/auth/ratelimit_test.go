package auth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClientKey(t *testing.T) {
	cases := []struct{ addr, want string }{
		{"192.0.2.1:5000", "192.0.2.1"},
		{"[2001:db8:1:2:3::4]:5000", "2001:db8:1:2::/64"},
		{"[2001:db8:1:2:ffff::1]:1", "2001:db8:1:2::/64"}, // the same /64
		{"[::ffff:192.0.2.1]:80", "192.0.2.1"},
		{"not an address", "not an address"},
	}
	for _, tc := range cases {
		if got := clientKey(tc.addr); got != tc.want {
			t.Errorf("clientKey(%q) = %q; want %q", tc.addr, got, tc.want)
		}
	}
}

func TestAttemptLimiter(t *testing.T) {
	l := newAttemptLimiter(10)
	for i := range 10 {
		if ok, _, _ := l.allow("a", t0); !ok {
			t.Fatalf("attempt %d denied; want 10 allowed", i+1)
		}
	}
	ok, retry, first := l.allow("a", t0.Add(20*time.Second))
	if ok || retry != 40*time.Second || !first {
		t.Fatalf("11th: ok=%v retry=%v first=%v; want denied, 40s, first denial", ok, retry, first)
	}
	if _, _, first := l.allow("a", t0.Add(21*time.Second)); first {
		t.Error("12th reported as the first denial again; want one warning per window")
	}
	if ok, _, _ := l.allow("b", t0); !ok {
		t.Error("another address denied")
	}
	if ok, _, _ := l.allow("a", t0.Add(time.Minute)); !ok {
		t.Error("denied after the window ended")
	}
}

func TestAttemptLimiterIsBounded(t *testing.T) {
	l := newAttemptLimiter(10)
	for i := range maxTrackedAddresses {
		l.allow(fmt.Sprint(i), t0)
	}
	if ok, retry, _ := l.allow("new", t0.Add(time.Second)); ok || retry <= 0 {
		t.Fatalf("new address while full of live windows: ok=%v retry=%v; want refused (fail closed)", ok, retry)
	}
	if ok, _, _ := l.allow("new", t0.Add(time.Minute)); !ok {
		t.Fatal("new address refused after the old windows expired; want them pruned")
	}
	if n := len(l.windows); n != 1 {
		t.Errorf("tracked addresses = %d; want 1 after pruning", n)
	}
}

func TestLoginIsRateLimitedPerAddress(t *testing.T) {
	env := newEnv(t)
	emp := env.employee()
	// A small limit: 60 hashed attempts can outlast the one-minute window on a loaded machine.
	const limit = 5
	svc, err := NewService(env.db.App, Options{Secret: testSecret, BootstrapFile: filepath.Join(t.TempDir(), "b.txt"), LoginRateLimit: limit})
	if err != nil {
		t.Fatal(err)
	}
	routes := svc.Routes()
	login := func(addr, forwarded, email, password string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)
		r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
		r.RemoteAddr = addr
		r.Header.Set("Content-Type", "application/json")
		if forwarded != "" {
			r.Header.Set("X-Forwarded-For", forwarded)
		}
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, r)
		return w
	}
	for i := range limit {
		// An unknown email, so the account lockout stays out of the way. A spoofed
		// X-Forwarded-For must not give each attempt a fresh budget.
		if w := login("192.0.2.1:1000", fmt.Sprintf("198.51.100.%d", i), "nobody@x.com", "wrong password!"); w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status %d; want 401", i+1, w.Code)
		}
	}
	w := login("192.0.2.1:1001", "", emp.Email, emp.Password)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" || !strings.Contains(w.Body.String(), "too_many_attempts") {
		t.Fatalf("attempt over the limit: %d %q Retry-After=%q; want 429 too_many_attempts with Retry-After, even with the right password",
			w.Code, w.Body.String(), w.Header().Get("Retry-After"))
	}
	// Activation links share the budget: guessing tokens is no way around it.
	r := httptest.NewRequest(http.MethodPost, "/activate", strings.NewReader(`{"token":"x","newPassword":"y"}`))
	r.RemoteAddr = "192.0.2.1:1002"
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	routes.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("activate from a limited address: %d; want 429", w.Code)
	}
	if w := login("192.0.2.2:1000", "", emp.Email, emp.Password); w.Code != http.StatusOK {
		t.Fatalf("another address: status %d; want 200", w.Code)
	}
}
