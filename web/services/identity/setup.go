package identity

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"sync"
	"time"

	"poople/store/zz"

	"zombiezen.com/go/sqlite"
)

const setupDuration = 15 * time.Minute

// Setup gates creation of the first account, which is the admin.
type Setup struct {
	mu        sync.Mutex
	users     *Service
	tokenHash [32]byte
	expiresAt time.Time
	consumed  bool
}

// NewSetup returns a token only while no account exists. Show it only to the
// operator, never in a public page or request log. Restart invalidates it.
func NewSetup(ctx context.Context, users *Service) (*Setup, string, error) {
	count, err := users.AccountCount(ctx)
	if err != nil {
		return nil, "", err
	}
	setup := &Setup{users: users, consumed: count != 0}
	if setup.consumed {
		return setup, "", nil
	}
	token, err := randomToken()
	if err != nil {
		return nil, "", err
	}
	setup.tokenHash = sha256.Sum256([]byte(token))
	setup.expiresAt = time.Now().UTC().Add(setupDuration)
	return setup, token, nil
}

func (s *Setup) valid(token string) bool {
	if s.consumed || !time.Now().Before(s.expiresAt) || !validToken(token) {
		return false
	}
	candidate := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(candidate[:], s.tokenHash[:]) == 1
}

// Valid gates the setup page; CreateAdmin repeats the checks before committing.
func (s *Setup) Valid(ctx context.Context, token string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.valid(token) {
		return false, nil
	}
	count, err := s.users.AccountCount(ctx)
	return count == 0 && err == nil, err
}

// CreateAdmin creates the first account as admin, attached to the browser's
// player with a fresh session like SignUp, then consumes the operator token.
func (s *Setup) CreateAdmin(ctx context.Context, token, browserToken, username, password string) (*Account, Session, error) {
	valid, err := s.Valid(ctx, token)
	if err != nil {
		return nil, Session{}, err
	}
	if !valid {
		return nil, Session{}, ErrInvalidSetupToken
	}
	normalized, valid := normalizeUsername(username)
	if !valid {
		return nil, Session{}, ErrUsernameInvalid
	}
	if err := validatePassword(password); err != nil {
		return nil, Session{}, err
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		return nil, Session{}, err
	}
	fresh, err := newSession()
	if err != nil {
		return nil, Session{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var account *Account
	if err := s.users.db.Write(ctx, func(conn *sqlite.Conn) error {
		if !s.valid(token) {
			return ErrInvalidSetupToken
		}
		count, err := zz.OnceCountAccounts(conn)
		if err != nil {
			return err
		}
		if count != 0 {
			return ErrSetupComplete
		}
		account, err = createAccount(conn, browserToken, fresh, normalized, passwordHash, true)
		return err
	}); err != nil {
		return nil, Session{}, fmt.Errorf("create admin account: %w", err)
	}
	s.consumed = true
	s.tokenHash = [32]byte{}
	s.users.notifyAccounts()
	return account, fresh, nil
}
