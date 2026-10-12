package identity_test

import (
	"errors"
	"testing"

	"poople/web/services/identity"

	"zombiezen.com/go/sqlite"
)

func TestPasswordResetLinkIsAdminOnlySingleUseAndExpiring(t *testing.T) {
	t.Parallel()
	db := openStore(t, t.TempDir())
	merged, committed := 0, 0
	users := openIdentity(t, db, func(*sqlite.Conn, int64, int64) (func(), error) { merged++; return func() { committed++ }, nil })
	admin, _ := createAdmin(t, users)
	mira, miraSession, err := users.SignUp(t.Context(), "", "mira", fixturePassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := users.IssuePasswordReset(t.Context(), mira.ID, admin.ID); !errors.Is(err, identity.ErrAdminRequired) {
		t.Fatalf("non-admin issued a reset: %v", err)
	}
	if _, err := users.IssuePasswordReset(t.Context(), admin.ID, 999); !errors.Is(err, identity.ErrAccountNotFound) {
		t.Fatalf("reset for a missing account: %v", err)
	}
	token, err := users.IssuePasswordReset(t.Context(), admin.ID, mira.ID)
	if err != nil {
		t.Fatal(err)
	}
	if scalar(t, db, "SELECT count(*) FROM password_resets WHERE token_hash = ?1", token) != 0 {
		t.Fatal("raw reset token stored")
	}
	if name, err := users.PasswordResetFor(t.Context(), token); err != nil || name != "mira" {
		t.Fatalf("reset lookup=%q %v", name, err)
	}
	if _, _, err := users.ResetPassword(t.Context(), token, "", "short"); !errors.Is(err, identity.ErrPasswordInvalid) {
		t.Fatalf("short reset password: %v", err)
	}
	// The link is used from a browser that played anonymously.
	anonymous, browser := anonymousPlayer(t, users)
	const newPassword = "a brand new password"
	account, session, err := users.ResetPassword(t.Context(), token, browser, newPassword)
	if err != nil || account.ID != mira.ID {
		t.Fatalf("reset: %v", err)
	}
	if v := viewer(t, users, session.Token); v.Account == nil || v.Account.ID != mira.ID || v.PlayerID == anonymous {
		t.Fatal("reset did not sign the browser in")
	}
	if merged != 1 || committed != 1 {
		t.Fatalf("reset merged %d anonymous players and ran %d commit hooks", merged, committed)
	}
	for _, old := range []string{miraSession.Token, browser} {
		if _, err := users.ViewerForSession(t.Context(), old); !errors.Is(err, identity.ErrInvalidSession) {
			t.Fatal("reset left an old session valid")
		}
	}
	if _, _, err := users.Login(t.Context(), "", "mira", fixturePassword); !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Fatal("old password still works")
	}
	if _, _, err := users.Login(t.Context(), "", "mira", newPassword); err != nil {
		t.Fatalf("new password: %v", err)
	}
	if _, _, err := users.ResetPassword(t.Context(), token, "", "another new password"); !errors.Is(err, identity.ErrInvalidReset) {
		t.Fatalf("reset link reused: %v", err)
	}
	if _, err := users.PasswordResetFor(t.Context(), token); !errors.Is(err, identity.ErrInvalidReset) {
		t.Fatal("used link still offered")
	}

	expiring, err := users.IssuePasswordReset(t.Context(), admin.ID, mira.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, db, "SELECT max(expires_at - created_at) FROM password_resets"); got != 24*60*60*1000 {
		t.Fatalf("reset lifetime=%dms", got)
	}
	execute(t, db, "UPDATE password_resets SET expires_at = 1 WHERE used_at IS NULL")
	if _, err := users.PasswordResetFor(t.Context(), expiring); !errors.Is(err, identity.ErrInvalidReset) {
		t.Fatal("expired link offered")
	}
	if _, _, err := users.ResetPassword(t.Context(), expiring, "", "another new password"); !errors.Is(err, identity.ErrInvalidReset) {
		t.Fatal("expired link used")
	}
	for _, bad := range []string{"", "not-a-token"} {
		if _, err := users.PasswordResetFor(t.Context(), bad); !errors.Is(err, identity.ErrInvalidReset) {
			t.Fatalf("malformed token %q accepted", bad)
		}
	}
}
