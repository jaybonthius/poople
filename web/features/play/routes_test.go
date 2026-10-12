package play_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	"poople/web/services/identity"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

const password = "correct horse battery"

type fixture struct {
	backend    *app.Backend
	handler    http.Handler
	setupToken string
	start      string   // today's start word
	path       []string // the shortest ladder the game service reveals
	par        int
	tomorrow   string // tomorrow's start word, which must never be sent
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	backend, err := app.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	handler, token, err := web.NewApp(t.Context(), backend, false, 1000)
	if err != nil {
		t.Fatal(err)
	}
	data, err := ladder.Load()
	if err != nil {
		t.Fatal(err)
	}
	number := ladder.DailyNumber(time.Now())
	start := data.DailyStart(number)
	path, ok := data.ShortestPath(start)
	if !ok {
		t.Fatalf("no ladder from %q", start)
	}
	return &fixture{backend, handler, token, start, path, len(path) - 1, data.DailyStart(number + 1)}
}

func (f *fixture) count(t *testing.T, query string) int64 {
	t.Helper()
	var n int64
	if err := f.backend.Database().Read(t.Context(), func(conn *sqlite.Conn) error {
		return sqlitex.Execute(conn, query, &sqlitex.ExecOptions{ResultFunc: func(stmt *sqlite.Stmt) error {
			n = stmt.ColumnInt64(0)
			return nil
		}})
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *fixture) page(t *testing.T, session string) string {
	t.Helper()
	r := httptest.NewRequest("GET", "/", nil)
	if session != "" {
		r.AddCookie(&http.Cookie{Name: identity.SessionCookie, Value: session})
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET / status=%d", w.Code)
	}
	return w.Body.String()
}

func (f *fixture) post(t *testing.T, path, session string, signals any) *httptest.ResponseRecorder {
	t.Helper()
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

// guess posts word as the browser does and returns the session cookie value,
// new when the browser had none.
func (f *fixture) guess(t *testing.T, session, word string) string {
	t.Helper()
	w := f.post(t, "/play/guess", session, map[string]any{"guess": word})
	if session != "" {
		if w.Code != http.StatusNoContent {
			t.Fatalf("guess %q status=%d body=%s", word, w.Code, w.Body)
		}
		return session
	}
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "datastar-patch-signals") || !strings.Contains(w.Body.String(), `"_playerSession"`) {
		t.Fatalf("first guess %q status=%d body=%s", word, w.Code, w.Body)
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == identity.SessionCookie && cookie.HttpOnly && cookie.SameSite == http.SameSiteLaxMode {
			return cookie.Value
		}
	}
	t.Fatal("first guess set no session cookie")
	return ""
}

// finish plays the shortest ladder from the current ladder's last word.
func (f *fixture) finish(t *testing.T, session string) string {
	t.Helper()
	for _, word := range f.path[1:] {
		session = f.guess(t, session, word)
	}
	return session
}

func (f *fixture) account(t *testing.T, path, username string, signals map[string]string) string {
	t.Helper()
	signals["username"], signals["password"] = username, password
	w := f.post(t, path, "", signals)
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == identity.SessionCookie {
			return cookie.Value
		}
	}
	t.Fatalf("%s %s: %d %s", path, username, w.Code, w.Body)
	return ""
}

// server serves the app until the test ends. Its Close runs after every
// stream's cancel (cleanups run last-registered first), so open streams
// cannot hold it up.
func (f *fixture) server(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(f.handler)
	t.Cleanup(server.Close)
	return server
}

// stream follows GET /play/state and delivers each full <main> frame.
type stream struct {
	frames chan string
}

func (f *fixture) stream(t *testing.T, server *httptest.Server, session string, shown map[string]any) *stream {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	target := server.URL + "/play/state"
	if shown != nil {
		encoded, _ := json.Marshal(shown)
		target += "?" + url.Values{"datastar": {string(encoded)}}.Encode()
	}
	r, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Accept-Encoding", "identity")
	r.Header.Set("Datastar-Request", "true")
	if session != "" {
		r.AddCookie(&http.Cookie{Name: identity.SessionCookie, Value: session})
	}
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stream status=%d", response.StatusCode)
	}
	s := &stream{frames: make(chan string, 16)}
	go func() {
		defer close(s.frames)
		defer response.Body.Close()
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		// An event's elements arrive as several "data: elements" lines, one per
		// line of HTML; a blank line ends the event.
		var lines []string
		for scanner.Scan() {
			line := scanner.Text()
			if element, ok := strings.CutPrefix(line, "data: elements "); ok {
				lines = append(lines, element)
				continue
			}
			if line != "" || len(lines) == 0 {
				continue
			}
			frame := strings.Join(lines, "\n")
			lines = nil
			if !strings.HasPrefix(frame, `<main id="main" class="play"`) {
				continue
			}
			select {
			case s.frames <- frame:
			case <-ctx.Done():
				return
			}
		}
	}()
	return s
}

func (s *stream) next(t *testing.T) string {
	t.Helper()
	select {
	case frame, ok := <-s.frames:
		if !ok {
			t.Fatal("stream ended")
		}
		return frame
	case <-time.After(5 * time.Second):
		t.Fatal("no stream frame within 5s")
		return ""
	}
}

// assertHidden fails when html reveals par, a solution word the ladder does
// not show yet, or a later puzzle's start word.
func (f *fixture) assertHidden(t *testing.T, html string, shownSteps int) {
	t.Helper()
	for _, secret := range []string{"result-score", ", par ", "(par ", "A shortest ladder", "Poople #", "standings"} {
		if strings.Contains(html, secret) {
			t.Fatalf("unfinished response contains %q", secret)
		}
	}
	unseen := []string{f.tomorrow}
	for _, word := range f.path[shownSteps+1 : len(f.path)-1] { // POOP is always the goal
		unseen = append(unseen, word)
	}
	// Words are drawn in capitals; lower-case four-letter strings also occur in
	// scripts and hashed asset names.
	for _, word := range unseen {
		if strings.Contains(html, strings.ToUpper(word)) {
			t.Fatalf("unfinished response contains %q", word)
		}
	}
}

func TestVisitorPageAndStreamCreateNoPlayer(t *testing.T) {
	f := newFixture(t)
	server := f.server(t)
	html := f.page(t, "")
	for _, want := range []string{"<!doctype html>", `<header class="masthead">`, "Daily #" + strconv.Itoa(ladder.DailyNumber(time.Now())), strings.ToUpper(f.start) + ", start word", "/play/state", `href="/login"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("page missing %q", want)
		}
	}
	f.assertHidden(t, html, 0)
	frame := f.stream(t, server, "", nil).next(t)
	f.assertHidden(t, frame, 0)
	if got := f.count(t, "SELECT count(*) FROM players"); got != 0 {
		t.Fatalf("page views created %d players", got)
	}
}

func TestFirstGuessCreatesThePlayerAndRepeatsAreHarmless(t *testing.T) {
	f := newFixture(t)
	f.page(t, "")
	session := f.guess(t, "", f.path[1])
	if got := f.count(t, "SELECT count(*) FROM players"); got != 1 {
		t.Fatalf("players after the first guess = %d", got)
	}
	f.guess(t, session, f.path[1]) // a double Enter
	if got := f.count(t, "SELECT count(*) FROM guesses"); got != 1 {
		t.Fatalf("guesses after repeating a word = %d", got)
	}
	if got := f.count(t, "SELECT rejection_seq FROM games"); got != 0 {
		t.Fatal("repeating a word was rejected")
	}
	// A guess typed on a puzzle the browser shows but that is no longer today's is dropped.
	if w := f.post(t, "/play/guess", session, map[string]any{"guess": f.path[2], "puzzleId": 999}); w.Code != http.StatusNoContent {
		t.Fatalf("stale guess status=%d", w.Code)
	}
	if got := f.count(t, "SELECT count(*) FROM guesses"); got != 1 {
		t.Fatalf("stale guess was stored: %d guesses", got)
	}
	if got := f.count(t, "SELECT count(*) FROM players"); got != 1 {
		t.Fatalf("players after more guesses = %d", got)
	}
}

func TestPlayRevealsParSolutionAndLeaderboardOnlyAfterFinishing(t *testing.T) {
	f := newFixture(t)
	server := f.server(t)
	session := f.guess(t, "", "zzzz")
	html := f.page(t, session)
	if !strings.Contains(html, "Not in word list") || !strings.Contains(html, "rung--rejected") {
		t.Fatal("rejection not shown")
	}
	f.assertHidden(t, html, 0)
	for i, word := range f.path[1 : len(f.path)-1] {
		f.guess(t, session, word)
		html := f.page(t, session)
		f.assertHidden(t, html, i+1)
		if strings.Contains(html, "Not in word list") {
			t.Fatal("rejection still shown after an accepted guess")
		}
		f.assertHidden(t, f.stream(t, server, session, nil).next(t), i+1)
	}
	f.guess(t, session, ladder.Target)
	for _, html := range []string{f.page(t, session), f.stream(t, server, session, nil).next(t)} {
		for _, want := range []string{
			`<h2 class="verdict verdict--perfect" id="finish-title">Perfect!</h2>`,
			"<strong>" + strconv.Itoa(f.par) + " steps</strong>, par " + strconv.Itoa(f.par),
			"On par",
			"A shortest ladder",
			"Today’s leaderboard",
			"Anonymous",
			`<span class="you-tag">You</span>`,
			`href="/signup">Create an account</a>`,
			`href="/unlimited"`,
			"Poople #" + strconv.Itoa(ladder.DailyNumber(time.Now())),
			"Next puzzle in",
		} {
			if !strings.Contains(html, want) {
				t.Fatalf("finished view missing %q", want)
			}
		}
		for _, word := range f.path {
			if !strings.Contains(html, strings.ToUpper(word)+"</span>") {
				t.Fatalf("finished view lacks solution word %q", word)
			}
		}
		if strings.Contains(html, strings.ToUpper(f.tomorrow)) {
			t.Fatal("finished view contains tomorrow's start word")
		}
	}
}

func TestShareTextIsSpoilerFree(t *testing.T) {
	f := newFixture(t)
	session := f.finish(t, "")
	html := f.page(t, session)
	start := strings.Index(html, `<share-result`)
	end := strings.Index(html[start:], `</share-result>`)
	if start < 0 || end < 0 {
		t.Fatal("no share component")
	}
	element := html[start : start+end]
	want := "Poople #" + strconv.Itoa(ladder.DailyNumber(time.Now())) + "\n" + strings.ToUpper(f.start) + " → POOP · " + strconv.Itoa(f.par) + " steps (par " + strconv.Itoa(f.par) + ")"
	for _, word := range f.path {
		want += "\n"
		for i := range 4 {
			if word[i] == ladder.Target[i] {
				want += "🟫"
			} else {
				want += "⬜"
			}
		}
	}
	if !strings.Contains(element, `text="`+want+`"`) {
		t.Fatalf("share element = %s, want text %q", element, want)
	}
	for _, word := range f.path[1 : len(f.path)-1] {
		if strings.Contains(strings.ToLower(element), word) {
			t.Fatalf("share text reveals %q", word)
		}
	}
}

func TestCrossOriginGuessIsRefused(t *testing.T) {
	f := newFixture(t)
	body, _ := json.Marshal(map[string]string{"guess": f.path[1]})
	r := httptest.NewRequest("POST", "/play/guess", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set("Origin", "https://untrusted.invalid")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin guess status=%d", w.Code)
	}
	if got := f.count(t, "SELECT count(*) FROM players"); got != 0 {
		t.Fatalf("cross-origin guess created %d players", got)
	}
}

func TestStreamFlipsOnlyStepsTheBrowserHasNotShown(t *testing.T) {
	f := newFixture(t)
	server := f.server(t)
	f.page(t, "")
	session := f.guess(t, "", f.path[1])
	puzzleID := f.count(t, "SELECT id FROM puzzles WHERE kind = 'daily'")
	// The page reopens its stream after the first guess while still showing no steps.
	if frame := f.stream(t, server, session, map[string]any{"puzzleId": puzzleID, "steps": 0}).next(t); !strings.Contains(frame, `id="rung-1" class="rung rung--fresh"`) {
		t.Fatal("the first guess does not flip in")
	}
	// A reload already shows the step.
	if frame := f.stream(t, server, session, map[string]any{"puzzleId": puzzleID, "steps": 1}).next(t); strings.Contains(frame, "rung--fresh") {
		t.Fatal("a step the browser already shows flips again")
	}
}

func TestAGuessUpdatesAnotherViewersLeaderboard(t *testing.T) {
	f := newFixture(t)
	server := f.server(t)
	admin := f.account(t, "/setup", "Admin", map[string]string{"setupToken": f.setupToken})
	f.finish(t, admin)
	watching := f.stream(t, server, admin, nil)
	frame := watching.next(t)
	if !strings.Contains(frame, `<li class="standing standing--you">`) || !strings.Contains(frame, "Admin") || strings.Contains(frame, "Create an account") {
		t.Fatal("admin's leaderboard lacks their own row")
	}
	// An anonymous player finishing is ranked too, without a name.
	if strings.Contains(frame, "Anonymous") {
		t.Fatal("admin's leaderboard lists an anonymous player before one finished")
	}
	f.finish(t, "")
	for !strings.Contains(frame, "Anonymous") {
		frame = watching.next(t)
	}
	bob := f.account(t, "/signup", "bob", map[string]string{})
	f.finish(t, bob)
	for !strings.Contains(frame, "bob") {
		frame = watching.next(t)
	}
}
