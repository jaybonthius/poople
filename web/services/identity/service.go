// Package identity owns players, sessions, accounts, setup, password-reset links and avatars.
package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"poople/store"
	"poople/store/zz"

	"zombiezen.com/go/sqlite"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrInvalidSession     = errors.New("invalid session")
	ErrInvalidSetupToken  = errors.New("invalid setup token")
	ErrSetupComplete      = errors.New("initial setup is already complete")
	ErrSetupRequired      = errors.New("sign-up opens after the admin account is set up")
	ErrUsernameInvalid    = errors.New("username must be 3–24 letters, numbers, dots, underscores or hyphens, starting with a letter or number")
	ErrPasswordInvalid    = errors.New("password must be 10–128 characters")
	ErrUsernameTaken      = errors.New("that username is taken")
	ErrAlreadySignedUp    = errors.New("this player already has an account")
	ErrAdminRequired      = errors.New("admin access required")
	ErrAccountNotFound    = errors.New("account not found")
	// ErrPasswordBusy refuses a password check while every hashing slot is taken.
	ErrPasswordBusy = errors.New("too many password checks in progress")
)

// MergePlayers moves fromPlayerID's games to toPlayerID inside the log-in
// transaction, keeping toPlayerID's game wherever both played a puzzle.
// Returning an error rolls back the whole log-in; otherwise identity calls
// committed after the commit to wake the views the merge changed.
type MergePlayers func(conn *sqlite.Conn, fromPlayerID, toPlayerID int64) (committed func(), err error)

// Viewer is who sent a request: a player with a live session, and their account if they signed up.
type Viewer struct {
	PlayerID int64
	Account  *Account
}

// Account is the public part of an account; it never carries the password hash.
type Account struct {
	ID         int64
	Username   string
	IsAdmin    bool
	AvatarHash string // empty when the account has no picture
}

// AvatarPath is the account's cacheable picture URL, or "" without a picture.
func (a *Account) AvatarPath() string {
	if a == nil || a.AvatarHash == "" {
		return ""
	}
	return "/avatars/" + strconv.FormatInt(a.ID, 10) + "/" + a.AvatarHash + ".png"
}

type accountRow struct {
	ID, PlayerID int64
	Username     string
	PasswordHash string
	IsAdmin      int64
	AvatarHash   *string
}

func (row *accountRow) account() *Account {
	account := &Account{ID: row.ID, Username: row.Username, IsAdmin: row.IsAdmin == 1}
	if row.AvatarHash != nil {
		account.AvatarHash = *row.AvatarHash
	}
	return account
}

func accountByID(conn *sqlite.Conn, id int64) (*accountRow, error) {
	row, err := zz.OnceGetAccountById(conn, id)
	if err != nil || row == nil {
		return nil, err
	}
	return &accountRow{row.AccountId, row.PlayerId, row.Username, row.PasswordHash, row.IsAdmin, row.AvatarHash}, nil
}

func accountByUsername(conn *sqlite.Conn, username string) (*accountRow, error) {
	row, err := zz.OnceGetAccountByUsername(conn, username)
	if err != nil || row == nil {
		return nil, err
	}
	return &accountRow{row.AccountId, row.PlayerId, row.Username, row.PasswordHash, row.IsAdmin, row.AvatarHash}, nil
}

func accountForPlayer(conn *sqlite.Conn, playerID int64) (*accountRow, error) {
	row, err := zz.OnceGetPlayerAccount(conn, playerID)
	if err != nil || row == nil {
		return nil, err
	}
	return &accountRow{row.AccountId, row.PlayerId, row.Username, row.PasswordHash, row.IsAdmin, row.AvatarHash}, nil
}

// Service borrows storage; the application closes it after HTTP users stop.
type Service struct {
	db        *store.Store
	cookies   Cookies
	merge     MergePlayers
	dummyHash string

	accountsMu      sync.Mutex
	accountsChanged chan struct{}
}

// New prepares identity and deletes expired sessions. merge may be nil until games exist.
func New(ctx context.Context, database *store.Store, cookies Cookies, merge MergePlayers) (*Service, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if database == nil {
		return nil, errors.New("identity requires storage")
	}
	dummy, err := randomToken()
	if err != nil {
		return nil, err
	}
	dummyHash, err := hashPassword(dummy)
	if err != nil {
		return nil, err
	}
	if err := database.Write(ctx, func(conn *sqlite.Conn) error {
		return zz.OnceDeleteExpiredSessions(conn, time.Now().UnixMilli())
	}); err != nil {
		return nil, fmt.Errorf("clean expired sessions: %w", err)
	}
	return &Service{db: database, cookies: cookies, merge: merge, dummyHash: dummyHash, accountsChanged: make(chan struct{})}, nil
}

// Cookies sets and clears the session cookie with the service's attributes.
func (s *Service) Cookies() Cookies { return s.cookies }

// AccountCount reports how many accounts exist; zero means setup is pending.
func (s *Service) AccountCount(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.Read(ctx, func(conn *sqlite.Conn) (err error) {
		count, err = zz.OnceCountAccounts(conn)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("count accounts: %w", err)
	}
	return count, nil
}

// ViewerForSession resolves a session token without writing. Streams call it on
// every snapshot so a revoked session stops its view with ErrInvalidSession.
func (s *Service) ViewerForSession(ctx context.Context, token string) (*Viewer, error) {
	if !validToken(token) {
		return nil, ErrInvalidSession
	}
	var viewer *Viewer
	if err := s.db.Read(ctx, func(conn *sqlite.Conn) error {
		playerID, err := sessionPlayer(conn, token)
		if err != nil || playerID == 0 {
			return err
		}
		viewer = &Viewer{PlayerID: playerID}
		row, err := accountForPlayer(conn, playerID)
		if err != nil {
			return err
		}
		if row != nil {
			viewer.Account = row.account()
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}
	if viewer == nil {
		return nil, ErrInvalidSession
	}
	return viewer, nil
}

// EnsurePlayer returns the request's player, first creating a player row and a
// long-lived anonymous session and setting its cookie. Call it from commands,
// never page views; WithPlayer must run upstream.
func (s *Service) EnsurePlayer(w http.ResponseWriter, r *http.Request) (int64, error) {
	if viewer := ViewerFrom(r.Context()); viewer != nil {
		return viewer.PlayerID, nil
	}
	session, err := newSession()
	if err != nil {
		return 0, err
	}
	var playerID int64
	if err := s.db.Write(r.Context(), func(conn *sqlite.Conn) (err error) {
		playerID, err = createPlayer(conn, session)
		return err
	}); err != nil {
		return 0, fmt.Errorf("create player: %w", err)
	}
	s.cookies.Set(w, session)
	return playerID, nil
}

func createPlayer(conn *sqlite.Conn, session Session) (int64, error) {
	playerID, err := zz.OnceCreatePlayer(conn, time.Now().UnixMilli())
	if err != nil {
		return 0, err
	}
	return playerID, insertSession(conn, playerID, session)
}

// SignUp attaches a new account to the browser's player, keeping its history,
// or to a new player without a live session. Either way it revokes the player's
// sessions and returns a fresh one, so a cookie planted before sign-up never
// becomes the account's session.
func (s *Service) SignUp(ctx context.Context, browserToken, username, password string) (*Account, Session, error) {
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
	var account *Account
	if err := s.db.Write(ctx, func(conn *sqlite.Conn) error {
		count, err := zz.OnceCountAccounts(conn)
		if err != nil {
			return err
		}
		if count == 0 {
			return ErrSetupRequired
		}
		account, err = createAccount(conn, browserToken, fresh, normalized, passwordHash, false)
		return err
	}); err != nil {
		return nil, Session{}, err
	}
	s.notifyAccounts()
	return account, fresh, nil
}

// createAccount runs inside Write. It attaches the account to the browser's
// player or, without a live session, to a new player, and makes fresh that
// player's only session.
func createAccount(conn *sqlite.Conn, browserToken string, fresh Session, username, passwordHash string, admin bool) (*Account, error) {
	taken, err := accountByUsername(conn, username)
	if err != nil {
		return nil, err
	}
	if taken != nil {
		return nil, ErrUsernameTaken
	}
	playerID, err := sessionPlayer(conn, browserToken)
	if err != nil {
		return nil, err
	}
	if playerID != 0 {
		existing, err := accountForPlayer(conn, playerID)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return nil, ErrAlreadySignedUp
		}
		if err := zz.OnceDeletePlayerSessions(conn, playerID); err != nil {
			return nil, err
		}
		if err := insertSession(conn, playerID, fresh); err != nil {
			return nil, err
		}
	} else if playerID, err = createPlayer(conn, fresh); err != nil {
		return nil, err
	}
	isAdmin := int64(0)
	if admin {
		isAdmin = 1
	}
	id, err := zz.OnceCreateAccount(conn, zz.CreateAccountParams{
		PlayerId: playerID, Username: username, PasswordHash: passwordHash,
		IsAdmin: isAdmin, CreatedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		return nil, err
	}
	return &Account{ID: id, Username: username, IsAdmin: admin}, nil
}

// Login verifies the password, then in one transaction moves the browser's
// anonymous games into the account, revokes the browser's session and issues a
// session for the account's player. Every credential failure is ErrInvalidCredentials.
func (s *Service) Login(ctx context.Context, browserToken, username, password string) (*Account, Session, error) {
	normalized, valid := normalizeUsername(username)
	if !valid || validatePassword(password) != nil {
		return nil, Session{}, ErrInvalidCredentials
	}
	var row *accountRow
	if err := s.db.Read(ctx, func(conn *sqlite.Conn) (err error) {
		row, err = accountByUsername(conn, normalized)
		return err
	}); err != nil {
		return nil, Session{}, fmt.Errorf("load credentials: %w", err)
	}
	hash := s.dummyHash
	if row != nil {
		hash = row.PasswordHash
	}
	// Hash even for unknown usernames so timing does not reveal which exist.
	matches, err := passwordMatches(password, hash)
	if err != nil {
		return nil, Session{}, err
	}
	if !matches || row == nil {
		return nil, Session{}, ErrInvalidCredentials
	}
	session, err := newSession()
	if err != nil {
		return nil, Session{}, err
	}
	var account *Account
	var merged func()
	if err := s.db.Write(ctx, func(conn *sqlite.Conn) error {
		current, err := accountByID(conn, row.ID)
		if err != nil {
			return err
		}
		// The account was removed or its password changed since verification.
		if current == nil || current.PasswordHash != row.PasswordHash {
			return ErrInvalidCredentials
		}
		if merged, err = s.signIn(conn, browserToken, current.PlayerID, session); err != nil {
			return err
		}
		account = current.account()
		return nil
	}); err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			return nil, Session{}, err
		}
		return nil, Session{}, fmt.Errorf("log in: %w", err)
	}
	merged()
	// Wake views so the browser's revoked session stops streaming.
	s.notifyAccounts()
	return account, session, nil
}

// signIn runs inside Write. It revokes the browser's session; when that session
// belonged to another, anonymous player it merges that player's games into
// playerID and retires it. Then it issues session for playerID. Call the
// returned function after commit to wake the views a merge changed.
func (s *Service) signIn(conn *sqlite.Conn, browserToken string, playerID int64, session Session) (func(), error) {
	merged := func() {}
	if err := zz.OnceDeleteExpiredSessions(conn, time.Now().UnixMilli()); err != nil {
		return nil, err
	}
	from, err := sessionPlayer(conn, browserToken)
	if err != nil {
		return nil, err
	}
	if from != 0 {
		if err := zz.OnceDeleteSession(conn, tokenHash(browserToken)); err != nil {
			return nil, err
		}
	}
	if from != 0 && from != playerID {
		owner, err := accountForPlayer(conn, from)
		if err != nil {
			return nil, err
		}
		if owner == nil {
			if s.merge != nil {
				committed, err := s.merge(conn, from, playerID)
				if err != nil {
					return nil, fmt.Errorf("merge games: %w", err)
				}
				if committed != nil {
					merged = committed
				}
			}
			if err := zz.OnceDeletePlayerSessions(conn, from); err != nil {
				return nil, err
			}
		}
	}
	return merged, insertSession(conn, playerID, session)
}

// Logout revokes the session; the caller clears the cookie.
func (s *Service) Logout(ctx context.Context, token string) error {
	if !validToken(token) {
		return nil
	}
	if err := s.db.Write(ctx, func(conn *sqlite.Conn) error {
		return zz.OnceDeleteSession(conn, tokenHash(token))
	}); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

func requireAdmin(conn *sqlite.Conn, accountID int64) error {
	row, err := accountByID(conn, accountID)
	if err != nil {
		return err
	}
	if row == nil || row.IsAdmin != 1 {
		return ErrAdminRequired
	}
	return nil
}
