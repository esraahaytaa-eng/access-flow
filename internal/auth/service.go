package auth

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

var (
	errInvalidCredentials     = platform.NewError(401, "invalid_credentials")
	errInvalidCurrentPassword = platform.ValidationError("currentPassword", "invalid_current_password")
	errPasswordUnchanged      = platform.ValidationError("newPassword", "password_unchanged")
	errTemporaryExpired       = platform.ValidationError("currentPassword", "temporary_password_expired")
)

// Options configures the Service.
type Options struct {
	Secret        []byte // JWT signing secret, from LoadSecret
	SecureCookies bool   // set the Secure flag; off for plain-HTTP localhost (A4)
	BootstrapFile string // where the initial Super Admin password is written (A4)
	Logger        *slog.Logger
}

// Service authenticates employees and guards the API.
type Service struct {
	pool          *pgxpool.Pool
	secret        []byte
	secureCookies bool
	bootstrapFile string
	log           *slog.Logger
	dummyHash     string        // verified for unknown emails, so timing does not reveal which exist
	hashSlots     chan struct{} // limits concurrent argon2 work (19 MiB each), see hashSlot
}

func NewService(pool *pgxpool.Pool, opts Options) (*Service, error) {
	dummy, err := HashPassword("not a real password")
	if err != nil {
		return nil, err
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Service{pool: pool, secret: opts.Secret, secureCookies: opts.SecureCookies,
		bootstrapFile: opts.BootstrapFile, log: opts.Logger, dummyHash: dummy, hashSlots: make(chan struct{}, 2)}, nil
}

// Principal is the signed-in employee, loaded from the database on every request.
type Principal struct {
	EmployeeID         uuid.UUID
	Email              string
	FullName           string
	Roles              []string
	AdminGroups        []Group
	MustChangePassword bool
}

// Group is an admin group the employee currently belongs to.
type Group struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// OpenGroupsJSON selects the open admin groups of employee e as a JSON array, ordered by name.
const OpenGroupsJSON = `COALESCE((SELECT json_agg(json_build_object('id', g.id, 'name', g.name) ORDER BY lower(g.name))
	FROM admin_group_memberships m JOIN admin_groups g ON g.id = m.group_id
	WHERE m.employee_id = e.id AND m.valid_to IS NULL), '[]')`

// HasRole reports whether the principal currently holds role.
func (p Principal) HasRole(role string) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// hashSlot waits for one of the few argon2 slots. It is taken before any database
// connection, so a burst of logins queues here instead of exhausting the pool and memory.
func (s *Service) hashSlot(ctx context.Context) (release func(), err error) {
	select {
	case s.hashSlots <- struct{}{}:
		return func() { <-s.hashSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Hash hashes a password under the hash-slot limit; for other packages that set passwords.
func (s *Service) Hash(ctx context.Context, pw string) (string, error) {
	release, err := s.hashSlot(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	return HashPassword(pw)
}

// verify checks pw against stored; a corrupted hash counts as a wrong password, so the
// response stays identical, and is logged.
func (s *Service) verify(id uuid.UUID, stored, pw string) bool {
	match, err := VerifyPassword(stored, pw)
	if err != nil {
		s.log.Error("stored password hash is invalid", "employee_id", id, "err", err)
	}
	return match
}

// login checks the password under a row lock on the employee and records every outcome.
// Failed attempts are committed (they are audit evidence); the caller gets one generic error.
func (s *Service) login(ctx context.Context, email, password string) (Session, error) {
	if normalized, err := NormalizeEmail(email); err == nil {
		email = normalized
	} else {
		// Not lowercased: Go folds some non-ASCII letters to ASCII (Kelvin sign → "k"), which
		// could match a real address. Stored emails are lowercase ASCII, so this matches none.
		email = strings.TrimSpace(email)
	}
	release, err := s.hashSlot(ctx)
	if err != nil {
		return Session{}, err
	}
	defer release()
	var session Session
	succeeded := false
	err = platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		var (
			id                 uuid.UUID
			status             string
			version, failed    int
			lockedUntil, expAt *time.Time
			hash               *string
		)
		err := tx.QueryRow(ctx, `
			SELECT e.id, e.status, e.token_version, e.failed_login_count, e.locked_until, c.password_hash, c.expires_at
			FROM employees e LEFT JOIN password_credentials c ON c.employee_id = e.id
			WHERE e.email = $1
			FOR UPDATE OF e`, email).Scan(&id, &status, &version, &failed, &lockedUntil, &hash, &expAt)
		if errors.Is(err, pgx.ErrNoRows) {
			_, _ = VerifyPassword(s.dummyHash, password)
			if r := []rune(email); len(r) > MaxEmailLength {
				email = string(r[:MaxEmailLength])
			}
			return audit.Record(ctx, tx, audit.Event{Action: "LOGIN_FAILED", Entity: "employee",
				Details: map[string]any{"email": email, "reason": "unknown_email"}})
		}
		if err != nil {
			return err
		}

		stored := s.dummyHash
		if hash != nil {
			stored = *hash
		}
		match := s.verify(id, stored, password)
		reason := ""
		switch {
		case IsLocked(lockedUntil, now):
			reason = "locked"
		case !match || hash == nil:
			reason = "wrong_password"
		case !CredentialUsable(expAt, now):
			reason = "temporary_password_expired"
		case status != "ACTIVE":
			reason = "inactive"
		}

		event := audit.Event{Entity: "employee", EntityID: &id, SubjectID: &id}
		if reason == "" {
			if _, err := tx.Exec(ctx, "UPDATE employees SET failed_login_count = 0, locked_until = NULL WHERE id = $1", id); err != nil {
				return err
			}
			if session, err = newSession(id, version, now); err != nil {
				return err
			}
			succeeded = true
			event.Action, event.ActorID = "LOGIN_SUCCEEDED", &id
			return audit.Record(ctx, tx, event)
		}
		if reason == "wrong_password" || reason == "temporary_password_expired" {
			if err := s.registerFailure(ctx, tx, id, failed, now); err != nil {
				return err
			}
		}
		event.Action, event.Details = "LOGIN_FAILED", map[string]any{"reason": reason}
		return audit.Record(ctx, tx, event)
	})
	if err != nil {
		return Session{}, err
	}
	if !succeeded {
		return Session{}, errInvalidCredentials
	}
	return session, nil
}

// registerFailure counts a wrong password and locks the account on the 5th in a row.
func (s *Service) registerFailure(ctx context.Context, tx pgx.Tx, id uuid.UUID, failed int, now time.Time) error {
	count, lockedUntil := RegisterFailure(failed, now)
	if _, err := tx.Exec(ctx, "UPDATE employees SET failed_login_count = $2, locked_until = COALESCE($3, locked_until) WHERE id = $1",
		id, count, lockedUntil); err != nil {
		return err
	}
	if lockedUntil == nil {
		return nil
	}
	return audit.Record(ctx, tx, audit.Event{Action: "LOGIN_LOCKED", Entity: "employee", EntityID: &id, SubjectID: &id,
		Details: map[string]any{"lockedUntil": lockedUntil}})
}

// loadPrincipal checks a session against the database: the employee must be active and
// the token version current. Roles and the temporary-password flag are read fresh (A4).
func (s *Service) loadPrincipal(ctx context.Context, session Session) (Principal, error) {
	p := Principal{EmployeeID: session.EmployeeID}
	var status string
	var version int
	var temporary *bool
	err := s.pool.QueryRow(ctx, `
		SELECT e.email, e.full_name, e.status, e.token_version, c.is_temporary,
		       ARRAY(SELECT r.role FROM role_assignments r WHERE r.employee_id = e.id AND r.valid_to IS NULL ORDER BY r.role),
		       `+OpenGroupsJSON+`
		FROM employees e LEFT JOIN password_credentials c ON c.employee_id = e.id
		WHERE e.id = $1`, session.EmployeeID).Scan(&p.Email, &p.FullName, &status, &version, &temporary, &p.Roles, &p.AdminGroups)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, platform.ErrUnauthorized
	}
	if err != nil {
		return Principal{}, err
	}
	if status != "ACTIVE" || version != session.Version {
		return Principal{}, platform.ErrUnauthorized
	}
	p.MustChangePassword = temporary != nil && *temporary
	return p, nil
}

// logout increments token_version, which invalidates every token of the employee,
// including copies (A4; signs out all browsers, an accepted trade-off).
func (s *Service) logout(ctx context.Context, p Principal) error {
	return platform.InTx(ctx, s.pool, func(tx pgx.Tx, _ time.Time) error {
		if _, err := tx.Exec(ctx, "UPDATE employees SET token_version = token_version + 1 WHERE id = $1", p.EmployeeID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "LOGOUT", ActorID: &p.EmployeeID, Entity: "employee",
			EntityID: &p.EmployeeID, SubjectID: &p.EmployeeID})
	})
}

// changePassword replaces the password after checking the current one. A wrong current
// password counts toward lockout. Other sessions are ended; the caller gets a new one.
func (s *Service) changePassword(ctx context.Context, p Principal, current, next string) (Session, error) {
	if err := ValidatePassword(next); err != nil {
		return Session{}, err
	}
	if next == current {
		return Session{}, errPasswordUnchanged
	}
	release, err := s.hashSlot(ctx)
	if err != nil {
		return Session{}, err
	}
	defer release()
	newHash, err := HashPassword(next)
	if err != nil {
		return Session{}, err
	}
	var session Session
	changed, expired := false, false
	err = platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		id := p.EmployeeID
		var (
			failed             int
			lockedUntil, expAt *time.Time
			hash               *string
			temporary          *bool
		)
		err := tx.QueryRow(ctx, `
			SELECT e.failed_login_count, e.locked_until, c.password_hash, c.expires_at, c.is_temporary
			FROM employees e LEFT JOIN password_credentials c ON c.employee_id = e.id
			WHERE e.id = $1
			FOR UPDATE OF e`, id).Scan(&failed, &lockedUntil, &hash, &expAt, &temporary)
		if err != nil {
			return err
		}
		stored := s.dummyHash
		if hash != nil {
			stored = *hash
		}
		match := s.verify(id, stored, current)
		event := audit.Event{ActorID: &id, Entity: "employee", EntityID: &id, SubjectID: &id}
		locked := IsLocked(lockedUntil, now)
		if !locked && match && hash != nil && !CredentialUsable(expAt, now) {
			// The right password, but too late: not a guess, so it does not count toward lockout.
			expired = true
			event.Action, event.Details = "PASSWORD_CHANGE_FAILED", map[string]any{"reason": "temporary_password_expired"}
			return audit.Record(ctx, tx, event)
		}
		if locked || !match || hash == nil {
			if !locked {
				if err := s.registerFailure(ctx, tx, id, failed, now); err != nil {
					return err
				}
			}
			event.Action = "PASSWORD_CHANGE_FAILED"
			return audit.Record(ctx, tx, event)
		}

		if _, err := tx.Exec(ctx, `UPDATE password_credentials
			SET password_hash = $2, is_temporary = false, expires_at = NULL, updated_at = $3 WHERE employee_id = $1`,
			id, newHash, now); err != nil {
			return err
		}
		var version int
		if err := tx.QueryRow(ctx, `UPDATE employees SET token_version = token_version + 1, failed_login_count = 0, locked_until = NULL
			WHERE id = $1 RETURNING token_version`, id).Scan(&version); err != nil {
			return err
		}
		if session, err = newSession(id, version, now); err != nil {
			return err
		}
		changed = true
		event.Action, event.Details = "PASSWORD_CHANGED", map[string]any{"wasTemporary": temporary != nil && *temporary}
		return audit.Record(ctx, tx, event)
	})
	if err != nil {
		return Session{}, err
	}
	if expired {
		return Session{}, errTemporaryExpired
	}
	if !changed {
		return Session{}, errInvalidCurrentPassword
	}
	if err := s.RemoveFinishedBootstrapFile(ctx); err != nil {
		s.log.Error("remove bootstrap file", "err", err)
	}
	return session, nil
}
