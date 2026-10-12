package play_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
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
	data       *ladder.Data
}

func newFixture(t *testing.T) *fixture {
	return newFixtureAt(t, time.Now)
}

// newFixtureAt runs the app on the clock now; the fixture's puzzle is the one
// open when it is created.
func newFixtureAt(t *testing.T, now func() time.Time) *fixture {
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
	handler, token, err := web.NewApp(t.Context(), backend, false, 1000, web.WithClock(now))
	if err != nil {
		t.Fatal(err)
	}
	data, err := ladder.Load()
	if err != nil {
		t.Fatal(err)
	}
	number := ladder.DailyNumber(now())
	start := data.DailyStart(number)
	path, ok := data.ShortestPath(start)
	if !ok {
		t.Fatalf("no ladder from %q", start)
	}
	return &fixture{backend, handler, token, start, path, len(path) - 1, data.DailyStart(number + 1), data}
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
		// Signal patches are kept and appended to the next frame for assertHidden.
		var lines, signals []string
		for scanner.Scan() {
			line := scanner.Text()
			if element, ok := strings.CutPrefix(line, "data: elements "); ok {
				lines = append(lines, element)
				continue
			}
			if strings.HasPrefix(line, "data: signals ") {
				signals = append(signals, line)
				continue
			}
			if line != "" || len(lines) == 0 {
				continue
			}
			frame := strings.Join(append(lines, signals...), "\n")
			lines, signals = nil, nil
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

var signalAttribute = regexp.MustCompile(`data-signals[^=\s>]*="([^"]*)"`)

// signalText is the signal data a response carries: data-signals attribute
// values and SSE "data: signals" lines, lower-cased.
func signalText(body string) string {
	var text []string
	for _, match := range signalAttribute.FindAllStringSubmatch(body, -1) {
		text = append(text, html.UnescapeString(match[1]))
	}
	for line := range strings.SplitSeq(body, "\n") {
		if signals, ok := strings.CutPrefix(line, "data: signals "); ok {
			text = append(text, signals)
		}
	}
	return strings.ToLower(strings.Join(text, "\n"))
}

var yesterdaySheet = regexp.MustCompile(`(?s)<dialog\s+id="yesterday".*?</dialog>`)

// splitYesterday returns body without Yesterday's answer, and that sheet.
func splitYesterday(body string) (rest, sheet string) {
	sheet = yesterdaySheet.FindString(body)
	return strings.Replace(body, sheet, "", 1), sheet
}

var answerRung = regexp.MustCompile(`<li class="rung[^"]*">(.*?)</li>`)
var answerTile = regexp.MustCompile(`<span class="tile[^"]*" aria-hidden="true">([A-Z])</span>`)

// assertYesterday fails unless body's Yesterday's answer is daily puzzle
// number: its title, par and its shortest ladder top to bottom, and nothing
// from today's puzzle.
func (f *fixture) assertYesterday(t *testing.T, body string, number int) {
	t.Helper()
	_, sheet := splitYesterday(body)
	start := f.data.DailyStart(number)
	path, _ := f.data.ShortestPath(start)
	for _, want := range []string{
		`aria-labelledby="yesterday-title"`,
		`<h2 class="sheet-title" id="yesterday-title">Yesterday’s answer</h2>`,
		"Poople #" + strconv.Itoa(number) + ": " + strings.ToUpper(start),
		"The shortest route to POOP took <strong>" + strconv.Itoa(len(path)-1) + " steps</strong>, which is par.",
	} {
		if !strings.Contains(sheet, want) {
			t.Fatalf("Yesterday's answer missing %q: %s", want, sheet)
		}
	}
	var rungs []string
	for _, rung := range answerRung.FindAllStringSubmatch(sheet, -1) {
		var word strings.Builder
		for _, tile := range answerTile.FindAllStringSubmatch(rung[1], -1) {
			word.WriteString(strings.ToLower(tile[1]))
		}
		rungs = append(rungs, word.String())
	}
	if strings.Join(rungs, " ") != strings.Join(path, " ") {
		t.Fatalf("Yesterday's ladder = %v, want #%d's %v", rungs, number, path)
	}
	if today := f.data.DailyStart(number + 1); strings.Contains(sheet, strings.ToUpper(today)) {
		t.Fatalf("Yesterday's answer contains today's start word %q", today)
	}
}

// assertHidden fails when body, outside Yesterday's answer, reveals par, a
// solution word the ladder does not show yet, or a later puzzle's start word.
func (f *fixture) assertHidden(t *testing.T, body string, shownSteps int) {
	t.Helper()
	body, _ = splitYesterday(body)
	for _, secret := range []string{"result-score", ", par ", "(par ", "A shortest ladder", "Poople #", "standings"} {
		if strings.Contains(body, secret) {
			t.Fatalf("unfinished response contains %q", secret)
		}
	}
	signals := signalText(body)
	if regexp.MustCompile(`\bpar\b`).MatchString(signals) {
		t.Fatalf("unfinished response's signals mention par: %s", signals)
	}
	unseen := []string{f.tomorrow}
	for _, word := range f.path[shownSteps+1 : len(f.path)-1] { // POOP is always the goal
		unseen = append(unseen, word)
	}
	// Words are drawn in capitals; lower-case four-letter strings also occur in
	// scripts and hashed asset names, so lower case is checked only in signals.
	for _, word := range unseen {
		if strings.Contains(body, strings.ToUpper(word)) || regexp.MustCompile(`\b`+word+`\b`).MatchString(signals) {
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

func TestPlayRevealsParAndLeaderboardOnlyAfterFinishing(t *testing.T) {
	f := newFixture(t)
	server := f.server(t)
	session := f.guess(t, "", "zzzz")
	html := f.page(t, session)
	if !strings.Contains(html, "Not in word list") || !strings.Contains(html, "rung--rejected") {
		t.Fatal("rejection not shown")
	}
	// A page loaded after the rejection remembers it as seen, so it does not replay.
	if !strings.Contains(html, `class="toast" style="display: none" data-show="$_toastSeen !== `) || !strings.Contains(html, `data-signals:_toast-seen__ifmissing="&#34;`) {
		t.Fatal("page load replays a stored rejection")
	}
	if strings.Contains(html, `aria-label="Keyboard" inert`) {
		t.Fatal("unfinished game's keyboard is inert")
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
			"Today’s leaderboard",
			"Anonymous",
			`<span class="you-tag">You</span>`,
			`href="/signup">Create an account</a>`,
			`href="/unlimited"`,
			"Poople #" + strconv.Itoa(ladder.DailyNumber(time.Now())),
			"Next puzzle in",
			`<section class="keyboard" aria-label="Keyboard" inert`,
		} {
			if !strings.Contains(html, want) {
				t.Fatalf("finished view missing %q", want)
			}
		}
		if rest, _ := splitYesterday(html); strings.Contains(rest, "A shortest ladder") || strings.Contains(rest, "ladder--answer") {
			t.Fatal("finished daily view draws today's shortest ladder")
		}
		if strings.Contains(html, strings.ToUpper(f.tomorrow)) {
			t.Fatal("finished view contains tomorrow's start word")
		}
	}
}

// Finishing today's puzzle by a longer route never reveals a shortest one:
// outside Yesterday's answer, the page and its stream show only the player's
// own ladder. The best route appears the next day as Yesterday's answer.
func TestFinishedDailyHidesTheShortestLadderUntilTomorrow(t *testing.T) {
	clock := &runningClock{}
	clock.Set(time.Date(2026, time.October, 11, 9, 0, 0, 0, time.UTC)) // #423, FUSE
	f := newFixtureAt(t, clock.Now)
	server := f.server(t)
	route := f.longerRoute(t)
	var withheld []string
	for _, word := range f.path {
		if !slices.Contains(route, word) {
			withheld = append(withheld, word)
		}
	}
	if len(withheld) == 0 {
		t.Fatalf("route %v shares every word with the shortest ladder %v", route, f.path)
	}
	session := ""
	for _, word := range route[1:] {
		session = f.guess(t, session, word)
	}
	for _, html := range []string{f.page(t, session), f.stream(t, server, session, nil).next(t)} {
		if !strings.Contains(html, "Solved") || !strings.Contains(html, "+"+strconv.Itoa(len(route)-1-f.par)+" over par") {
			t.Fatal("finish sheet missing")
		}
		rest, _ := splitYesterday(html)
		for _, word := range withheld {
			if strings.Contains(rest, strings.ToUpper(word)) || strings.Contains(signalText(rest), word) {
				t.Fatalf("finished daily view reveals solution word %q", word)
			}
		}
		for _, want := range []string{`id="yesterday-link"`, `id="finish-yesterday-link"`, "Today’s shortest ladder appears tomorrow as Yesterday’s answer."} {
			if !strings.Contains(rest, want) {
				t.Fatalf("finished daily view missing %q", want)
			}
		}
		f.assertYesterday(t, html, 422)
	}
	clock.Set(time.Date(2026, time.October, 12, 8, 0, 0, 0, time.UTC))
	html := f.page(t, session)
	f.assertYesterday(t, html, 423)
	if !strings.Contains(html, "Daily #424<") {
		t.Fatal("the next day's page is not #424")
	}
}

// longerRoute is a ladder from today's start to POOP that avoids the shortest
// ladder's first step.
func (f *fixture) longerRoute(t *testing.T) []string {
	t.Helper()
	for _, slack := range []int{1, 0} {
		for _, next := range f.data.Neighbors(f.start) {
			if d, ok := f.data.Distance(next); next == f.path[1] || !ok || d != f.par-1+slack {
				continue
			}
			rest, _ := f.data.ShortestPath(next)
			if !slices.Contains(rest, f.path[1]) && !slices.Contains(rest, f.start) {
				return append([]string{f.start}, rest...)
			}
		}
	}
	t.Fatalf("no route from %q avoiding %q", f.start, f.path[1])
	return nil
}

// Yesterday's answer opens from the heading before the player has guessed.
func TestYesterdaysAnswerIsOnThePageBeforePlaying(t *testing.T) {
	f := newFixture(t)
	server := f.server(t)
	number := ladder.DailyNumber(time.Now())
	for _, html := range []string{f.page(t, ""), f.stream(t, server, "", nil).next(t)} {
		if !strings.Contains(html, `<button type="button" id="yesterday-link" class="result-link" aria-haspopup="dialog" data-on:click="$_yesterdayOpen = true">Yesterday’s answer</button>`) {
			t.Fatal("heading lacks Yesterday's answer")
		}
		f.assertYesterday(t, html, number-1)
		f.assertHidden(t, html, 0)
	}
	if got := f.count(t, "SELECT count(*) FROM players"); got != 0 {
		t.Fatalf("Yesterday's answer created %d players", got)
	}
}

// Puzzle #0 has no yesterday.
func TestNoYesterdaysAnswerOnTheFirstPuzzle(t *testing.T) {
	f := newFixtureAt(t, func() time.Time { return time.Date(2025, time.August, 14, 9, 0, 0, 0, time.UTC) })
	html := f.page(t, "")
	if !strings.Contains(html, "Daily #0<") || strings.Contains(html, "Yesterday’s answer") || strings.Contains(html, `id="yesterday"`) {
		t.Fatal("puzzle #0 offers a Yesterday's answer")
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

func TestFirstGuessResponseIsNotStored(t *testing.T) {
	f := newFixture(t)
	w := f.post(t, "/play/guess", "", map[string]any{"guess": f.path[1]})
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("first guess status=%d Cache-Control=%q", w.Code, w.Header().Get("Cache-Control"))
	}
	f.assertHidden(t, w.Body.String(), 1)
}

func TestNewPlayersAreRateLimitedPerClient(t *testing.T) {
	f := newFixture(t)
	var session string
	for range 10 {
		session = f.guess(t, "", "zzzz")
	}
	w := f.post(t, "/play/guess", "", map[string]any{"guess": "zzzz"})
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("eleventh new player status=%d", w.Code)
	}
	if got := f.count(t, "SELECT count(*) FROM players"); got != 10 {
		t.Fatalf("players = %d, want 10", got)
	}
	// Players who already exist keep guessing.
	for _, word := range f.path[1:3] {
		f.guess(t, session, word)
	}
}

// runningClock runs in real time from a time the test sets.
type runningClock struct {
	mu        sync.Mutex
	base, set time.Time
}

func (c *runningClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.base.Add(time.Since(c.set))
}

func (c *runningClock) Set(base time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.base, c.set = base, time.Now()
}

func TestStreamMovesToTheNextPuzzleAt0800UTCAndDropsGuessesOnTheClosedOne(t *testing.T) {
	const number = 500
	opens := ladder.DailyOpens(number + 1)
	clock := &runningClock{}
	clock.Set(opens.Add(-time.Hour))
	f := newFixtureAt(t, clock.Now)
	server := f.server(t)
	clock.Set(opens.Add(-time.Second))
	session := f.guess(t, "", f.path[1])
	closed := f.count(t, "SELECT id FROM puzzles WHERE kind = 'daily'")
	watching := f.stream(t, server, session, nil)
	frame := watching.next(t)
	if !strings.Contains(frame, "Daily #"+strconv.Itoa(number)+"<") || !strings.Contains(frame, strings.ToUpper(f.path[1])) {
		t.Fatalf("frame before 08:00 UTC is not puzzle #%d with the guess", number)
	}
	f.assertHidden(t, frame, 1)
	f.assertYesterday(t, frame, number-1)
	// Nothing commits; the stream's own timer moves it on at 08:00 UTC.
	frame = watching.next(t)
	if !strings.Contains(frame, "Daily #"+strconv.Itoa(number+1)+"<") || !strings.Contains(frame, strings.ToUpper(f.tomorrow)+", start word") {
		t.Fatalf("frame after 08:00 UTC is not puzzle #%d", number+1)
	}
	f.assertYesterday(t, frame, number)
	// A guess the browser typed against the closed puzzle is dropped.
	if w := f.post(t, "/play/guess", session, map[string]any{"guess": f.path[2], "puzzleId": closed}); w.Code != http.StatusNoContent {
		t.Fatalf("guess on the closed puzzle status=%d", w.Code)
	}
	if got := f.count(t, "SELECT count(*) FROM guesses"); got != 1 {
		t.Fatalf("guesses = %d after a guess on the closed puzzle", got)
	}
}

func TestSigningUpNamesTheFinisherOnAnotherViewersLeaderboard(t *testing.T) {
	f := newFixture(t)
	server := f.server(t)
	admin := f.account(t, "/setup", "Admin", map[string]string{"setupToken": f.setupToken})
	f.finish(t, admin)
	anonymous := f.finish(t, "")
	watching := f.stream(t, server, admin, nil)
	frame := watching.next(t)
	if !strings.Contains(frame, "Anonymous") {
		t.Fatal("anonymous finisher missing")
	}
	if w := f.post(t, "/signup", anonymous, map[string]string{"username": "carol", "password": password}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "window.location") {
		t.Fatalf("sign-up: %d %s", w.Code, w.Body)
	}
	for strings.Contains(frame, "Anonymous") || !strings.Contains(frame, "carol") {
		frame = watching.next(t)
	}
}

func TestLoggingInMovesTheAnonymousFinishOntoAnotherViewersLeaderboard(t *testing.T) {
	f := newFixture(t)
	server := f.server(t)
	admin := f.account(t, "/setup", "Admin", map[string]string{"setupToken": f.setupToken})
	f.finish(t, admin)
	f.account(t, "/signup", "bob", map[string]string{}) // bob has not played today
	anonymous := f.finish(t, "")
	watching := f.stream(t, server, admin, nil)
	frame := watching.next(t)
	if !strings.Contains(frame, "Anonymous") || strings.Contains(frame, "bob") {
		t.Fatal("leaderboard before log-in")
	}
	if w := f.post(t, "/login", anonymous, map[string]string{"username": "bob", "password": password}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "window.location") {
		t.Fatalf("log-in: %d %s", w.Code, w.Body)
	}
	for strings.Contains(frame, "Anonymous") || !strings.Contains(frame, "bob") {
		frame = watching.next(t)
	}
}
