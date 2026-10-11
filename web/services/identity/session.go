package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"poople/store/zz"

	"zombiezen.com/go/sqlite"
)

// sessionDuration applies to every session, anonymous or signed in: one cookie for everyone.
const sessionDuration = 365 * 24 * time.Hour

// Session is a newly issued cookie value. Only its hash is stored.
type Session struct {
	Token     string
	ExpiresAt time.Time
}

func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func validToken(token string) bool {
	if len(token) != 43 {
		return false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	return err == nil && len(raw) == 32 && base64.RawURLEncoding.EncodeToString(raw) == token
}

func tokenHash(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func newSession() (Session, error) {
	token, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	return Session{Token: token, ExpiresAt: time.Now().UTC().Truncate(time.Second).Add(sessionDuration)}, nil
}

func insertSession(conn *sqlite.Conn, playerID int64, session Session) error {
	return zz.OnceCreateSession(conn, zz.CreateSessionParams{
		TokenHash: tokenHash(session.Token), PlayerId: playerID,
		CreatedAt: time.Now().UnixMilli(), ExpiresAt: session.ExpiresAt.UnixMilli(),
	})
}

// sessionPlayer returns the player behind a live session token, or 0.
func sessionPlayer(conn *sqlite.Conn, token string) (int64, error) {
	if !validToken(token) {
		return 0, nil
	}
	return zz.OnceGetSessionPlayer(conn, zz.GetSessionPlayerParams{TokenHash: tokenHash(token), Now: time.Now().UnixMilli()})
}
