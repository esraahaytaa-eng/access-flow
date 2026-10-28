// Package auth handles passwords, sessions (JWT in an HttpOnly cookie), CSRF,
// login, logout, password change and the initial Super Admin (A4, F4).
package auth

import (
	"time"
	"unicode/utf8"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// A4 values.
const (
	MinPasswordLength         = 12
	MaxPasswordLength         = 128
	MaxFailedLogins           = 5
	LockoutDuration           = 15 * time.Minute
	TokenLifetime             = 30 * time.Minute
	MaxSessionLength          = 12 * time.Hour
	TemporaryPasswordLifetime = 72 * time.Hour
)

// ValidatePassword enforces the length rule (no composition rules), counting characters.
func ValidatePassword(pw string) error {
	if n := utf8.RuneCountInString(pw); n < MinPasswordLength || n > MaxPasswordLength {
		return platform.ValidationError("newPassword", "password_length")
	}
	return nil
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

// TemporaryPasswordExpiry is 72 hours for employees; the bootstrap Super Admin's
// temporary password never expires (F4, 2026-10-28).
func TemporaryPasswordExpiry(now time.Time, bootstrap bool) *time.Time {
	if bootstrap {
		return nil
	}
	exp := now.Add(TemporaryPasswordLifetime)
	return &exp
}

// CredentialUsable reports whether a password with this expiry may still be used.
func CredentialUsable(expiresAt *time.Time, now time.Time) bool {
	return expiresAt == nil || now.Before(*expiresAt)
}
