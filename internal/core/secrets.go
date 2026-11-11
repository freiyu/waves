package core

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

var idEnc = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// NewID returns prefix_ plus 16 random base32 characters (80 bits).
func NewID(prefix string) string {
	b := make([]byte, 10)
	rand.Read(b) // crypto/rand.Read never fails on supported platforms
	return prefix + "_" + idEnc.EncodeToString(b)
}

// NewSecret returns a 256-bit random token, URL-safe, with a readable prefix.
func NewSecret(prefix string) string {
	b := make([]byte, 32)
	rand.Read(b)
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(b)
}

// HashSecret is how every token is stored: hex SHA-256.
func HashSecret(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// SameHash compares two hashes in constant time.
func SameHash(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// derive returns HMAC-SHA256(key, label) as hex; used for the CSRF token
// derived from the session secret.
func derive(key, label string) string {
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(label))
	return hex.EncodeToString(m.Sum(nil))
}

// SecretKind tells which credential family a bearer value claims to be by its
// prefix. It is only a hint for auditing bypass attempts; the hash lookup decides.
func SecretKind(s string) string {
	i := strings.IndexByte(s, '_')
	if i < 0 {
		return ""
	}
	return s[:i]
}
