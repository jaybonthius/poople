package identity

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"poople/store/zz"

	"zombiezen.com/go/sqlite"
)

// ErrDeleteOwnAccount stops an admin removing the account they are signed in with.
var ErrDeleteOwnAccount = errors.New("admins cannot delete their own account")

// ListedAccount is one row of the admin's user list.
type ListedAccount struct {
	Account
	PlayerID    int64
	CreatedAt   time.Time
	GamesPlayed int64 // games with at least one accepted guess
}

// AccountChanges closes after the next committed change to any account: sign-up,
// username, picture, password, revoked sessions or deletion. Take it before reading a view.
func (s *Service) AccountChanges() <-chan struct{} {
	s.accountsMu.Lock()
	defer s.accountsMu.Unlock()
	return s.accountsChanged
}

// notifyAccounts wakes every view of account state. Call it only after the change commits.
func (s *Service) notifyAccounts() {
	s.accountsMu.Lock()
	defer s.accountsMu.Unlock()
	close(s.accountsChanged)
	s.accountsChanged = make(chan struct{})
}

// ChangePassword checks the current password of the session's account, sets the
// new one and revokes the account's other sessions, keeping this one. A wrong
// current password is ErrInvalidCredentials.
func (s *Service) ChangePassword(ctx context.Context, sessionToken, current, next string) error {
	if !validToken(sessionToken) {
		return ErrInvalidSession
	}
	var row *accountRow
	if err := s.db.Read(ctx, func(conn *sqlite.Conn) error {
		playerID, err := sessionPlayer(conn, sessionToken)
		if err != nil || playerID == 0 {
			return err
		}
		row, err = accountForPlayer(conn, playerID)
		return err
	}); err != nil {
		return fmt.Errorf("load account: %w", err)
	}
	if row == nil {
		return ErrInvalidSession
	}
	// Bound the input before Argon2 sees it.
	if len(current) > PasswordMaxLength*utf8.UTFMax {
		return ErrInvalidCredentials
	}
	matches, err := passwordMatches(current, row.PasswordHash)
	if err != nil {
		return err
	}
	if !matches {
		return ErrInvalidCredentials
	}
	if err := validatePassword(next); err != nil {
		return err
	}
	passwordHash, err := hashPassword(next)
	if err != nil {
		return err
	}
	if err := s.db.Write(ctx, func(conn *sqlite.Conn) error {
		playerID, err := sessionPlayer(conn, sessionToken)
		if err != nil {
			return err
		}
		if playerID != row.PlayerID {
			return ErrInvalidSession
		}
		latest, err := accountByID(conn, row.ID)
		if err != nil {
			return err
		}
		// The password changed or the account went since the check above.
		if latest == nil || latest.PasswordHash != row.PasswordHash {
			return ErrInvalidCredentials
		}
		if err := zz.OnceSetAccountPassword(conn, zz.SetAccountPasswordParams{PasswordHash: passwordHash, Id: row.ID}); err != nil {
			return err
		}
		return zz.OnceDeleteOtherPlayerSessions(conn, zz.DeleteOtherPlayerSessionsParams{PlayerId: row.PlayerID, TokenHash: tokenHash(sessionToken)})
	}); err != nil {
		if errors.Is(err, ErrInvalidSession) || errors.Is(err, ErrInvalidCredentials) {
			return err
		}
		return fmt.Errorf("change password: %w", err)
	}
	// Wake views so the revoked sessions stop streaming.
	s.notifyAccounts()
	return nil
}

// ChangeUsername renames accountID under sign-up's username rules and returns
// the stored name. Names are unique case-insensitively, but an account may
// change the case of its own. The old name is free for others afterwards.
func (s *Service) ChangeUsername(ctx context.Context, accountID int64, username string) (string, error) {
	normalized, valid := normalizeUsername(username)
	if !valid {
		return "", ErrUsernameInvalid
	}
	changed := false
	if err := s.db.Write(ctx, func(conn *sqlite.Conn) error {
		current, err := accountByID(conn, accountID)
		if err != nil {
			return err
		}
		if current == nil {
			return ErrAccountNotFound
		}
		if current.Username == normalized {
			return nil
		}
		taken, err := accountByUsername(conn, normalized)
		if err != nil {
			return err
		}
		if taken != nil && taken.ID != accountID {
			return ErrUsernameTaken
		}
		changed = true
		return zz.OnceSetAccountUsername(conn, zz.SetAccountUsernameParams{Username: normalized, Id: accountID})
	}); err != nil {
		if errors.Is(err, ErrAccountNotFound) || errors.Is(err, ErrUsernameTaken) {
			return "", err
		}
		return "", fmt.Errorf("change username: %w", err)
	}
	// Wake every view that shows names: leaderboards, the admin list and mastheads.
	if changed {
		s.notifyAccounts()
	}
	return normalized, nil
}

// ListAccounts returns every account by username for adminID.
func (s *Service) ListAccounts(ctx context.Context, adminID int64) ([]ListedAccount, error) {
	var accounts []ListedAccount
	err := s.db.Read(ctx, func(conn *sqlite.Conn) error {
		if err := requireAdmin(conn, adminID); err != nil {
			return err
		}
		rows, err := zz.OnceListAccounts(conn)
		if err != nil {
			return err
		}
		for _, row := range rows {
			listed := ListedAccount{
				Account:     Account{ID: row.ListedAccountId, Username: row.Username, IsAdmin: row.IsAdmin == 1},
				PlayerID:    row.PlayerId,
				CreatedAt:   time.UnixMilli(row.CreatedAt).UTC(),
				GamesPlayed: row.GamesPlayed,
			}
			if row.AvatarHash != nil {
				listed.AvatarHash = *row.AvatarHash
			}
			accounts = append(accounts, listed)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrAdminRequired) {
			return nil, err
		}
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	return accounts, nil
}

// AdminRemoveAvatar lets adminID delete accountID's picture.
func (s *Service) AdminRemoveAvatar(ctx context.Context, adminID, accountID int64) error {
	if err := s.db.Write(ctx, func(conn *sqlite.Conn) error {
		if err := requireAdmin(conn, adminID); err != nil {
			return err
		}
		if err := zz.OnceRemoveAccountAvatar(conn, accountID); err != nil {
			return err
		}
		if conn.Changes() != 1 {
			return ErrAccountNotFound
		}
		return nil
	}); err != nil {
		if errors.Is(err, ErrAdminRequired) || errors.Is(err, ErrAccountNotFound) {
			return err
		}
		return fmt.Errorf("remove picture: %w", err)
	}
	s.notifyAccounts()
	return nil
}

// DeleteAccount lets adminID remove accountID with its sessions and reset links.
// The player row and its games stay, now anonymous.
func (s *Service) DeleteAccount(ctx context.Context, adminID, accountID int64) error {
	if adminID == accountID {
		return ErrDeleteOwnAccount
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
		if err := zz.OnceDeletePlayerSessions(conn, target.PlayerID); err != nil {
			return err
		}
		// Reset links cascade with the account.
		return zz.OnceDeleteAccount(conn, accountID)
	}); err != nil {
		if errors.Is(err, ErrAdminRequired) || errors.Is(err, ErrAccountNotFound) {
			return err
		}
		return fmt.Errorf("delete account: %w", err)
	}
	s.notifyAccounts()
	return nil
}

// IssuedPasswordReset authorizes showing a just-issued reset link again: token
// must be an unused, unexpired link adminID issued for accountID. It returns the
// account's username. The raw token travels only in the admin's redirect URL.
func (s *Service) IssuedPasswordReset(ctx context.Context, adminID, accountID int64, token string) (string, error) {
	if !validToken(token) {
		return "", ErrInvalidReset
	}
	var username string
	err := s.db.Read(ctx, func(conn *sqlite.Conn) error {
		if err := requireAdmin(conn, adminID); err != nil {
			return err
		}
		row, err := zz.OnceGetIssuedPasswordReset(conn, zz.GetIssuedPasswordResetParams{TokenHash: tokenHash(token), Now: time.Now().UnixMilli()})
		if err != nil {
			return err
		}
		if row == nil || row.IssuedAccountId != accountID || row.CreatedBy == nil || *row.CreatedBy != adminID {
			return ErrInvalidReset
		}
		target, err := accountByID(conn, accountID)
		if err != nil {
			return err
		}
		if target == nil {
			return ErrInvalidReset
		}
		username = target.Username
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrAdminRequired) || errors.Is(err, ErrInvalidReset) {
			return "", err
		}
		return "", fmt.Errorf("load issued reset: %w", err)
	}
	return username, nil
}
