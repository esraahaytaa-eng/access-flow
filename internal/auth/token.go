package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Session is what the signed cookie carries (A4). Status and roles are not in it:
// they are read from the database on every request.
type Session struct {
	EmployeeID uuid.UUID
	Version    int // must match employees.token_version
	CSRF       string
	LoginAt    time.Time
	IssuedAt   time.Time
	ExpiresAt  time.Time
}

type tokenClaims struct {
	jwt.RegisteredClaims
	Version int              `json:"ver"`
	CSRF    string           `json:"csrf"`
	LoginAt *jwt.NumericDate `json:"login_at"`
}

// newSession starts a session at login time now with a fresh CSRF value.
func newSession(employeeID uuid.UUID, version int, now time.Time) (Session, error) {
	csrf := make([]byte, 32)
	if _, err := rand.Read(csrf); err != nil {
		return Session{}, err
	}
	now = now.UTC().Truncate(time.Second) // JWT times have second precision
	return Session{
		EmployeeID: employeeID,
		Version:    version,
		CSRF:       base64.RawURLEncoding.EncodeToString(csrf),
		LoginAt:    now,
		IssuedAt:   now,
		ExpiresAt:  TokenExpiry(now, now),
	}, nil
}

// renewed is the same session re-issued at now: login time, CSRF value and version stay.
func (s Session) renewed(now time.Time) Session {
	now = now.UTC().Truncate(time.Second)
	s.IssuedAt = now
	s.ExpiresAt = TokenExpiry(s.LoginAt, now)
	return s
}

func (s Session) claims() tokenClaims {
	return tokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   s.EmployeeID.String(),
			IssuedAt:  jwt.NewNumericDate(s.IssuedAt),
			ExpiresAt: jwt.NewNumericDate(s.ExpiresAt),
		},
		Version: s.Version,
		CSRF:    s.CSRF,
		LoginAt: jwt.NewNumericDate(s.LoginAt),
	}
}

func signToken(secret []byte, s Session) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodHS256, s.claims()).SignedString(secret)
}

var errBadToken = errors.New("invalid session token")

// parseToken accepts only HS256 tokens signed with secret, unexpired at now and with
// every claim AccessFlow needs.
func parseToken(secret []byte, token string, now time.Time) (Session, error) {
	var c tokenClaims
	_, err := jwt.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
	if err != nil {
		return Session{}, errBadToken
	}
	id, err := uuid.Parse(c.Subject)
	if err != nil || c.CSRF == "" || c.LoginAt == nil || c.IssuedAt == nil {
		return Session{}, errBadToken
	}
	return Session{
		EmployeeID: id,
		Version:    c.Version,
		CSRF:       c.CSRF,
		LoginAt:    c.LoginAt.UTC(),
		IssuedAt:   c.IssuedAt.UTC(),
		ExpiresAt:  c.ExpiresAt.UTC(),
	}, nil
}
