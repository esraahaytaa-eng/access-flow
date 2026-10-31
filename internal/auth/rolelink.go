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

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// Role-acceptance links (D2 succession, 2026-10-30): the nominee of an HR succession
// nomination accepts the Super Admin role through a single-use link, valid for 24 hours,
// while signed in as themself. Kept apart from activation links: the token's SHA-256 is
// stored on the nomination, and only the nominee's own Mock Email Outbox shows the link.

const RoleLinkLifetime = 24 * time.Hour

var errRoleLinkUnavailable = platform.NewError(http.StatusGone, "role_link_unavailable")

// RoleLinkHash is the stored form of a role-acceptance token, or nil for a malformed one.
func RoleLinkHash(token string) []byte {
	if raw, err := base64.RawURLEncoding.DecodeString(token); err != nil || len(raw) != 32 {
		return nil
	}
	hash := sha256.Sum256([]byte(token))
	return hash[:]
}

// IssueRoleLink gives the nominee of a pending nomination a new link inside the caller's
// transaction: the nomination stores the new token's hash and expires in 24 hours, earlier
// encrypted copies are cleared, and the email goes to the nominee's outbox. The caller audits.
func (s *Service) IssueRoleLink(ctx context.Context, tx pgx.Tx, now time.Time, nomination, nominee uuid.UUID) (time.Time, error) {
	if s.mailKey == nil {
		return time.Time{}, errMockEmailUnavailable
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	mail, err := uuid.NewV7()
	if err != nil {
		return time.Time{}, err
	}
	sealed, err := seal(s.mailKey, token, mail, nominee)
	if err != nil {
		return time.Time{}, err
	}
	expires := now.Add(RoleLinkLifetime)
	var name string
	if err := tx.QueryRow(ctx, `UPDATE super_admin_nominations n SET token_hash = $2, expires_at = $3 FROM employees e
		WHERE n.id = $1 AND e.id = n.nominee_id RETURNING e.full_name`, nomination, RoleLinkHash(token), expires).Scan(&name); err != nil {
		return time.Time{}, err
	}
	if _, err := tx.Exec(ctx, "UPDATE email_outbox SET sealed_token = NULL WHERE nomination_id = $1 AND sealed_token IS NOT NULL", nomination); err != nil {
		return time.Time{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO email_outbox (id, recipient_id, kind, nomination_id, subject, body, sealed_token)
		VALUES ($1, $2, 'ROLE_ACCEPTANCE', $3, $4, $5, $6)`, mail, nominee, nomination, "Süper Yönetici rolü size atandı: kabulünüz bekleniyor",
		"Merhaba "+name+", İK sizi AccessFlow'un yeni Süper Yöneticisi olarak belirledi. Rol, siz kabul edene kadar etkin olmaz. "+
			"Bağlantıyı kendi hesabınızla oturum açmışken açın ve rolü kabul edin. Bağlantı 24 saat geçerlidir ve yalnızca bir kez kullanılabilir; "+
			"parolanız değişmez.", sealed)
	return expires, err
}

// revealRoleLink decrypts a role-acceptance email's link for its recipient only, while the
// nomination is still open and the link current. Anyone else gets not found.
func (s *Service) revealRoleLink(ctx context.Context, actor Principal, mail uuid.UUID) (string, error) {
	var (
		sealed []byte
		usable bool
	)
	err := s.pool.QueryRow(ctx, `SELECT o.sealed_token, n.status = 'PENDING' AND n.expires_at > now() AND e.status = 'ACTIVE'
		FROM email_outbox o JOIN super_admin_nominations n ON n.id = o.nomination_id JOIN employees e ON e.id = o.recipient_id
		WHERE o.id = $1 AND o.kind = 'ROLE_ACCEPTANCE' AND o.recipient_id = $2`, mail, actor.EmployeeID).Scan(&sealed, &usable)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", platform.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if !usable || sealed == nil || s.mailKey == nil {
		return "", errRoleLinkUnavailable
	}
	token, err := openSealed(s.mailKey, sealed, mail, actor.EmployeeID)
	if err != nil {
		s.log.Warn("mock email link does not decrypt with the current key", "email_id", mail)
		return "", errRoleLinkUnavailable
	}
	return token, nil
}

func (s *Service) handleRoleLink(w http.ResponseWriter, r *http.Request) {
	mail, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		platform.WriteError(w, r, platform.ErrNotFound)
		return
	}
	p, _ := PrincipalFrom(r.Context())
	token, err := s.revealRoleLink(r.Context(), p, mail)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store") // carries a credential
	platform.WriteJSON(w, http.StatusOK, map[string]string{"token": token})
}
