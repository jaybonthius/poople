package identity_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"poople/store"
	"poople/web/services/identity"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

const fixturePassword = "correct horse battery"

func openStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	db, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func openIdentity(t *testing.T, db *store.Store, merge identity.MergePlayers) *identity.Service {
	t.Helper()
	users, err := identity.New(t.Context(), db, identity.Cookies{}, merge)
	if err != nil {
		t.Fatal(err)
	}
	return users
}

func scalar(t *testing.T, db *store.Store, query string, args ...any) int64 {
	t.Helper()
	var result int64
	if err := db.Read(t.Context(), func(conn *sqlite.Conn) error {
		return sqlitex.Execute(conn, query, &sqlitex.ExecOptions{Args: args, ResultFunc: func(stmt *sqlite.Stmt) error { result = stmt.ColumnInt64(0); return nil }})
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func execute(t *testing.T, db *store.Store, query string, args ...any) {
	t.Helper()
	if err := db.Write(t.Context(), func(conn *sqlite.Conn) error {
		return sqlitex.Execute(conn, query, &sqlitex.ExecOptions{Args: args})
	}); err != nil {
		t.Fatal(err)
	}
}

// createAdmin completes setup from a browser without a session.
func createAdmin(t *testing.T, users *identity.Service) (*identity.Account, identity.Session) {
	t.Helper()
	setup, token, err := identity.NewSetup(t.Context(), users)
	if err != nil || token == "" {
		t.Fatalf("setup token=%q err=%v", token, err)
	}
	admin, session, err := setup.CreateAdmin(t.Context(), token, "", "Admin", fixturePassword)
	if err != nil || session == nil {
		t.Fatalf("create admin: %v", err)
	}
	return admin, *session
}

// anonymousPlayer makes a first command through WithPlayer and EnsurePlayer, returning the cookie token.
func anonymousPlayer(t *testing.T, users *identity.Service) (int64, string) {
	t.Helper()
	var playerID int64
	handler := identity.WithPlayer(users)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		if playerID, err = users.EnsurePlayer(w, r); err != nil {
			t.Error(err)
		}
	}))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("POST", "/", nil))
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != identity.SessionCookie || playerID == 0 {
		t.Fatalf("first command cookies=%v player=%d", cookies, playerID)
	}
	return playerID, cookies[0].Value
}

func viewer(t *testing.T, users *identity.Service, token string) *identity.Viewer {
	t.Helper()
	v, err := users.ViewerForSession(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSetupIsGatedAndAtomic(t *testing.T) {
	t.Parallel()
	db := openStore(t, t.TempDir())
	users := openIdentity(t, db, nil)
	setup, token, err := identity.NewSetup(t.Context(), users)
	if err != nil || token == "" {
		t.Fatal("empty database did not issue a setup token")
	}
	if _, _, err := users.SignUp(t.Context(), "", "early", fixturePassword); !errors.Is(err, identity.ErrSetupRequired) {
		t.Fatalf("sign-up before setup: %v", err)
	}
	altered := token[:42] + "A"
	if token[42] == 'A' {
		altered = token[:42] + "E"
	}
	for _, bad := range []string{"", "short", altered} {
		if ok, err := setup.Valid(t.Context(), bad); ok || err != nil {
			t.Fatalf("setup accepted token %q: %v", bad, err)
		}
		if _, _, err := setup.CreateAdmin(t.Context(), bad, "", "admin", fixturePassword); !errors.Is(err, identity.ErrInvalidSetupToken) {
			t.Fatalf("bad setup token: %v", err)
		}
	}
	if _, _, err := setup.CreateAdmin(t.Context(), token, "", "admin", "short"); !errors.Is(err, identity.ErrPasswordInvalid) {
		t.Fatalf("short password: %v", err)
	}
	// The setup link belongs to an anonymous player who already played.
	playerID, browser := anonymousPlayer(t, users)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := range 8 {
		wg.Go(func() {
			_, _, err := setup.CreateAdmin(context.Background(), token, browser, "admin"+strconv.Itoa(i), fixturePassword)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if !errors.Is(err, identity.ErrInvalidSetupToken) && !errors.Is(err, identity.ErrSetupComplete) {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || scalar(t, db, "SELECT count(*) FROM accounts") != 1 || scalar(t, db, "SELECT count(*) FROM accounts WHERE is_admin = 1") != 1 {
		t.Fatal("concurrent setup did not create exactly one admin")
	}
	v := viewer(t, users, browser)
	if v.PlayerID != playerID || v.Account == nil || !v.Account.IsAdmin {
		t.Fatalf("setup did not attach the admin to the browser's player: %+v", v)
	}
	if ok, _ := setup.Valid(t.Context(), token); ok {
		t.Fatal("consumed setup token still valid")
	}
	if _, again, err := identity.NewSetup(t.Context(), users); err != nil || again != "" {
		t.Fatal("setup token issued after an account exists")
	}
}

func TestSignUpKeepsThePlayerAndRejectsDuplicateUsernames(t *testing.T) {
	t.Parallel()
	db := openStore(t, t.TempDir())
	users := openIdentity(t, db, nil)
	createAdmin(t, users)
	playerID, browser := anonymousPlayer(t, users)
	account, session, err := users.SignUp(t.Context(), browser, " Mira ", fixturePassword)
	if err != nil || session != nil {
		t.Fatalf("sign-up issued a new session or failed: %v", err)
	}
	if account.Username != "Mira" || account.IsAdmin {
		t.Fatalf("account=%+v", account)
	}
	if v := viewer(t, users, browser); v.PlayerID != playerID || v.Account == nil || v.Account.ID != account.ID {
		t.Fatalf("sign-up changed the player: %+v", v)
	}
	if _, _, err := users.SignUp(t.Context(), browser, "other", fixturePassword); !errors.Is(err, identity.ErrAlreadySignedUp) {
		t.Fatalf("second account for one player: %v", err)
	}
	for _, name := range []string{"mira", "MIRA", "admin"} {
		if _, _, err := users.SignUp(t.Context(), "", name, fixturePassword); !errors.Is(err, identity.ErrUsernameTaken) {
			t.Fatalf("duplicate username %q: %v", name, err)
		}
	}
	for _, name := range []string{"ab", "-dash", "has space", "émile", "abcdefghijklmnopqrstuvwxy"} {
		if _, _, err := users.SignUp(t.Context(), "", name, fixturePassword); !errors.Is(err, identity.ErrUsernameInvalid) {
			t.Fatalf("invalid username %q: %v", name, err)
		}
	}
	fresh, session, err := users.SignUp(t.Context(), "", "ollie", fixturePassword)
	if err != nil || session == nil {
		t.Fatalf("sign-up without a session: %v", err)
	}
	if v := viewer(t, users, session.Token); v.PlayerID == playerID || v.Account.ID != fresh.ID {
		t.Fatal("sign-up without a session did not create its own player")
	}
	if got := scalar(t, db, "SELECT count(*) FROM players"); got != 3 {
		t.Fatalf("players=%d", got)
	}
	if _, _, err := users.Login(t.Context(), "", "MIRA", fixturePassword); err != nil {
		t.Fatalf("case-insensitive log-in: %v", err)
	}
}

func TestLoginFailuresAreGeneric(t *testing.T) {
	t.Parallel()
	db := openStore(t, t.TempDir())
	users := openIdentity(t, db, nil)
	createAdmin(t, users)
	_, browser := anonymousPlayer(t, users)
	for _, tc := range []struct{ username, password string }{
		{"admin", "wrong password"}, {"nobody", fixturePassword}, {"x", fixturePassword}, {"admin", "short"},
	} {
		if _, _, err := users.Login(t.Context(), browser, tc.username, tc.password); err != identity.ErrInvalidCredentials {
			t.Fatalf("%q/%q: %v", tc.username, tc.password, err)
		}
	}
	if v := viewer(t, users, browser); v.Account != nil {
		t.Fatal("failed log-in changed the browser's session")
	}
}

func TestLoginMergesTheAnonymousPlayerInsideTheTransaction(t *testing.T) {
	t.Parallel()
	db := openStore(t, t.TempDir())
	type call struct{ from, to int64 }
	var calls []call
	var failure error
	users := openIdentity(t, db, func(conn *sqlite.Conn, from, to int64) error {
		calls = append(calls, call{from, to})
		// A write here must commit or roll back with the log-in.
		if err := sqlitex.Execute(conn, "INSERT INTO app_meta(key, value) VALUES ('merged-' || ?1, ?2)", &sqlitex.ExecOptions{Args: []any{from, to}}); err != nil {
			return err
		}
		return failure
	})
	_, adminSession := createAdmin(t, users)
	adminPlayer := viewer(t, users, adminSession.Token).PlayerID
	anonymous, browser := anonymousPlayer(t, users)

	failure = errors.New("merge failed")
	if _, _, err := users.Login(t.Context(), browser, "admin", fixturePassword); !errors.Is(err, failure) {
		t.Fatalf("hook failure: %v", err)
	}
	if scalar(t, db, "SELECT count(*) FROM app_meta WHERE key LIKE 'merged-%'") != 0 {
		t.Fatal("hook write survived a failed log-in")
	}
	if v := viewer(t, users, browser); v.PlayerID != anonymous || v.Account != nil {
		t.Fatal("failed log-in revoked the anonymous session")
	}
	if got := scalar(t, db, "SELECT count(*) FROM sessions WHERE player_id = ?1", adminPlayer); got != 1 {
		t.Fatalf("failed log-in issued a session: %d", got)
	}

	failure = nil
	calls = nil
	account, session, err := users.Login(t.Context(), browser, "admin", fixturePassword)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != (call{anonymous, adminPlayer}) {
		t.Fatalf("merge calls=%v, want from %d to %d", calls, anonymous, adminPlayer)
	}
	if scalar(t, db, "SELECT value FROM app_meta WHERE key = 'merged-' || ?1", anonymous) != adminPlayer {
		t.Fatal("hook write did not commit with the log-in")
	}
	if _, err := users.ViewerForSession(t.Context(), browser); !errors.Is(err, identity.ErrInvalidSession) {
		t.Fatal("anonymous session survived log-in")
	}
	if v := viewer(t, users, session.Token); v.PlayerID != adminPlayer || v.Account.ID != account.ID {
		t.Fatalf("log-in session=%+v", v)
	}

	// Logging in again from a signed-in or cookieless browser merges nothing.
	calls = nil
	if _, _, err := users.Login(t.Context(), session.Token, "admin", fixturePassword); err != nil {
		t.Fatal(err)
	}
	if _, _, err := users.Login(t.Context(), "", "admin", fixturePassword); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("merge called without an anonymous player: %v", calls)
	}
}

func TestLoginWithoutMergeHook(t *testing.T) {
	t.Parallel()
	db := openStore(t, t.TempDir())
	users := openIdentity(t, db, nil)
	createAdmin(t, users)
	_, browser := anonymousPlayer(t, users)
	if _, _, err := users.Login(t.Context(), browser, "admin", fixturePassword); err != nil {
		t.Fatalf("nil merge hook: %v", err)
	}
}

func TestSessionsSurviveReopenAndLogoutRevokes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	users := openIdentity(t, db, nil)
	admin, session := createAdmin(t, users)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openStore(t, dir)
	users = openIdentity(t, db, nil)
	if v := viewer(t, users, session.Token); v.Account == nil || v.Account.ID != admin.ID {
		t.Fatal("session lost across reopen")
	}
	if err := users.Logout(t.Context(), session.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := users.ViewerForSession(t.Context(), session.Token); !errors.Is(err, identity.ErrInvalidSession) {
		t.Fatal("logout did not revoke the session")
	}
}

func TestExpiredSessionIsIgnoredAndCleanedUp(t *testing.T) {
	t.Parallel()
	db := openStore(t, t.TempDir())
	users := openIdentity(t, db, nil)
	_, browser := anonymousPlayer(t, users)
	execute(t, db, "UPDATE sessions SET expires_at = ?1", 1)
	if _, err := users.ViewerForSession(t.Context(), browser); !errors.Is(err, identity.ErrInvalidSession) {
		t.Fatal("expired session accepted")
	}
	var seen *identity.Viewer
	handler := identity.WithPlayer(users)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = identity.ViewerFrom(r.Context())
	}))
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: identity.SessionCookie, Value: browser})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if seen != nil || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("expired cookie not ignored and cleared")
	}
	openIdentity(t, db, nil)
	if scalar(t, db, "SELECT count(*) FROM sessions") != 0 {
		t.Fatal("startup kept an expired session")
	}
}

func TestWithPlayerReadsWithoutWritingAndEnsurePlayerCreatesOnce(t *testing.T) {
	t.Parallel()
	// Closed by the test to prove a storage failure fails closed.
	db, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	users := openIdentity(t, db, nil)
	var ids []int64
	handler := identity.WithPlayer(users)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			id, err := users.EnsurePlayer(w, r)
			if err != nil {
				t.Error(err)
			}
			ids = append(ids, id)
		}
	}))
	send := func(method, token string) *http.Response {
		r := httptest.NewRequest(method, "/", nil)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: identity.SessionCookie, Value: token})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Result()
	}
	if got := send("GET", ""); len(got.Cookies()) != 0 || scalar(t, db, "SELECT count(*) FROM players") != 0 {
		t.Fatal("page view created a player")
	}
	first := send("POST", "")
	cookie := first.Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.Domain != "" || cookie.MaxAge != 365*24*60*60 {
		t.Fatalf("session cookie attributes=%+v", cookie)
	}
	if got := send("POST", cookie.Value); len(got.Cookies()) != 0 {
		t.Fatal("known player got a new cookie")
	}
	if len(ids) != 2 || ids[0] != ids[1] || scalar(t, db, "SELECT count(*) FROM players") != 1 {
		t.Fatalf("player ids=%v", ids)
	}
	w := httptest.NewRecorder()
	identity.Cookies{Secure: true}.Set(w, identity.Session{Token: cookie.Value})
	if !w.Result().Cookies()[0].Secure {
		t.Fatal("secure cookie flag ignored")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if got := send("GET", cookie.Value); got.StatusCode != http.StatusInternalServerError {
		t.Fatal("storage failure did not fail closed")
	}
}
