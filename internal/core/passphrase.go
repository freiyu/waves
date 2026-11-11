package core

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// MinPassphraseLen is the shortest accepted approval passphrase. The hash is
// readable by same-user processes, so a short passphrase could be cracked offline.
const MinPassphraseLen = 12

// CheckPassphraseStrength refuses passphrases that are too short or trivial.
func CheckPassphraseStrength(p string) error {
	if utf8.RuneCountInString(p) < MinPassphraseLen {
		return fmt.Errorf("the passphrase must have at least %d characters", MinPassphraseLen)
	}
	if strings.Count(p, string([]rune(p)[0])) == utf8.RuneCountInString(p) {
		return errors.New("the passphrase must not repeat one character")
	}
	return nil
}

// idKey runs argon2id and hands its working memory (19 MiB by default) back
// to the OS at once, so step-ups do not pile up in the resident set.
func idKey(pass string, salt []byte, a Argon) []byte {
	out := argon2.IDKey([]byte(pass), salt, a.Time, a.MemKiB, a.Threads, 64)
	debug.FreeOSMemory()
	return out
}

// hashPassphrase returns the stored verifier. argon2id produces 64 bytes: the
// first 32 are stored, the last 32 are the audit checkpoint key, never stored.
func hashPassphrase(a Argon, pass string) (stored string, key []byte) {
	salt := make([]byte, 16)
	rand.Read(salt)
	out := idKey(pass, salt, a)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", a.MemKiB, a.Time, a.Threads, enc.EncodeToString(salt), enc.EncodeToString(out[:32])), out[32:]
}

// verifyPassphrase checks pass against stored and returns the checkpoint key.
func verifyPassphrase(stored, pass string) (bool, []byte, error) {
	parts := strings.Split(stored, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, nil, errors.New("stored passphrase hash has an unknown format")
	}
	var a Argon
	var threads int
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &a.MemKiB, &a.Time, &threads); err != nil {
		return false, nil, fmt.Errorf("stored passphrase parameters: %w", err)
	}
	if threads < 1 || threads > 255 || a.Time < 1 || a.MemKiB < 8 {
		return false, nil, errors.New("stored passphrase parameters are out of range")
	}
	a.Threads = uint8(threads)
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil {
		return false, nil, fmt.Errorf("stored passphrase salt: %w", err)
	}
	want, err := enc.DecodeString(parts[5])
	if err != nil || len(want) != 32 {
		return false, nil, errors.New("stored passphrase hash is damaged")
	}
	out := idKey(pass, salt, a)
	if subtle.ConstantTimeCompare(out[:32], want) != 1 {
		return false, nil, nil
	}
	return true, out[32:], nil
}
