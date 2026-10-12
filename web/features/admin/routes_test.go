package admin_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"poople/app"
	"poople/ladder"
	"poople/web"
	"poople/web/services/game"
	"poople/web/services/identity"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

const password = "correct horse battery"

type fixture struct {
	backend *app.Backend
	handler http.Handler
	users   *identity.Service // a second service on the same storage, for direct setup steps
	admin   string            // session token of jay, the admin
	mira    string            // session token of mira, an ordinary account
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	backend, err := app.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { backend.Close() })
	handler, token, err := web.NewApp(t.Context(), backend, false, 1000)
	if err != nil {
		t.Fatal(err)
	}
	users, err := identity.New(t.Context(), backend.Database(), identity.Cookies{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{backend: backend, handler: handler, users: users}
	f.admin = f.session(t, f.postJSON("/setup", "", map[string]string{"setupToken": token, "username": "jay", "password": password}))
	f.mira = f.session(t, f.postJSON("/signup", "", map[string]string{"username": "mira", "password": password}))
	return f
}

func (f fixture) session(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == identity.SessionCookie && cookie.Value != "" {
			return cookie.Value
		}
	}
	t.Fatalf("no session cookie: %d %s", w.Code, w.Body)
	return ""
}

func (f fixture) viewer(t *testing.T, session string) *identity.Viewer {
	t.Helper()
	viewer, err := f.users.ViewerForSession(t.Context(), session)
	if err != nil {
		t.Fatal(err)
	}
	return viewer
}

func (f fixture) serve(r *http.Request, session string) *httptest.ResponseRecorder {
	if session != "" {
		r.AddCookie(&http.Cookie{Name: identity.SessionCookie, Value: session})
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func (f fixture) page(path, session string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Sec-Fetch-Dest", "document")
	return f.serve(r, session)
}

func (f fixture) postJSON(path, session string, signals map[string]string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(signals)
	r := httptest.NewRequest("POST", path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Datastar-Request", "true")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	return f.serve(r, session)
}

// postForm submits a plain HTML form, as the reset-link button does.
func (f fixture) postForm(path, session string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(""))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	return f.serve(r, session)
}

func accountPath(id int64, command string) string {
	return "/admin/users/" + itoa(id) + "/" + command
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestAdminPagesRefuseNonAdmins(t *testing.T) {
	f := newFixture(t)
	mira := f.viewer(t, f.mira).Account.ID
	if w := f.page("/admin/users", ""); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Fatalf("anonymous: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := f.page("/admin/users", f.mira); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "<!doctype html>") {
		t.Fatalf("non-admin page: %d", w.Code)
	}
	stream := httptest.NewRequest("GET", "/admin/users/state", nil)
	stream.Header.Set("Datastar-Request", "true")
	if w := f.serve(stream, f.mira); w.Code != http.StatusForbidden {
		t.Fatalf("non-admin stream: %d", w.Code)
	}
	admin := f.viewer(t, f.admin).Account.ID
	for _, w := range []*httptest.ResponseRecorder{
		f.postForm(accountPath(admin, "reset"), f.mira),
		f.postJSON(accountPath(admin, "avatar/remove"), f.mira, nil),
		f.postJSON(accountPath(admin, "delete"), f.mira, nil),
		f.postJSON(accountPath(mira, "delete"), f.mira, nil),
	} {
		if w.Code != http.StatusForbidden {
			t.Fatalf("non-admin command: %d %s", w.Code, w.Body)
		}
	}
	if f.viewer(t, f.admin).Account == nil || f.viewer(t, f.mira).Account == nil {
		t.Fatal("refused command changed accounts")
	}
	if w := f.page("/admin/users", f.admin); w.Code != 200 || !strings.Contains(w.Body.String(), `aria-label="Admin" aria-current="page"`) {
		t.Fatalf("admin page: %d", w.Code)
	}
}

func TestResetLinkIsShownOnceAndWorks(t *testing.T) {
	f := newFixture(t)
	mira := f.viewer(t, f.mira).Account.ID
	w := f.postForm(accountPath(mira, "reset"), f.admin)
	location, err := url.Parse(w.Header().Get("Location"))
	if w.Code != http.StatusSeeOther || err != nil || location.Path != "/admin/users" {
		t.Fatalf("issue: %d %q", w.Code, w.Header().Get("Location"))
	}
	token := location.Query().Get("reset")
	if location.Query().Get("account") != itoa(mira) || token == "" {
		t.Fatalf("issued redirect %q", location)
	}
	// Only the token's hash is stored.
	if err := f.backend.Database().Read(t.Context(), func(conn *sqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, "SELECT token_hash FROM password_resets", &sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				if strings.Contains(stmt.ColumnText(0), token) {
					t.Fatal("raw reset token stored")
				}
				return nil
			},
		})
	}); err != nil {
		t.Fatal(err)
	}

	link := "/reset?" + url.Values{"token": {token}}.Encode()
	page := f.page(location.String(), f.admin)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `value="`+strings.ReplaceAll(link, "&", "&amp;")+`"`) || !strings.Contains(page.Body.String(), "Reset link for mira") {
		t.Fatalf("issued page: %d", page.Code)
	}
	// Once drawn, the token leaves the address bar and history, so Back or a
	// reload cannot show it again, as the hint says.
	if !strings.Contains(page.Body.String(), "history.replaceState(history.state, '', '/admin/users')") || !strings.Contains(page.Body.String(), "it won't be shown again once you reload or leave this page") {
		t.Fatal("issued page leaves the reset token in the address bar")
	}
	if body := f.page("/admin/users", f.admin).Body.String(); strings.Contains(body, token) || strings.Contains(body, `id="reset-url"`) {
		t.Fatal("reset token shown without its URL")
	}
	// The link is shown only to the admin who issued it, for the account it belongs to.
	wrong := url.Values{"account": {itoa(f.viewer(t, f.admin).Account.ID)}, "reset": {token}}
	if body := f.page("/admin/users?"+wrong.Encode(), f.admin).Body.String(); strings.Contains(body, `id="reset-url"`) {
		t.Fatal("reset link shown for another account")
	}

	if w := f.page(link, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Choose a new password for mira.") {
		t.Fatalf("reset page: %d", w.Code)
	}
	const next = "a brand new password"
	w = f.postJSON("/reset", "", map[string]string{"resetToken": token, "password": next})
	if !strings.Contains(w.Body.String(), `window.location.href = "/"`) {
		t.Fatalf("use reset: %s", w.Body)
	}
	if f.page("/account", f.mira).Code != http.StatusSeeOther {
		t.Fatal("reset kept mira's old session")
	}
	if w := f.postJSON("/reset", "", map[string]string{"resetToken": token, "password": "another password!"}); !strings.Contains(w.Body.String(), "expired or was already used") {
		t.Fatalf("reset reused: %s", w.Body)
	}
	used := f.page(location.String(), f.admin).Body.String()
	if strings.Contains(used, `id="reset-url"`) || strings.Contains(used, `value="/reset?`) || !strings.Contains(used, "That reset link has been used or has expired.") {
		t.Fatal("used reset link still shown")
	}
	if w := f.postJSON("/login", "", map[string]string{"username": "mira", "password": next}); !strings.Contains(w.Body.String(), `window.location.href = "/"`) {
		t.Fatalf("new password: %s", w.Body)
	}
}

func TestAdminRemovesPicture(t *testing.T) {
	f := newFixture(t)
	mira := f.viewer(t, f.mira).Account.ID
	var picture bytes.Buffer
	png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 16, 16)))
	hash, err := f.users.SetAvatar(t.Context(), mira, &picture)
	if err != nil {
		t.Fatal(err)
	}
	if list := f.page("/admin/users", f.admin).Body.String(); !strings.Contains(list, hash) || !strings.Contains(list, `aria-label="Remove picture for mira"`) {
		t.Fatal("list does not show mira's picture with a removal button named for its visible label")
	}
	if w := f.postJSON(accountPath(mira, "avatar/remove"), f.admin, nil); w.Code != http.StatusNoContent {
		t.Fatalf("remove: %d %s", w.Code, w.Body)
	}
	if strings.Contains(f.page("/admin/users", f.admin).Body.String(), hash) || f.viewer(t, f.mira).Account.AvatarHash != "" {
		t.Fatal("picture survived removal")
	}
}

func TestDeletedAccountKeepsGamesWithoutName(t *testing.T) {
	f := newFixture(t)
	viewer := f.viewer(t, f.mira)
	admin := f.viewer(t, f.admin).Account.ID

	games := game.New(f.backend, time.Now)
	puzzle, err := games.DailyPuzzle(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	data, err := ladder.Load()
	if err != nil {
		t.Fatal(err)
	}
	if rejection, err := games.Guess(t.Context(), viewer.PlayerID, puzzle.ID, data.Neighbors(puzzle.StartWord)[0]); err != nil || rejection != "" {
		t.Fatalf("guess: %q %v", rejection, err)
	}
	list := f.page("/admin/users", f.admin).Body.String()
	if !strings.Contains(list, ">mira") || !strings.Contains(list, "1 game") {
		t.Fatal("list does not show mira's game")
	}

	if w := f.postJSON(accountPath(admin, "delete"), f.admin, nil); !strings.Contains(w.Body.String(), "You can't delete your own account.") {
		t.Fatalf("self-delete: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(f.page("/admin/users", f.admin).Body.String(), ">jay") {
		t.Fatal("admin deleted themselves")
	}

	if w := f.postForm(accountPath(viewer.Account.ID, "reset"), f.admin); w.Code != http.StatusSeeOther {
		t.Fatalf("issue reset: %d", w.Code)
	}
	if w := f.postJSON(accountPath(viewer.Account.ID, "delete"), f.admin, nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if strings.Contains(f.page("/admin/users", f.admin).Body.String(), "mira") {
		t.Fatal("deleted account still listed by name")
	}
	if _, err := f.users.ViewerForSession(t.Context(), f.mira); err != identity.ErrInvalidSession {
		t.Fatalf("deleted account's session survived: %v", err)
	}
	if w := f.postJSON("/login", "", map[string]string{"username": "mira", "password": password}); !strings.Contains(w.Body.String(), "Invalid username or password.") {
		t.Fatal("deleted account still logs in")
	}
	var gamesLeft, accountsLeft, resetsLeft int
	if err := f.backend.Database().Read(t.Context(), func(conn *sqlite.Conn) error {
		count := func(query string, into *int) error {
			return sqlitex.ExecuteTransient(conn, query, &sqlitex.ExecOptions{
				Args:       []any{viewer.PlayerID},
				ResultFunc: func(stmt *sqlite.Stmt) error { *into = stmt.ColumnInt(0); return nil },
			})
		}
		if err := count("SELECT count(*) FROM games WHERE player_id = ?", &gamesLeft); err != nil {
			return err
		}
		if err := count("SELECT count(*) FROM accounts WHERE player_id = ?", &accountsLeft); err != nil {
			return err
		}
		return count("SELECT count(*) FROM password_resets WHERE ? > 0", &resetsLeft)
	}); err != nil {
		t.Fatal(err)
	}
	if gamesLeft != 1 || accountsLeft != 0 || resetsLeft != 0 {
		t.Fatalf("after delete: games=%d accounts=%d resets=%d", gamesLeft, accountsLeft, resetsLeft)
	}
	if view, err := games.GameView(t.Context(), viewer.PlayerID, puzzle.ID); err != nil || view.Steps != 1 {
		t.Fatalf("anonymous game: %+v %v", view, err)
	}
}

// nextMain returns the next streamed <main>, or "" when the stream ends.
func nextMain(events *bufio.Reader) string {
	var event strings.Builder
	for {
		line, err := events.ReadString('\n')
		if err != nil {
			return ""
		}
		if line != "\n" {
			event.WriteString(line)
			continue
		}
		if strings.Contains(event.String(), `<main id="main"`) {
			return event.String()
		}
		event.Reset()
	}
}

func TestUserListFollowsAccountsAndGames(t *testing.T) {
	f := newFixture(t)
	server := httptest.NewServer(f.handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel() // runs before server.Close, ending the stream
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/admin/users/state", nil)
	request.Header.Set("Datastar-Request", "true")
	request.AddCookie(&http.Cookie{Name: identity.SessionCookie, Value: f.admin})
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	events := bufio.NewReader(response.Body)
	if view := nextMain(events); !strings.Contains(view, ">mira") || !strings.Contains(view, "0 games") {
		t.Fatalf("first view: %q", view)
	}

	f.session(t, f.postJSON("/signup", "", map[string]string{"username": "ollie", "password": password}))
	if view := nextMain(events); !strings.Contains(view, ">ollie") {
		t.Fatalf("after sign-up: %q", view)
	}

	games := game.New(f.backend, time.Now)
	puzzle, err := games.DailyPuzzle(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	data, err := ladder.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := games.Guess(t.Context(), f.viewer(t, f.mira).PlayerID, puzzle.ID, data.Neighbors(puzzle.StartWord)[0]); err != nil {
		t.Fatal(err)
	}
	if view := nextMain(events); !strings.Contains(view, "1 game") {
		t.Fatalf("after mira's guess: %q", view)
	}
}
