package account_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"poople/app"
	"poople/ladder"
	"poople/web"
	"poople/web/services/identity"
)

const password = "correct horse battery"

type fixture struct {
	handler http.Handler
	admin   string // session token of the admin, jay
	mira    string // session token of mira, an ordinary account
}

// newFixture sets up the admin and signs mira up through HTTP.
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
	f := fixture{handler: handler}
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

// upload posts parts the way Datastar posts a multipart form.
func (f fixture) upload(t *testing.T, session string, parts map[string][]byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, data := range parts {
		part, err := writer.CreateFormFile(name, "picture.bin")
		if err != nil {
			t.Fatal(err)
		}
		part.Write(data)
	}
	writer.Close()
	r := httptest.NewRequest("POST", "/account/avatar", &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("Datastar-Request", "true")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	return f.serve(r, session)
}

func pngImage(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := range width {
		img.Set(x, height/2, color.RGBA{122, 71, 38, 255})
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func signalPatch(w *httptest.ResponseRecorder, message string) bool {
	body := w.Body.String()
	return w.Code == 200 && strings.Contains(body, "datastar-patch-signals") && strings.Contains(body, message)
}

func loginRedirect(w *httptest.ResponseRecorder) bool {
	return w.Code == 200 && strings.Contains(w.Body.String(), `window.location.href = "/login"`)
}

func TestAccountRequiresSignedInAccount(t *testing.T) {
	f := newFixture(t)
	if w := f.page("/account", ""); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Fatalf("anonymous page: %d %q", w.Code, w.Header().Get("Location"))
	}
	stream := httptest.NewRequest("GET", "/account/state", nil)
	stream.Header.Set("Datastar-Request", "true")
	if w := f.serve(stream, ""); !loginRedirect(w) {
		t.Fatalf("anonymous stream: %d %s", w.Code, w.Body)
	}
	if w := f.upload(t, "", map[string][]byte{"avatar": pngImage(t, 8, 8)}); !loginRedirect(w) {
		t.Fatalf("anonymous upload: %d %s", w.Code, w.Body)
	}
	if w := f.postJSON("/account/password", "", map[string]string{"currentPassword": password, "newPassword": "a new password here"}); !loginRedirect(w) {
		t.Fatalf("anonymous password change: %d %s", w.Code, w.Body)
	}
	w := f.page("/account", f.mira)
	for _, want := range []string{`<main id="main" class="page"`, `<h1 class="page-title">mira</h1>`, `enctype="multipart/form-data"`, `autocomplete="current-password"`, `aria-label="Your account, mira" aria-current="page"`, "/account/static/styles/account"} {
		if w.Code != 200 || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("account page missing %q: %d", want, w.Code)
		}
	}
	if strings.Contains(w.Body.String(), "/admin/users") {
		t.Fatal("non-admin masthead links to admin")
	}
}

func TestAvatarUploadThroughHTTP(t *testing.T) {
	f := newFixture(t)
	if w := f.upload(t, f.mira, map[string][]byte{"avatar": pngImage(t, 400, 300)}); w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	page := f.page("/account", f.mira).Body.String()
	start := strings.Index(page, `src="/avatars/`)
	if start < 0 {
		t.Fatal("account page does not show the uploaded picture")
	}
	path := page[start+len(`src="`):]
	path = path[:strings.IndexByte(path, '"')]
	picture := f.page(path, "")
	if picture.Code != 200 || picture.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("picture %s: %d", path, picture.Code)
	}
	if config, err := png.DecodeConfig(picture.Body); err != nil || config.Width != identity.AvatarSize || config.Height != identity.AvatarSize {
		t.Fatalf("stored picture %+v %v", config, err)
	}

	for name, attempt := range map[string]struct {
		parts   map[string][]byte
		message string
	}{
		"too large":     {map[string][]byte{"avatar": bytes.Repeat([]byte{0}, identity.AvatarMaxBytes+1)}, "Picture must be 2 MB or smaller."},
		"beyond limit":  {map[string][]byte{"avatar": bytes.Repeat([]byte{0}, identity.AvatarMaxBytes+512<<10)}, "Picture must be 2 MB or smaller."},
		"not an image":  {map[string][]byte{"avatar": []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)}, "Picture must be a PNG, JPEG, GIF or WebP image."},
		"no file":       {map[string][]byte{}, "Choose a picture to upload."},
		"unknown field": {map[string][]byte{"other": pngImage(t, 8, 8)}, "Choose a picture to upload."},
	} {
		if w := f.upload(t, f.mira, attempt.parts); !signalPatch(w, attempt.message) {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	// Refusals leave the stored picture in place.
	if f.page(path, "").Code != 200 {
		t.Fatal("refused upload replaced the picture")
	}

	if w := f.postJSON("/account/avatar/remove", f.mira, map[string]string{}); w.Code != http.StatusNoContent {
		t.Fatalf("remove: %d %s", w.Code, w.Body)
	}
	if strings.Contains(f.page("/account", f.mira).Body.String(), `src="/avatars/`) || f.page(path, "").Code != http.StatusNotFound {
		t.Fatal("picture still shown after removal")
	}
}

// readEvent returns the next SSE event, or "" when the stream ends or fails.
func readEvent(events *bufio.Reader) string {
	var event strings.Builder
	for {
		line, err := events.ReadString('\n')
		if err != nil {
			return event.String()
		}
		if line == "\n" {
			if event.Len() > 0 {
				return event.String()
			}
			continue
		}
		event.WriteString(line)
	}
}

func TestPasswordChangeRevokesOtherSessions(t *testing.T) {
	f := newFixture(t)
	other := f.session(t, f.postJSON("/login", "", map[string]string{"username": "mira", "password": password}))

	// The other device has the account page open.
	server := httptest.NewServer(f.handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel() // runs before server.Close, ending the stream
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/account/state", nil)
	request.Header.Set("Datastar-Request", "true")
	request.AddCookie(&http.Cookie{Name: identity.SessionCookie, Value: other})
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	events := bufio.NewReader(response.Body)
	for event := readEvent(events); !strings.Contains(event, `<main id="main"`); event = readEvent(events) {
		if event == "" {
			t.Fatal("stream ended before rendering")
		}
	}

	const next = "a brand new password"
	if w := f.postJSON("/account/password", f.mira, map[string]string{"currentPassword": "not my password", "newPassword": next}); !signalPatch(w, "Current password is incorrect.") {
		t.Fatalf("wrong current password: %s", w.Body)
	}
	if w := f.postJSON("/account/password", f.mira, map[string]string{"currentPassword": password, "newPassword": "short"}); !signalPatch(w, "New password must be 10–128 characters.") {
		t.Fatalf("short new password: %s", w.Body)
	}
	if w := f.page("/account", other); w.Code != 200 {
		t.Fatal("refused changes revoked the other session")
	}
	if w := f.postJSON("/account/password", f.mira, map[string]string{"currentPassword": password, "newPassword": next}); !signalPatch(w, "Password changed.") || !strings.Contains(w.Body.String(), `"_currentPassword":""`) {
		t.Fatalf("change password: %s", w.Body)
	}

	// The open stream ends instead of rendering for the revoked session.
	ended := make(chan string, 1)
	go func() {
		var rest strings.Builder
		for event := readEvent(events); event != ""; event = readEvent(events) {
			rest.WriteString(event)
		}
		ended <- rest.String()
	}()
	select {
	case rest := <-ended:
		if strings.Contains(rest, `<main id="main"`) {
			t.Fatal("revoked stream rendered the account again")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("revoked session's stream stayed open")
	}

	if w := f.page("/account", other); w.Code != http.StatusSeeOther {
		t.Fatalf("other session survived: %d", w.Code)
	}
	if w := f.page("/account", f.mira); w.Code != 200 {
		t.Fatalf("changing session was revoked: %d", w.Code)
	}
	if w := f.postJSON("/login", "", map[string]string{"username": "mira", "password": password}); !signalPatch(w, "Invalid username or password.") {
		t.Fatal("old password still logs in")
	}
	if w := f.postJSON("/login", "", map[string]string{"username": "mira", "password": next}); !strings.Contains(w.Body.String(), `window.location.href = "/"`) {
		t.Fatalf("new password does not log in: %s", w.Body)
	}
}

// The account stream also patches the masthead's account link, so a new or
// removed picture shows there without a reload; the picker's label follows it.
func TestAccountStreamPatchesMastheadPicture(t *testing.T) {
	f := newFixture(t)
	if page := f.page("/account", f.mira).Body.String(); !strings.Contains(page, `<label class="button" for="avatar-file">Choose picture</label>`) || !strings.Contains(page, `class="avatar avatar--empty`) {
		t.Fatal("account without a picture must offer to choose one")
	}
	server := httptest.NewServer(f.handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/account/state", nil)
	request.Header.Set("Datastar-Request", "true")
	request.AddCookie(&http.Cookie{Name: identity.SessionCookie, Value: f.mira})
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	events := bufio.NewReader(response.Body)
	nextView := func() string {
		for event := readEvent(events); event != ""; event = readEvent(events) {
			if strings.Contains(event, `<main id="main"`) {
				return event
			}
		}
		t.Fatal("stream ended before rendering")
		return ""
	}
	if first := nextView(); !strings.Contains(first, `<a id="masthead-account" class="nav-link" href="/account"`) || strings.Contains(first, `src="/avatars/`) {
		t.Fatalf("first view has no pictureless masthead link: %s", first)
	}
	if w := f.upload(t, f.mira, map[string][]byte{"avatar": pngImage(t, 64, 64)}); w.Code != http.StatusNoContent {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	view := nextView()
	masthead := view[strings.Index(view, `<a id="masthead-account"`):strings.Index(view, `<main id="main"`)]
	if !strings.Contains(masthead, `<img class="avatar" src="/avatars/`) {
		t.Fatalf("masthead does not show the new picture: %s", masthead)
	}
	if !strings.Contains(view, `<label class="button" for="avatar-file">Change picture</label>`) {
		t.Fatal("picker still offers to choose a first picture")
	}
}

func TestPasswordChangeIsRateLimited(t *testing.T) {
	f := newFixture(t)
	var last *httptest.ResponseRecorder
	for range 6 {
		last = f.postJSON("/account/password", f.mira, map[string]string{"currentPassword": "not my password", "newPassword": "a brand new password"})
	}
	if !signalPatch(last, "Too many attempts.") {
		t.Fatalf("sixth attempt: %s", last.Body)
	}
}

// rename posts a username change from client, so each test controls which
// requests share the per-client rate limit.
func (f fixture) rename(session, username, client string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"username": username})
	r := httptest.NewRequest("POST", "/account/username", bytes.NewReader(body))
	r.RemoteAddr = client
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Datastar-Request", "true")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	return f.serve(r, session)
}

func loggedIn(w *httptest.ResponseRecorder) bool {
	return strings.Contains(w.Body.String(), `window.location.href = "/"`)
}

func TestUsernameChangeThroughHTTP(t *testing.T) {
	f := newFixture(t)
	if page := f.page("/account", f.mira).Body.String(); !strings.Contains(page, `id="account-username" value="mira"`) || !strings.Contains(page, `action="/account/username"`) {
		t.Fatal("account page does not offer the current username")
	}
	w := f.rename(f.mira, " Mirabel ", "192.0.2.1:1000")
	if !signalPatch(w, "Username saved.") || !strings.Contains(w.Body.String(), `"_username":"Mirabel"`) || strings.Contains(w.Body.String(), "<main") {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	if page := f.page("/account", f.mira).Body.String(); !strings.Contains(page, `<h1 class="page-title">Mirabel</h1>`) || !strings.Contains(page, `aria-label="Your account, Mirabel"`) {
		t.Fatal("account page still shows the old name")
	}
	if w := f.postJSON("/login", "", map[string]string{"username": "mirabel", "password": password}); !loggedIn(w) {
		t.Fatalf("new name does not log in: %s", w.Body)
	}
	if w := f.postJSON("/login", "", map[string]string{"username": "mira", "password": password}); !signalPatch(w, "Invalid username or password.") {
		t.Fatalf("old name still logs in: %s", w.Body)
	}

	// Taken names are refused in any case, against the field, changing nothing.
	for i, name := range []string{"jay", "JAY"} {
		w := f.rename(f.mira, name, "192.0.2.2:"+strconv.Itoa(1000+i))
		if !signalPatch(w, "That username is taken. Try another.") || !strings.Contains(w.Body.String(), `"_usernameField":"username"`) {
			t.Fatalf("taken %q: %s", name, w.Body)
		}
	}
	if !strings.Contains(f.page("/account", f.mira).Body.String(), `<h1 class="page-title">Mirabel</h1>`) || !strings.Contains(f.page("/account", f.admin).Body.String(), `<h1 class="page-title">jay</h1>`) {
		t.Fatal("refused rename changed a username")
	}

	for i, name := range []string{"ab", "has space", "-dash", strings.Repeat("a", 25)} {
		if w := f.rename(f.mira, name, "192.0.2.3:"+strconv.Itoa(1000+i)); !signalPatch(w, "Username must be 3–24 letters") || !strings.Contains(w.Body.String(), `"_usernameField":"username"`) {
			t.Fatalf("invalid %q: %s", name, w.Body)
		}
	}

	if w := f.rename(f.mira, "MIRABEL", "192.0.2.4:1000"); !signalPatch(w, "Username saved.") {
		t.Fatalf("case-only change of own name: %s", w.Body)
	}
	if !strings.Contains(f.page("/account", f.mira).Body.String(), `<h1 class="page-title">MIRABEL</h1>`) {
		t.Fatal("case-only change not stored")
	}
	// The old name is free for someone else.
	if w := f.postJSON("/signup", "", map[string]string{"username": "mira", "password": password}); !loggedIn(w) {
		t.Fatalf("old name not freed: %s", w.Body)
	}
}

func TestUsernameChangeRefusesAnonymousAndCrossOriginRequests(t *testing.T) {
	f := newFixture(t)
	if w := f.rename("", "intruder", "192.0.2.1:1000"); !loginRedirect(w) {
		t.Fatalf("no session: %d %s", w.Code, w.Body)
	}
	// A player who has played but has no account is refused too.
	data, err := ladder.Load()
	if err != nil {
		t.Fatal(err)
	}
	path, _ := data.ShortestPath(data.DailyStart(ladder.DailyNumber(time.Now())))
	anonymous := f.session(t, f.postJSON("/play/guess", "", map[string]string{"guess": path[1]}))
	if w := f.rename(anonymous, "intruder", "192.0.2.1:1001"); !loginRedirect(w) {
		t.Fatalf("anonymous player: %d %s", w.Code, w.Body)
	}

	body, _ := json.Marshal(map[string]string{"username": "intruder"})
	r := httptest.NewRequest("POST", "/account/username", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set("Origin", "https://untrusted.invalid")
	if w := f.serve(r, f.mira); w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin rename status=%d", w.Code)
	}
	form := httptest.NewRequest("POST", "/account/username", strings.NewReader("username=intruder"))
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if w := f.serve(form, f.mira); !signalPatch(w, "Invalid request. Try again.") {
		t.Fatalf("form-encoded rename: %s", w.Body)
	}
	if !strings.Contains(f.page("/account", f.mira).Body.String(), `<h1 class="page-title">mira</h1>`) {
		t.Fatal("a refused request renamed the account")
	}
}

func TestUsernameChangeIsRateLimitedPerClient(t *testing.T) {
	f := newFixture(t)
	for i := range 5 {
		if w := f.rename(f.mira, "jay", "192.0.2.10:"+strconv.Itoa(1000+i)); !signalPatch(w, "That username is taken.") {
			t.Fatalf("attempt %d: %s", i+1, w.Body)
		}
	}
	if w := f.rename(f.mira, "mirabel", "192.0.2.10:2000"); !signalPatch(w, "Too many attempts.") {
		t.Fatalf("sixth attempt: %s", w.Body)
	}
	if w := f.rename(f.mira, "mirabel", "192.0.2.11:1000"); !signalPatch(w, "Username saved.") {
		t.Fatalf("another client shares the limit: %s", w.Body)
	}
}

// Another viewer's open leaderboard re-renders with the new name.
func TestRenameShowsOnAnotherViewersLeaderboard(t *testing.T) {
	f := newFixture(t)
	data, err := ladder.Load()
	if err != nil {
		t.Fatal(err)
	}
	path, _ := data.ShortestPath(data.DailyStart(ladder.DailyNumber(time.Now())))
	for _, word := range path[1:] {
		if w := f.postJSON("/play/guess", f.mira, map[string]string{"guess": word}); w.Code != http.StatusNoContent {
			t.Fatalf("guess %q: %d %s", word, w.Code, w.Body)
		}
	}

	server := httptest.NewServer(f.handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/leaderboard/state", nil)
	request.Header.Set("Datastar-Request", "true")
	request.AddCookie(&http.Cookie{Name: identity.SessionCookie, Value: f.admin})
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	events := bufio.NewReader(response.Body)
	waitFor := func(name string) {
		t.Helper()
		for event := readEvent(events); event != ""; event = readEvent(events) {
			if strings.Contains(event, `<main id="main"`) && strings.Contains(event, name) {
				return
			}
		}
		t.Fatalf("stream ended before showing %q", name)
	}
	waitFor(">mira<")
	if w := f.rename(f.mira, "Mirabel", "192.0.2.1:1000"); !signalPatch(w, "Username saved.") {
		t.Fatalf("rename: %s", w.Body)
	}
	waitFor(">Mirabel<")
}
