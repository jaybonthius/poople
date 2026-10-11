package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"poople/store/zz"

	"zombiezen.com/go/sqlite"
)

// ErrInvalidReset covers unknown, used and expired reset links alike.
var ErrInvalidReset = errors.New("password reset link is invalid or no longer available")

const resetDuration = 24 * time.Hour

// IssuePasswordReset lets adminID give accountID a single-use link to set a new
// password. It returns the raw token for /reset?token=…; only its hash is stored.
// Deliver it privately, never through logs or saved outcomes.
func (s *Service) IssuePasswordReset(ctx context.Context, adminID, accountID int64) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	if err := s.db.Write(ctx, func(conn *sqlite.Conn) error {
		if err := requireAdmin(conn, adminID); err != nil {
			return err
		}
		target, err := accountByID(conn, accountID)
		if err != nil {
			return err
		}
		if target == nil {
			return ErrAccountNotFound
		}
		now := time.Now()
		return zz.OnceCreatePasswordReset(conn, zz.CreatePasswordResetParams{
			TokenHash: tokenHash(token), AccountId: accountID, CreatedBy: &adminID,
			CreatedAt: now.UnixMilli(), ExpiresAt: now.Add(resetDuration).UnixMilli(),
		})
	}); err != nil {
		if errors.Is(err, ErrAdminRequired) || errors.Is(err, ErrAccountNotFound) {
			return "", err
		}
		return "", fmt.Errorf("issue password reset: %w", err)
	}
	return token, nil
}

// PasswordResetFor returns the username a reset link is for without consuming it.
func (s *Service) PasswordResetFor(ctx context.Context, token string) (string, error) {
	if !validToken(token) {
		return "", ErrInvalidReset
	}
	var username string
	if err := s.db.Read(ctx, func(conn *sqlite.Conn) error {
		row, err := zz.OnceGetPasswordReset(conn, zz.GetPasswordResetParams{TokenHash: tokenHash(token), Now: time.Now().UnixMilli()})
		if err != nil || row == nil {
			return err
		}
		username = row.Username
		return nil
	}); err != nil {
		return "", fmt.Errorf("load password reset: %w", err)
	}
	if username == "" {
		return "", ErrInvalidReset
	}
	return username, nil
}

// ResetPassword consumes the link, sets the password, revokes the account's other
// sessions and signs this browser in exactly as Login does.
func (s *Service) ResetPassword(ctx context.Context, token, browserToken, password string) (*Account, Session, error) {
	if !validToken(token) {
		return nil, Session{}, ErrInvalidReset
	}
	if err := validatePassword(password); err != nil {
		return nil, Session{}, err
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		return nil, Session{}, err
	}
	session, err := newSession()
	if err != nil {
		return nil, Session{}, err
	}
	hash := tokenHash(token)
	var account *Account
	if err := s.db.Write(ctx, func(conn *sqlite.Conn) error {
		now := time.Now().UnixMilli()
		reset, err := zz.OnceGetPasswordReset(conn, zz.GetPasswordResetParams{TokenHash: hash, Now: now})
		if err != nil {
			return err
		}
		if reset == nil {
			return ErrInvalidReset
		}
		if err := zz.OnceUsePasswordReset(conn, zz.UsePasswordResetParams{TokenHash: hash, Now: &now}); err != nil {
			return err
		}
		if conn.Changes() != 1 {
			return ErrInvalidReset
		}
		row, err := accountByID(conn, reset.ResetAccountId)
		if err != nil {
			return err
		}
		if row == nil {
			return ErrInvalidReset
		}
		if err := zz.OnceSetAccountPassword(conn, zz.SetAccountPasswordParams{PasswordHash: passwordHash, Id: row.ID}); err != nil {
			return err
		}
		if err := zz.OnceDeletePlayerSessions(conn, row.PlayerID); err != nil {
			return err
		}
		if err := s.signIn(conn, browserToken, row.PlayerID, session); err != nil {
			return err
		}
		account = row.account()
		return nil
	}); err != nil {
		if errors.Is(err, ErrInvalidReset) {
			return nil, Session{}, err
		}
		return nil, Session{}, fmt.Errorf("reset password: %w", err)
	}
	// Wake views so the account's revoked sessions stop streaming.
	s.notifyAccounts()
	return account, session, nil
}
