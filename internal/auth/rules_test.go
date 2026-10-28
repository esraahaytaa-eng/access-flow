package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

var t0 = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name string
		pw   string
		ok   bool
	}{
		{"11 characters is too short", strings.Repeat("a", 11), false},
		{"12 characters is enough", strings.Repeat("a", 12), true},
		{"128 characters is the maximum", strings.Repeat("a", 128), true},
		{"129 characters is too long", strings.Repeat("a", 129), false},
		{"length counts characters, not bytes", strings.Repeat("ş", 12), true},
		{"no composition rules", "aaaaaaaaaaaa", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePassword(tc.pw)
			if tc.ok && err != nil {
				t.Fatalf("got %v; want nil", err)
			}
			var e *platform.Error
			if !tc.ok && (!errors.As(err, &e) || e.Code != "password_length" || e.Field != "newPassword") {
				t.Fatalf("got %v; want password_length on newPassword", err)
			}
		})
	}
}

func TestRegisterFailure(t *testing.T) {
	cases := []struct {
		before     int
		wantCount  int
		wantLocked bool
	}{
		{0, 1, false},
		{3, 4, false},
		{4, 0, true}, // the 5th consecutive failure locks and resets the counter
	}
	for _, tc := range cases {
		count, lockedUntil := RegisterFailure(tc.before, t0)
		if count != tc.wantCount || (lockedUntil != nil) != tc.wantLocked {
			t.Errorf("RegisterFailure(%d) = %d, %v; want %d, locked=%v", tc.before, count, lockedUntil, tc.wantCount, tc.wantLocked)
		}
		if lockedUntil != nil && !lockedUntil.Equal(t0.Add(15*time.Minute)) {
			t.Errorf("locked until %v; want 15 minutes after %v", lockedUntil, t0)
		}
	}
}

func TestIsLocked(t *testing.T) {
	until := t0.Add(15 * time.Minute)
	cases := []struct {
		name  string
		until *time.Time
		now   time.Time
		want  bool
	}{
		{"never locked", nil, t0, false},
		{"during the lock", &until, t0.Add(14 * time.Minute), true},
		{"lock has ended", &until, until, false},
	}
	for _, tc := range cases {
		if got := IsLocked(tc.until, tc.now); got != tc.want {
			t.Errorf("%s: got %v; want %v", tc.name, got, tc.want)
		}
	}
}

func TestTokenExpiry(t *testing.T) {
	cases := []struct {
		name         string
		loginAt, now time.Time
		want         time.Time
	}{
		{"fresh login lasts 30 minutes", t0, t0, t0.Add(30 * time.Minute)},
		{"re-issue after 5 hours", t0, t0.Add(5 * time.Hour), t0.Add(5*time.Hour + 30*time.Minute)},
		{"never beyond 12 hours after login", t0, t0.Add(11*time.Hour + 50*time.Minute), t0.Add(12 * time.Hour)},
	}
	for _, tc := range cases {
		if got := TokenExpiry(tc.loginAt, tc.now); !got.Equal(tc.want) {
			t.Errorf("%s: got %v; want %v", tc.name, got, tc.want)
		}
	}
}

func TestShouldReissue(t *testing.T) {
	cases := []struct {
		name              string
		loginAt, exp, now time.Time
		want              bool
	}{
		{"more than half left", t0, t0.Add(30 * time.Minute), t0.Add(10 * time.Minute), false},
		{"less than half left", t0, t0.Add(30 * time.Minute), t0.Add(16 * time.Minute), true},
		{"at the 12-hour cap a new token would not last longer", t0, t0.Add(12 * time.Hour), t0.Add(11*time.Hour + 50*time.Minute), false},
	}
	for _, tc := range cases {
		if got := ShouldReissue(tc.loginAt, tc.exp, tc.now); got != tc.want {
			t.Errorf("%s: got %v; want %v", tc.name, got, tc.want)
		}
	}
}

func TestTemporaryPasswordExpiry(t *testing.T) {
	if got := TemporaryPasswordExpiry(t0, false); got == nil || !got.Equal(t0.Add(72*time.Hour)) {
		t.Errorf("employee: got %v; want 72 hours later", got)
	}
	if got := TemporaryPasswordExpiry(t0, true); got != nil {
		t.Errorf("bootstrap Super Admin: got %v; want no expiry", got)
	}
}

func TestCredentialUsable(t *testing.T) {
	expires := t0.Add(72 * time.Hour)
	cases := []struct {
		name    string
		expires *time.Time
		now     time.Time
		want    bool
	}{
		{"no expiry (permanent or bootstrap)", nil, t0.Add(1000 * time.Hour), true},
		{"before expiry", &expires, expires.Add(-time.Second), true},
		{"at expiry", &expires, expires, false},
	}
	for _, tc := range cases {
		if got := CredentialUsable(tc.expires, tc.now); got != tc.want {
			t.Errorf("%s: got %v; want %v", tc.name, got, tc.want)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	// 64 + 1 + 189 = 254 characters, the longest valid address.
	longest := strings.Repeat("a", 64) + "@" + strings.Repeat("b", 185) + ".com"
	cases := []struct {
		in, want string
		ok       bool
	}{
		{" Admin@Example.COM ", "admin@example.com", true},
		{"\tuser@example.com\n", "user@example.com", true},
		{longest, longest, true},
		{longest + "x", "", false},
		{"", "", false},
		{"a", "", false},
		{"a@", "", false},
		{"@b", "", false},
		{"a@b@c", "", false},
		{"a b@c.d", "", false},
		{"a\tb@c.d", "", false},
		{"admİn@x.com", "", false}, // Turkish dotted capital I: Go and PostgreSQL lowercase it differently
		{"ayşe@x.com", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := NormalizeEmail(tc.in)
			if !tc.ok {
				var e *platform.Error
				if !errors.As(err, &e) || e.Code != "invalid_email" || e.Field != "email" {
					t.Fatalf("NormalizeEmail(%q) = %q, %v; want invalid_email on field email", tc.in, got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("NormalizeEmail(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}
}
