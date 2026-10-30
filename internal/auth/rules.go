// Package auth handles passwords, sessions (JWT in an HttpOnly cookie), CSRF,
// login, logout, password change and the initial Super Admin (A4, F4).
package auth

import (
	_ "embed"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// A4 values.
const (
	MinPasswordLength = 12
	MaxPasswordLength = 128
	MaxFailedLogins   = 5
	LockoutDuration   = 15 * time.Minute
	TokenLifetime     = 30 * time.Minute
	MaxSessionLength  = 12 * time.Hour
)

// ValidatePassword enforces the length rule (no composition rules), counting characters.
func ValidatePassword(pw string) error {
	if n := utf8.RuneCountInString(pw); n < MinPasswordLength || n > MaxPasswordLength {
		return platform.ValidationError("newPassword", "password_length")
	}
	return nil
}

//go:embed common_passwords.txt
var commonPasswordList string

// commonPasswords holds the embedded list, lowercased; lines starting with # are comments.
var commonPasswords = func() map[string]bool {
	m := map[string]bool{}
	for _, line := range strings.Split(commonPasswordList, "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			m[strings.ToLower(line)] = true
		}
	}
	return m
}()

// ValidateNewPassword checks a password a person chooses (activation, password change):
// the length rule, not a common password, and not their email or its name part (A4, M18).
func ValidateNewPassword(pw, email string) error {
	if err := ValidatePassword(pw); err != nil {
		return err
	}
	lower := strings.ToLower(pw)
	if commonPasswords[lower] {
		return platform.ValidationError("newPassword", "common_password")
	}
	email = strings.ToLower(email)
	name, _, _ := strings.Cut(email, "@")
	if lower == email || (len(name) >= 4 && strings.Contains(lower, name)) {
		return platform.ValidationError("newPassword", "password_contains_email")
	}
	return nil
}

// MaxEmailLength is the longest valid address.
const MaxEmailLength = 254

var errInvalidEmail = platform.ValidationError("email", "invalid_email")

// NormalizeEmail trims and lowercases an address and checks it: printable ASCII without
// spaces, one "@" between non-empty parts, at most 254 characters. ASCII only, so Go and
// PostgreSQL lowercase it identically; the employees CHECK enforces the same rule.
func NormalizeEmail(s string) (string, error) {
	s = strings.Trim(s, " \t\r\n\v\f")
	at := strings.IndexByte(s, '@')
	if len(s) > MaxEmailLength || at <= 0 || at == len(s)-1 || strings.Count(s, "@") != 1 {
		return "", errInvalidEmail
	}
	for i := 0; i < len(s); i++ { // before lowercasing: Go maps some non-ASCII letters to ASCII
		if s[i] < '!' || s[i] > '~' {
			return "", errInvalidEmail
		}
	}
	return strings.ToLower(s), nil
}

// RegisterFailure returns the new failure count and, on the 5th consecutive failure,
// the lock end; the counter then restarts.
func RegisterFailure(failed int, now time.Time) (int, *time.Time) {
	if failed+1 >= MaxFailedLogins {
		until := now.Add(LockoutDuration)
		return 0, &until
	}
	return failed + 1, nil
}

// IsLocked reports whether a lock is still running at now.
func IsLocked(lockedUntil *time.Time, now time.Time) bool {
	return lockedUntil != nil && now.Before(*lockedUntil)
}

// TokenExpiry is 30 minutes from now, but never more than 12 hours after login.
func TokenExpiry(loginAt, now time.Time) time.Time {
	exp := now.Add(TokenLifetime)
	if limit := loginAt.Add(MaxSessionLength); exp.After(limit) {
		return limit
	}
	return exp
}

// ShouldReissue reports whether an active user's cookie should be renewed: less than
// half its lifetime is left and a new token would actually last longer.
func ShouldReissue(loginAt, exp, now time.Time) bool {
	return exp.Sub(now) < TokenLifetime/2 && TokenExpiry(loginAt, now).After(exp)
}

// CredentialUsable reports whether a password with this expiry may still be used. Only
// temporary passwords issued before M18 (72 hours) have one; new ones do not expire.
func CredentialUsable(expiresAt *time.Time, now time.Time) bool {
	return expiresAt == nil || now.Before(*expiresAt)
}

// ActivationStatus is an employee's activation state from their newest link (M18): NO_LINK
// (none, or revoked by a mock email key reset), PENDING, EXPIRED or COMPLETED.
func ActivationStatus(hasLink bool, used, revoked *time.Time, expires, now time.Time) string {
	switch {
	case !hasLink || (used == nil && revoked != nil):
		return "NO_LINK"
	case used != nil:
		return "COMPLETED"
	case now.Before(expires):
		return "PENDING"
	}
	return "EXPIRED"
}
