package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var testSecret = []byte("0123456789abcdef0123456789abcdef")

func TestTokenRoundTrip(t *testing.T) {
	s, err := newSession(uuid.New(), 7, t0)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signToken(testSecret, s)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseToken(testSecret, token, t0.Add(time.Minute))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got != s {
		t.Fatalf("got %+v; want %+v", got, s)
	}
	if s.CSRF == "" || !s.ExpiresAt.Equal(t0.Add(30*time.Minute)) || !s.LoginAt.Equal(t0) {
		t.Fatalf("new session = %+v; want a CSRF value, login now and 30 minutes lifetime", s)
	}
}

func TestParseTokenRejects(t *testing.T) {
	s, _ := newSession(uuid.New(), 1, t0)
	valid, _ := signToken(testSecret, s)
	claims := s.claims()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	sign := func(m jwt.SigningMethod, key any, c jwt.Claims) string {
		tok, err := jwt.NewWithClaims(m, c).SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	without := func(edit func(*tokenClaims)) string {
		c := claims
		edit(&c)
		return sign(jwt.SigningMethodHS256, testSecret, c)
	}

	cases := map[string]struct {
		token string
		now   time.Time
	}{
		"alg none":         {sign(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, claims), t0},
		"HS512":            {sign(jwt.SigningMethodHS512, testSecret, claims), t0},
		"RS256":            {sign(jwt.SigningMethodRS256, rsaKey, claims), t0},
		"other secret":     {sign(jwt.SigningMethodHS256, []byte("another-secret-another-secret-xx"), claims), t0},
		"tampered payload": {tamper(valid), t0},
		"expired":          {valid, s.ExpiresAt},
		"no expiry":        {without(func(c *tokenClaims) { c.ExpiresAt = nil }), t0},
		"no employee":      {without(func(c *tokenClaims) { c.Subject = "" }), t0},
		"no CSRF value":    {without(func(c *tokenClaims) { c.CSRF = "" }), t0},
		"no login time":    {without(func(c *tokenClaims) { c.LoginAt = nil }), t0},
		"issued in future": {valid, t0.Add(-time.Hour)},
		"not a token":      {"garbage", t0},
		"empty":            {"", t0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseToken(testSecret, tc.token, tc.now); err == nil {
				t.Fatal("token accepted; want rejected")
			}
		})
	}
}

func TestRenewedSessionKeepsLoginTimeAndCSRF(t *testing.T) {
	s, _ := newSession(uuid.New(), 3, t0)
	later := t0.Add(11*time.Hour + 50*time.Minute)
	r := s.renewed(later)
	if !r.LoginAt.Equal(s.LoginAt) || r.CSRF != s.CSRF || r.Version != s.Version || r.EmployeeID != s.EmployeeID {
		t.Fatalf("renewed %+v; want same login time, CSRF, version and employee as %+v", r, s)
	}
	if !r.IssuedAt.Equal(later) || !r.ExpiresAt.Equal(t0.Add(12*time.Hour)) {
		t.Fatalf("renewed issued %v expires %v; want issued now, capped at 12 hours after login", r.IssuedAt, r.ExpiresAt)
	}
}

// tamper changes one character of the payload, keeping the signature.
func tamper(token string) string {
	parts := strings.Split(token, ".")
	p := []byte(parts[1])
	if p[5] == 'A' {
		p[5] = 'B'
	} else {
		p[5] = 'A'
	}
	parts[1] = string(p)
	return strings.Join(parts, ".")
}
