package auth

import (
	"strings"
	"testing"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("hash %q does not carry the OWASP minimum argon2id parameters", hash)
	}
	if ok, err := VerifyPassword(hash, "correct horse battery"); err != nil || !ok {
		t.Fatalf("correct password: ok=%v err=%v", ok, err)
	}
	if ok, err := VerifyPassword(hash, "correct horse batterY"); err != nil || ok {
		t.Fatalf("wrong password: ok=%v err=%v", ok, err)
	}
}

func TestHashesUseAFreshSalt(t *testing.T) {
	a, _ := HashPassword("same password here")
	b, _ := HashPassword("same password here")
	if a == b {
		t.Fatal("two hashes of the same password are equal; want different salts")
	}
}

func TestVerifyRejectsMalformedOrUnsafeHashes(t *testing.T) {
	valid, _ := HashPassword("correct horse battery")
	parts := strings.Split(valid, "$") // "", argon2id, v=19, params, salt, hash
	for name, hash := range map[string]string{
		"empty":             "",
		"bcrypt":            "$2a$10$abcdefghijklmnopqrstuv",
		"argon2i":           strings.Replace(valid, "argon2id", "argon2i", 1),
		"wrong version":     strings.Replace(valid, "v=19", "v=16", 1),
		"bad salt encoding": strings.Join([]string{"", parts[1], parts[2], parts[3], "!!", parts[5]}, "$"),
		"huge memory cost":  strings.Replace(valid, "m=19456", "m=99999999", 1),
		"missing parts":     strings.Join(parts[:4], "$"),
	} {
		t.Run(name, func(t *testing.T) {
			if ok, err := VerifyPassword(hash, "correct horse battery"); ok || err == nil {
				t.Fatalf("ok=%v err=%v; want rejected with an error", ok, err)
			}
		})
	}
}

func TestNewTemporaryPassword(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		pw, err := NewTemporaryPassword()
		if err != nil {
			t.Fatal(err)
		}
		if len([]rune(pw)) != 16 {
			t.Fatalf("%q has %d characters; want 16", pw, len([]rune(pw)))
		}
		if strings.ContainsAny(pw, "0O1lI") {
			t.Fatalf("%q contains a look-alike character", pw)
		}
		seen[pw] = true
	}
	if len(seen) != 50 {
		t.Fatalf("got %d distinct passwords out of 50", len(seen))
	}
}

// testutil cannot import auth (cycle), so it carries a precomputed hash; this keeps it honest.
func TestTestutilPasswordHashMatches(t *testing.T) {
	ok, err := VerifyPassword(testutil.PasswordHash, testutil.Password)
	if err != nil || !ok {
		t.Fatalf("testutil.PasswordHash does not verify testutil.Password: %v %v", ok, err)
	}
}
