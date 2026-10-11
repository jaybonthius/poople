package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	passwordMemory      = 19 * 1024
	passwordIterations  = 2
	passwordParallelism = 1
	passwordSaltBytes   = 16
	passwordKeyBytes    = 32
	passwordPrefix      = "$argon2id$v=19$m=19456,t=2,p=1$"

	// PasswordMinLength and PasswordMaxLength count characters.
	PasswordMinLength = 10
	PasswordMaxLength = 128
)

// Usernames are ASCII so SQLite's NOCASE collation compares them case-insensitively.
var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,23}$`)

// normalizeUsername trims surrounding space and keeps the username's case for display.
func normalizeUsername(username string) (string, bool) {
	// Bound the raw input before trimming.
	if len(username) > 256 || !utf8.ValidString(username) {
		return "", false
	}
	username = strings.TrimSpace(username)
	return username, usernamePattern.MatchString(username)
}

func validatePassword(password string) error {
	if len(password) > PasswordMaxLength*utf8.UTFMax || !utf8.ValidString(password) {
		return ErrPasswordInvalid
	}
	length := utf8.RuneCountInString(password)
	if length < PasswordMinLength || length > PasswordMaxLength {
		return ErrPasswordInvalid
	}
	return nil
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, passwordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, passwordIterations, passwordMemory, passwordParallelism, passwordKeyBytes)
	return passwordPrefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}

func passwordMatches(password, hash string) bool {
	// Accept only this application's fixed PHC format. Never let stored input
	// control Argon2 memory, iterations, parallelism or allocation lengths.
	if len(hash) != len(passwordPrefix)+22+1+43 || !strings.HasPrefix(hash, passwordPrefix) {
		return false
	}
	saltText, keyText, ok := strings.Cut(hash[len(passwordPrefix):], "$")
	if !ok || len(saltText) != 22 || len(keyText) != 43 {
		return false
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(saltText)
	if err != nil || len(salt) != passwordSaltBytes {
		return false
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(keyText)
	if err != nil || len(key) != passwordKeyBytes {
		return false
	}
	candidate := argon2.IDKey([]byte(password), salt, passwordIterations, passwordMemory, passwordParallelism, passwordKeyBytes)
	return subtle.ConstantTimeCompare(candidate, key) == 1
}
