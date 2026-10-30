package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// The Mock Email Outbox keeps activation tokens encrypted with a key kept in a file on the
// app volume, never in the database, so a database dump reveals no link (A4, M18).

const mailKeySize = 32 // AES-256

var (
	// ErrMailKeyUnavailable: the key is missing while links are encrypted with it, or the file
	// is unreadable or damaged. The app runs on; links cannot be shown or issued until
	// reset-mail-key.
	ErrMailKeyUnavailable = errors.New("mock email key unavailable; run reset-mail-key")
	ErrMailKeyValid       = errors.New("the mock email key is valid; nothing to reset")
)

// LoadMailKey reads the key at path. It creates one only when the file does not exist and no
// link is encrypted with a lost key; it never overwrites a file.
func LoadMailKey(ctx context.Context, pool *pgxpool.Pool, path string) ([]byte, error) {
	key, err := readMailKey(path)
	if !errors.Is(err, os.ErrNotExist) {
		return key, err
	}
	var sealed bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM email_outbox WHERE sealed_token IS NOT NULL)").Scan(&sealed); err != nil {
		return nil, err
	}
	if sealed {
		return nil, fmt.Errorf("%w: %s is missing while activation links are encrypted with it", ErrMailKeyUnavailable, path)
	}
	return writeMailKey(path)
}

// readMailKey returns os.ErrNotExist for a missing file and ErrMailKeyUnavailable for any other problem.
func readMailKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, err
	case err != nil:
		return nil, fmt.Errorf("%w: %s is unreadable", ErrMailKeyUnavailable, path)
	case len(key) != mailKeySize:
		return nil, fmt.Errorf("%w: %s is not a %d-byte key", ErrMailKeyUnavailable, path, mailKeySize)
	}
	return key, nil
}

func writeMailKey(path string) ([]byte, error) {
	key := make([]byte, mailKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(key); err != nil {
		f.Close()
		return nil, err
	}
	return key, f.Close()
}

// ResetMailKey is the explicit recovery for an unavailable key (the reset-mail-key command):
// every open link is revoked and every encrypted copy cleared, audited, then a new key is
// written. It refuses while the current key is valid. Returns the number of links revoked.
func ResetMailKey(ctx context.Context, pool *pgxpool.Pool, path string) (int, error) {
	if _, err := readMailKey(path); err == nil {
		return 0, ErrMailKeyValid
	}
	var revoked int64
	err := platform.InTx(ctx, pool, func(tx pgx.Tx, now time.Time) error {
		tag, err := tx.Exec(ctx, "UPDATE activation_tokens SET revoked_at = $1 WHERE used_at IS NULL AND revoked_at IS NULL", now)
		if err != nil {
			return err
		}
		revoked = tag.RowsAffected()
		if _, err := tx.Exec(ctx, "UPDATE email_outbox SET sealed_token = NULL WHERE sealed_token IS NOT NULL"); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "MOCK_EMAIL_KEY_RESET", Entity: "mock_email_key",
			Details: map[string]any{"revokedLinks": revoked}})
	})
	if err != nil {
		return 0, err
	}
	// Nothing is encrypted any more, so a failure from here on is safe: the next start creates the key.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return int(revoked), err
	}
	_, err = writeMailKey(path)
	return int(revoked), err
}

// seal encrypts token for one mock email of one employee (AES-256-GCM, random nonce). The ids are
// authenticated, so a ciphertext copied to another row does not open.
func seal(key []byte, token string, mail, employee uuid.UUID) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(token), append(mail[:], employee[:]...)), nil
}

func openSealed(key, sealed []byte, mail, employee uuid.UUID) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	if len(sealed) < gcm.NonceSize() {
		return "", errors.New("sealed token too short")
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], append(mail[:], employee[:]...))
	return string(plain), err
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
