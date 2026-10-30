package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// Activation links (F4, A4, M18): a new, rehired or reset employee gets a single-use link,
// valid for 24 hours, to choose their own password. Only the token's SHA-256 is stored; the
// Mock Email Outbox keeps the raw token encrypted (mailkey.go) until the link is used, replaced
// or expires. Using a link never signs anyone in.

const ActivationLifetime = 24 * time.Hour

var (
	errInvalidActivationLink     = platform.NewError(http.StatusBadRequest, "invalid_activation_link")
	errActivationLinkUnavailable = platform.NewError(http.StatusGone, "activation_link_unavailable")
	errMockEmailUnavailable      = platform.NewError(http.StatusServiceUnavailable, "mock_email_unavailable")
)

// IssueActivation gives an active employee a new activation link inside the caller's
// transaction: earlier links are revoked and their encrypted copies cleared, the current
// password removed, every session ended and a lockout cleared; the link goes to the Mock Email
// Outbox and is audited without the token. reason is CREATED, IMPORTED, REHIRED or RESET.
func (s *Service) IssueActivation(ctx context.Context, tx pgx.Tx, now time.Time, actor *uuid.UUID, employee uuid.UUID, reason string) error {
	if s.mailKey == nil {
		return errMockEmailUnavailable
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	mail, err := uuid.NewV7()
	if err != nil {
		return err
	}
	sealed, err := seal(s.mailKey, token, mail, employee)
	if err != nil {
		return err
	}

	var name string
	if err := tx.QueryRow(ctx, `UPDATE employees SET token_version = token_version + 1, failed_login_count = 0, locked_until = NULL
		WHERE id = $1 RETURNING full_name`, employee).Scan(&name); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE activation_tokens SET revoked_at = $2 WHERE employee_id = $1 AND used_at IS NULL AND revoked_at IS NULL",
		employee, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE email_outbox SET sealed_token = NULL
		WHERE sealed_token IS NOT NULL AND activation_id IN (SELECT id FROM activation_tokens WHERE employee_id = $1)`, employee); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM password_credentials WHERE employee_id = $1", employee); err != nil {
		return err
	}
	expires := now.Add(ActivationLifetime)
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO activation_tokens (employee_id, token_hash, reason, issued_by, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, employee, hash[:], reason, actor, now, expires).Scan(&id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO email_outbox (id, recipient_id, kind, activation_id, subject, body, sealed_token)
		VALUES ($1, $2, 'ACTIVATION', $3, $4, $5, $6)`, mail, employee, id, "AccessFlow hesabınızı etkinleştirin",
		"Merhaba "+name+", AccessFlow hesabınız için parolanızı belirleyin. Bağlantı 24 saat geçerlidir ve yalnızca bir kez kullanılabilir. "+
			"Parolanızı belirledikten sonra normal giriş sayfasından oturum açın.", sealed); err != nil {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{ActorID: actor, Action: "ACTIVATION_LINK_ISSUED", Entity: "employee",
		EntityID: &employee, SubjectID: &employee, Details: map[string]any{"activationId": id, "reason": reason, "expiresAt": expires}})
}

// RegisterActivations adds the activation routes and the nominee's role-link reveal; r must already require a session.
func (s *Service) RegisterActivations(r chi.Router) {
	r.Get("/email-outbox/{id}/activation-link", s.handleReveal)
	r.Get("/email-outbox/{id}/role-link", s.handleRoleLink)
	r.Get("/activations", s.handleActivations)
}

// Activation is one row of the Super Admin's activation screen.
type Activation struct {
	Employee struct {
		ID       uuid.UUID `json:"id"`
		FullName string    `json:"fullName"`
		Email    string    `json:"email"`
	} `json:"employee"`
	Status      string     `json:"status"`
	Reason      *string    `json:"reason"`
	CreatedAt   *time.Time `json:"createdAt"`
	ExpiresAt   *time.Time `json:"expiresAt"`
	CompletedAt *time.Time `json:"completedAt"`
}

// activations lists active employees by their newest link, plus those with neither a link nor
// a password; employees with a password and no link (e.g. the demo accounts) are not listed.
func (s *Service) activations(ctx context.Context, actor Principal) ([]Activation, error) {
	if !actor.HasRole("SUPER_ADMIN") {
		return nil, platform.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT e.id, e.full_name, e.email, t.id IS NOT NULL, t.reason, t.created_at, t.expires_at, t.used_at, t.revoked_at, now()
		FROM employees e
		LEFT JOIN LATERAL (SELECT * FROM activation_tokens WHERE employee_id = e.id ORDER BY created_at DESC, id DESC LIMIT 1) t ON true
		WHERE e.status = 'ACTIVE'
		  AND (t.id IS NOT NULL OR NOT EXISTS (SELECT 1 FROM password_credentials c WHERE c.employee_id = e.id))
		ORDER BY lower(e.full_name), e.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Activation, error) {
		var (
			a       Activation
			hasLink bool
			revoked *time.Time
			now     time.Time
		)
		err := row.Scan(&a.Employee.ID, &a.Employee.FullName, &a.Employee.Email, &hasLink, &a.Reason, &a.CreatedAt, &a.ExpiresAt,
			&a.CompletedAt, &revoked, &now)
		var expires time.Time
		if a.ExpiresAt != nil {
			expires = *a.ExpiresAt
		}
		a.Status = ActivationStatus(hasLink, a.CompletedAt, revoked, expires, now)
		return a, err
	})
}

// ClearExpiredLinks removes the encrypted copies of expired links from the Mock Email Outbox
// (run by the sweeps). The reveal already refuses them; this keeps nothing longer than needed.
func ClearExpiredLinks(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	tag, err := pool.Exec(ctx, `UPDATE email_outbox o SET sealed_token = NULL FROM activation_tokens t
		WHERE t.id = o.activation_id AND o.sealed_token IS NOT NULL AND t.expires_at <= now()`)
	if err != nil {
		return 0, err
	}
	roles, err := pool.Exec(ctx, `UPDATE email_outbox o SET sealed_token = NULL FROM super_admin_nominations n
		WHERE n.id = o.nomination_id AND o.sealed_token IS NOT NULL AND (n.status <> 'PENDING' OR n.expires_at <= now())`)
	return int(tag.RowsAffected() + roles.RowsAffected()), err
}

// revealLink decrypts the link of an activation email for a Super Admin, only while it can
// still be used, and audits the view.
func (s *Service) revealLink(ctx context.Context, actor Principal, mail uuid.UUID) (string, error) {
	if !actor.HasRole("SUPER_ADMIN") {
		return "", platform.ErrForbidden
	}
	var token string
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		var (
			employee, activation uuid.UUID
			sealed               []byte
			usable               bool
		)
		err := tx.QueryRow(ctx, `
			SELECT o.recipient_id, o.activation_id, o.sealed_token,
			       t.used_at IS NULL AND t.revoked_at IS NULL AND t.expires_at > $2 AND e.status = 'ACTIVE'
			FROM email_outbox o JOIN activation_tokens t ON t.id = o.activation_id JOIN employees e ON e.id = o.recipient_id
			WHERE o.id = $1`, mail, now).Scan(&employee, &activation, &sealed, &usable)
		if errors.Is(err, pgx.ErrNoRows) {
			return platform.ErrNotFound
		}
		if err != nil {
			return err
		}
		if !usable || sealed == nil || s.mailKey == nil {
			return errActivationLinkUnavailable
		}
		if token, err = openSealed(s.mailKey, sealed, mail, employee); err != nil {
			s.log.Warn("mock email link does not decrypt with the current key", "email_id", mail)
			return errActivationLinkUnavailable
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "ACTIVATION_LINK_VIEWED", Entity: "employee",
			EntityID: &employee, SubjectID: &employee, Details: map[string]any{"activationId": activation}})
	})
	return token, err
}

// activate sets the password chosen through a link. Every link failure gets the same answer;
// a weak password is reported on its field and leaves the link usable.
func (s *Service) activate(ctx context.Context, token, password string) error {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return errInvalidActivationLink
	}
	hash := sha256.Sum256([]byte(token))
	var (
		employee     uuid.UUID
		email        string
		usable, dead bool
	)
	err = s.pool.QueryRow(ctx, `SELECT t.employee_id, e.email,
		       t.used_at IS NULL AND t.revoked_at IS NULL AND t.expires_at > now() AND e.status = 'ACTIVE'
		FROM activation_tokens t JOIN employees e ON e.id = t.employee_id WHERE t.token_hash = $1`, hash[:]).Scan(&employee, &email, &usable)
	if errors.Is(err, pgx.ErrNoRows) {
		return errInvalidActivationLink
	}
	if err != nil {
		return err
	}
	// A dead link gets the generic answer whatever the password; the check under lock decides.
	var passwordHash string
	if usable {
		if err := ValidateNewPassword(password, email); err != nil {
			return err
		}
		if passwordHash, err = s.Hash(ctx, password); err != nil {
			return err
		}
	}
	err = platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		// Lock the employee first, as every other change to a person does, then read the link.
		if _, err := tx.Exec(ctx, "SELECT 1 FROM employees WHERE id = $1 FOR UPDATE", employee); err != nil {
			return err
		}
		var (
			id                uuid.UUID
			status            string
			expires           time.Time
			usedAt, revokedAt *time.Time
		)
		if err := tx.QueryRow(ctx, `SELECT t.id, t.expires_at, t.used_at, t.revoked_at, e.status
			FROM activation_tokens t JOIN employees e ON e.id = t.employee_id WHERE t.token_hash = $1`, hash[:]).
			Scan(&id, &expires, &usedAt, &revokedAt, &status); err != nil {
			return err
		}
		reason := ""
		switch {
		case usedAt != nil:
			reason = "used"
		case revokedAt != nil:
			reason = "replaced"
		case !now.Before(expires):
			reason = "expired"
		case status != "ACTIVE":
			reason = "inactive"
		case passwordHash == "": // looked dead before the lock, alive now: ask again
			return errInvalidActivationLink
		}
		event := audit.Event{Entity: "employee", EntityID: &employee, SubjectID: &employee}
		if reason != "" {
			dead = true
			event.Action, event.Details = "ACTIVATION_FAILED", map[string]any{"activationId": id, "reason": reason}
			return audit.Record(ctx, tx, event)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO password_credentials (employee_id, password_hash, is_temporary, expires_at, updated_at)
			VALUES ($1, $2, false, NULL, $3)
			ON CONFLICT (employee_id) DO UPDATE SET password_hash = $2, is_temporary = false, expires_at = NULL, updated_at = $3`,
			employee, passwordHash, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE activation_tokens SET used_at = $2 WHERE id = $1", id, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE email_outbox SET sealed_token = NULL WHERE activation_id = $1", id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE employees SET token_version = token_version + 1, failed_login_count = 0, locked_until = NULL WHERE id = $1",
			employee); err != nil {
			return err
		}
		viewedBy := []string{}
		rows, err := tx.Query(ctx, `SELECT DISTINCT actor_id::text FROM audit_events
			WHERE action = 'ACTIVATION_LINK_VIEWED' AND details->>'activationId' = $1::text`, id)
		if err != nil {
			return err
		}
		if viewedBy, err = pgx.AppendRows(viewedBy, rows, pgx.RowTo[string]); err != nil {
			return err
		}
		event.ActorID, event.Action = &employee, "ACTIVATION_COMPLETED"
		event.Details = map[string]any{"activationId": id, "viewedBy": viewedBy}
		return audit.Record(ctx, tx, event)
	})
	if err != nil {
		return err
	}
	if dead {
		return errInvalidActivationLink
	}
	return nil
}

func (s *Service) handleReveal(w http.ResponseWriter, r *http.Request) {
	mail, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		platform.WriteError(w, r, platform.ErrNotFound)
		return
	}
	p, _ := PrincipalFrom(r.Context())
	token, err := s.revealLink(r.Context(), p, mail)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store") // carries a credential
	platform.WriteJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (s *Service) handleActivations(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	list, err := s.activations(r.Context(), p)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"activations": list})
}

func (s *Service) handleActivate(w http.ResponseWriter, r *http.Request) {
	if s.limited(w, r) {
		return
	}
	var in struct{ Token, NewPassword string }
	if err := platform.DecodeJSON(w, r, &in); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if err := s.activate(r.Context(), in.Token, in.NewPassword); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
