package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"time"
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

// passwordSlots bounds concurrent Argon2 hashes, each holding passwordMemory
// KiB, so parallel log-in attempts cannot exhaust memory. A hash waits at most
// passwordSlotWait for a slot, then fails with ErrPasswordBusy.
var passwordSlots = make(chan struct{}, runtime.GOMAXPROCS(0))

const passwordSlotWait = time.Second

func passwordKey(password string, salt []byte) ([]byte, error) {
	wait := time.NewTimer(passwordSlotWait)
	defer wait.Stop()
	select {
	case passwordSlots <- struct{}{}:
	case <-wait.C:
		return nil, ErrPasswordBusy
	}
	defer func() { <-passwordSlots }()
	return argon2.IDKey([]byte(password), salt, passwordIterations, passwordMemory, passwordParallelism, passwordKeyBytes), nil
}

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
	key, err := passwordKey(password, salt)
	if err != nil {
		return "", err
	}
	return passwordPrefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}

// passwordMatches reports whether password matches hash; its only error is ErrPasswordBusy.
func passwordMatches(password, hash string) (bool, error) {
	// Accept only this application's fixed PHC format. Never let stored input
	// control Argon2 memory, iterations, parallelism or allocation lengths.
	if len(hash) != len(passwordPrefix)+22+1+43 || !strings.HasPrefix(hash, passwordPrefix) {
		return false, nil
	}
	saltText, keyText, ok := strings.Cut(hash[len(passwordPrefix):], "$")
	if !ok || len(saltText) != 22 || len(keyText) != 43 {
		return false, nil
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(saltText)
	if err != nil || len(salt) != passwordSaltBytes {
		return false, nil
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(keyText)
	if err != nil || len(key) != passwordKeyBytes {
		return false, nil
	}
	candidate, err := passwordKey(password, salt)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(candidate, key) == 1, nil
}
