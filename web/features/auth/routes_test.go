package auth_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"poople/app"
	"poople/web"
	"poople/web/services/identity"
)

const password = "correct horse battery"

type fixture struct {
	handler    http.Handler
	setupToken string
	users      *identity.Service // a second service on the same storage, for admin-only steps
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	backend, err := app.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { backend.Close() })
	handler, token, err := web.NewApp(t.Context(), backend, false, 30)
	if err != nil || token == "" {
		t.Fatalf("new app token=%q err=%v", token, err)
	}
	users, err := identity.New(t.Context(), backend.Database(), identity.Cookies{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{handler, token, users}
}

func (f fixture) get(path, session string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Sec-Fetch-Dest", "document")
	if session != "" {
		r.AddCookie(&http.Cookie{Name: identity.SessionCookie, Value: session})
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func (f fixture) post(path, session string, signals map[string]string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(signals)
	r := httptest.NewRequest("POST", path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Datastar-Request", "true")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if session != "" {
		r.AddCookie(&http.Cookie{Name: identity.SessionCookie, Value: session})
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func sessionCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == identity.SessionCookie {
			return cookie
		}
	}
	return nil
}

func redirected(w *httptest.ResponseRecorder, path string) bool {
	return w.Code == 200 && strings.Contains(w.Body.String(), "window.location.href = "+strconv.Quote(path))
}

func refused(w *httptest.ResponseRecorder, message string) bool {
	body := w.Body.String()
	return w.Code == 200 && strings.Contains(body, "datastar-patch-signals") && strings.Contains(body, message) &&
		strings.Contains(body, `"password":""`) && !strings.Contains(body, "window.location")
}

func TestSetupSignUpLogInAndLogOut(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{"/setup", "/setup?token=wrong"} {
		if w := f.get(path, ""); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "<!doctype html>") {
			t.Fatalf("%s status=%d", path, w.Code)
		}
	}
	setupPath := "/setup?" + url.Values{"token": {f.setupToken}}.Encode()
	if w := f.get(setupPath, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `name="setupToken" value="`+f.setupToken+`"`) || !strings.Contains(w.Body.String(), `class="form-surface"`) {
		t.Fatalf("setup page status=%d", w.Code)
	}
	if w := f.post("/signup", "", map[string]string{"username": "early", "password": password}); !refused(w, "Sign-up opens once the admin has set up Poople.") {
		t.Fatalf("sign-up before setup: %s", w.Body)
	}
	w := f.post("/setup", "", map[string]string{"setupToken": f.setupToken, "username": "Admin", "password": password})
	admin := sessionCookie(t, w)
	if !redirected(w, "/") || admin == nil || !admin.HttpOnly || admin.SameSite != http.SameSiteLaxMode {
		t.Fatalf("setup: %d %s", w.Code, w.Body)
	}
	if w := f.get(setupPath, ""); w.Code != http.StatusNotFound {
		t.Fatal("setup page available after setup")
	}
	if w := f.post("/setup", "", map[string]string{"setupToken": f.setupToken, "username": "second", "password": password}); !refused(w, "Setup link is unavailable") {
		t.Fatalf("setup reused: %s", w.Body)
	}

	page := f.get("/signup", "")
	for _, want := range []string{`<main id="main" class="page">`, `class="form-panel"`, `<a href="/signup" aria-current="page">Sign up</a>`, `autocomplete="new-password"`, `minlength="10"`, "At least 10 characters."} {
		if page.Code != 200 || !strings.Contains(page.Body.String(), want) {
			t.Fatalf("sign-up page missing %q", want)
		}
	}
	w = f.post("/signup", "", map[string]string{"username": "Mira", "password": password})
	mira := sessionCookie(t, w)
	if !redirected(w, "/") || mira == nil {
		t.Fatalf("sign-up: %s", w.Body)
	}
	if w := f.post("/signup", "", map[string]string{"username": "MIRA", "password": password}); !refused(w, "That username is taken. Try another.") || sessionCookie(t, w) != nil {
		t.Fatalf("duplicate sign-up: %s", w.Body)
	}
	if w := f.post("/signup", "", map[string]string{"username": "ollie", "password": "short"}); !refused(w, "Password must be 10–128 characters.") {
		t.Fatalf("short password: %s", w.Body)
	}
	if w := f.get("/signup", mira.Value); w.Code != http.StatusSeeOther {
		t.Fatal("signed-in player offered sign-up")
	}

	for _, attempt := range []map[string]string{{"username": "mira", "password": "wrong password"}, {"username": "nobody", "password": password}} {
		if w := f.post("/login", "", attempt); !refused(w, "Invalid username or password.") || sessionCookie(t, w) != nil {
			t.Fatalf("bad log-in %v: %s", attempt, w.Body)
		}
	}
	w = f.post("/login", "", map[string]string{"username": "mira", "password": password})
	login := sessionCookie(t, w)
	if !redirected(w, "/") || login == nil || login.Value == mira.Value {
		t.Fatalf("log-in: %s", w.Body)
	}
	if w := f.get("/login", login.Value); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatal("signed-in player offered log-in")
	}
	w = f.post("/logout", login.Value, map[string]string{})
	if cleared := sessionCookie(t, w); !redirected(w, "/") || cleared == nil || cleared.MaxAge != -1 {
		t.Fatalf("log-out: %s", w.Body)
	}
	if w := f.get("/login", login.Value); w.Code != 200 || sessionCookie(t, w) == nil {
		t.Fatal("logged-out session still signed in")
	}
}

func TestLoginIsRateLimitedPerUsername(t *testing.T) {
	f := newFixture(t)
	for range 5 {
		if w := f.post("/login", "", map[string]string{"username": "mira", "password": "wrong password"}); !refused(w, "Invalid username or password.") {
			t.Fatal(w.Body.String())
		}
	}
	if w := f.post("/login", "", map[string]string{"username": "MIRA", "password": password}); !refused(w, "Too many attempts.") {
		t.Fatalf("sixth attempt: %s", w.Body)
	}
	if w := f.post("/login", "", map[string]string{"username": "other", "password": password}); !refused(w, "Invalid username or password.") {
		t.Fatalf("other username limited: %s", w.Body)
	}
}

func TestAccountCommandsRefuseCrossOriginAndUnboundedRequests(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{"/login", "/signup", "/setup", "/reset", "/logout"} {
		r := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.Header.Set("Origin", "https://untrusted.invalid")
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("cross-origin %s status=%d", path, w.Code)
		}
	}
	form := httptest.NewRequest("POST", "/login", strings.NewReader("username=mira&password=x"))
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, form)
	if !refused(w, "Invalid request. Try again.") {
		t.Fatalf("form-encoded log-in: %s", w.Body)
	}
	huge := f.post("/signup", "", map[string]string{"username": "mira", "password": strings.Repeat("a", 20<<10)})
	if !refused(huge, "Invalid request. Try again.") {
		t.Fatalf("oversized body: %s", huge.Body)
	}
}

func TestResetLinkSetsPasswordAndSignsIn(t *testing.T) {
	f := newFixture(t)
	f.post("/setup", "", map[string]string{"setupToken": f.setupToken, "username": "admin", "password": password})
	f.post("/signup", "", map[string]string{"username": "mira", "password": password})
	adminAccount, _, err := f.users.Login(t.Context(), "", "admin", password)
	if err != nil {
		t.Fatal(err)
	}
	miraAccount, _, err := f.users.Login(t.Context(), "", "mira", password)
	if err != nil {
		t.Fatal(err)
	}
	token, err := f.users.IssuePasswordReset(t.Context(), adminAccount.ID, miraAccount.ID)
	if err != nil {
		t.Fatal(err)
	}
	if w := f.get("/reset?token=bad", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Reset link unavailable") || strings.Contains(w.Body.String(), "resetToken") {
		t.Fatal("bad reset link offered a form")
	}
	resetPath := "/reset?" + url.Values{"token": {token}}.Encode()
	if w := f.get(resetPath, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Choose a new password for mira.") {
		t.Fatal("reset page missing")
	}
	w := f.post("/reset", "", map[string]string{"resetToken": token, "password": "a brand new password"})
	if !redirected(w, "/") || sessionCookie(t, w) == nil {
		t.Fatalf("reset: %s", w.Body)
	}
	if w := f.post("/reset", "", map[string]string{"resetToken": token, "password": "another new password"}); !refused(w, "This reset link has expired or was already used.") {
		t.Fatalf("reset reused: %s", w.Body)
	}
	if w := f.post("/login", "", map[string]string{"username": "mira", "password": "a brand new password"}); !redirected(w, "/") {
		t.Fatal("new password rejected")
	}
}

func TestAvatarIsServedByHashWithLongCache(t *testing.T) {
	f := newFixture(t)
	f.post("/setup", "", map[string]string{"setupToken": f.setupToken, "username": "admin", "password": password})
	admin, _, err := f.users.Login(t.Context(), "", "admin", password)
	if err != nil {
		t.Fatal(err)
	}
	var upload bytes.Buffer
	if err := png.Encode(&upload, image.NewRGBA(image.Rect(0, 0, 40, 30))); err != nil {
		t.Fatal(err)
	}
	hash, err := f.users.SetAvatar(t.Context(), admin.ID, &upload)
	if err != nil {
		t.Fatal(err)
	}
	path := (&identity.Account{ID: admin.ID, AvatarHash: hash}).AvatarPath()
	w := f.get(path, "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("avatar status=%d headers=%v", w.Code, w.Header())
	}
	if _, err := png.Decode(w.Body); err != nil {
		t.Fatal(err)
	}
	for _, missing := range []string{
		"/avatars/" + strconv.FormatInt(admin.ID, 10) + "/" + strings.Repeat("0", 64) + ".png",
		"/avatars/999/" + hash + ".png",
		"/avatars/x/" + hash + ".png",
	} {
		if w := f.get(missing, ""); w.Code != http.StatusNotFound || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s status=%d cache=%q", missing, w.Code, w.Header().Get("Cache-Control"))
		}
	}
}
