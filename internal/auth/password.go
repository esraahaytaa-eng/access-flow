package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// OWASP minimum argon2id parameters (A4): 19 MiB memory, 2 iterations, 1 thread.
const (
	argonMemoryKiB = 19 * 1024
	argonTime      = 2
	argonThreads   = 1
	argonSaltLen   = 16
	argonKeyLen    = 32
	// Upper bounds when reading stored hashes, so a corrupted row cannot exhaust memory.
	maxArgonMemoryKiB = 256 * 1024
	maxArgonTime      = 10
)

var errBadHash = errors.New("unsupported password hash")

// HashPassword returns an argon2id hash in PHC format, parameters included.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword checks pw against an encoded hash in constant time, using the
// parameters stored with the hash.
func VerifyPassword(encoded, pw string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return false, errBadHash
	}
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false, errBadHash
	}
	if memory == 0 || memory > maxArgonMemoryKiB || iterations == 0 || iterations > maxArgonTime || threads == 0 {
		return false, errBadHash
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return false, errBadHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 || len(want) > 64 {
		return false, errBadHash
	}
	got := argon2.IDKey([]byte(pw), salt, iterations, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// Look-alike characters (0, O, 1, l, I) are left out so a password read from a file or
// screen is typed correctly.
const temporaryAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// NewTemporaryPassword returns 16 random characters (about 93 bits) (A4).
func NewTemporaryPassword() (string, error) {
	out := make([]byte, 16)
	buf := make([]byte, 1)
	for i := 0; i < len(out); {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		// Rejection sampling keeps every character equally likely.
		if int(buf[0]) >= 256-256%len(temporaryAlphabet) {
			continue
		}
		out[i] = temporaryAlphabet[int(buf[0])%len(temporaryAlphabet)]
		i++
	}
	return string(out), nil
}
