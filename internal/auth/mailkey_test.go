package auth

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestSealedTokensOpenOnlyForTheirOwnRow(t *testing.T) {
	mail, emp := uuid.New(), uuid.New()
	sealed, err := seal(testMailKey, "the-token", mail, emp)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := openSealed(testMailKey, sealed, mail, emp); err != nil || got != "the-token" {
		t.Fatalf("open = %q, %v", got, err)
	}
	if _, err := openSealed(testMailKey, sealed, uuid.New(), emp); err == nil {
		t.Error("opened under another mock email")
	}
	if _, err := openSealed(testMailKey, sealed, mail, uuid.New()); err == nil {
		t.Error("opened for another employee")
	}
	if _, err := openSealed(bytes.Repeat([]byte{9}, 32), sealed, mail, emp); err == nil {
		t.Error("opened with another key")
	}
}

func TestLoadMailKey(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)

	path := filepath.Join(t.TempDir(), "mock-email.key")
	key, err := LoadMailKey(ctx, env.db.App, path)
	if err != nil || len(key) != 32 {
		t.Fatalf("first start: %d bytes, %v; want a new 32-byte key", len(key), err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v; want mode 0600", info, err)
	}
	again, err := LoadMailKey(ctx, env.db.App, path)
	if err != nil || !bytes.Equal(again, key) {
		t.Fatal("the key changed on the next start")
	}

	// A lost key is never silently replaced while links are encrypted with it.
	env.issue(env.employee().ID)
	lost := filepath.Join(t.TempDir(), "mock-email.key")
	if _, err := LoadMailKey(ctx, env.db.App, lost); !errors.Is(err, ErrMailKeyUnavailable) {
		t.Fatalf("lost key with encrypted links: %v; want ErrMailKeyUnavailable", err)
	}
	if _, err := os.Stat(lost); !os.IsNotExist(err) {
		t.Fatal("a new key was written while encrypted links exist")
	}

	// A damaged or unreadable key is never overwritten.
	short := filepath.Join(t.TempDir(), "mock-email.key")
	if err := os.WriteFile(short, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMailKey(ctx, env.db.App, short); !errors.Is(err, ErrMailKeyUnavailable) {
		t.Fatalf("wrong size: %v", err)
	}
	if data, _ := os.ReadFile(short); string(data) != "short" {
		t.Fatal("the damaged key was overwritten")
	}
	unreadable := t.TempDir() // a directory: reading it fails
	if _, err := LoadMailKey(ctx, env.db.App, unreadable); !errors.Is(err, ErrMailKeyUnavailable) {
		t.Fatalf("unreadable: %v", err)
	}
}

func TestResetMailKey(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	valid := filepath.Join(t.TempDir(), "mock-email.key")
	if err := os.WriteFile(valid, testMailKey, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResetMailKey(ctx, env.db.App, valid); !errors.Is(err, ErrMailKeyValid) {
		t.Fatalf("with a valid key: %v; want ErrMailKeyValid", err)
	}

	pending, done := env.employee(), env.employee()
	env.issue(pending.ID)
	env.issue(done.ID)
	env.exec("UPDATE activation_tokens SET used_at = now() WHERE employee_id = $1", done.ID)

	damaged := filepath.Join(t.TempDir(), "mock-email.key")
	if err := os.WriteFile(damaged, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := ResetMailKey(ctx, env.db.App, damaged)
	if err != nil || n != 1 {
		t.Fatalf("reset: %d, %v; want 1 open link revoked", n, err)
	}
	if env.count("SELECT count(*) FROM activation_tokens WHERE used_at IS NULL AND revoked_at IS NULL") != 0 ||
		env.count("SELECT count(*) FROM email_outbox WHERE sealed_token IS NOT NULL") != 0 {
		t.Fatal("open links or encrypted copies remain")
	}
	if env.count("SELECT count(*) FROM activation_tokens WHERE employee_id = $1 AND used_at IS NOT NULL AND revoked_at IS NULL", done.ID) != 1 {
		t.Fatal("a used link was changed")
	}
	if env.count("SELECT count(*) FROM audit_events WHERE action = 'MOCK_EMAIL_KEY_RESET' AND actor_id IS NULL AND (details->>'revokedLinks')::int = 1") != 1 {
		t.Fatal("reset not audited")
	}
	key, err := LoadMailKey(ctx, env.db.App, damaged)
	if err != nil || len(key) != 32 {
		t.Fatalf("after reset: %d bytes, %v; want the new key", len(key), err)
	}
}
