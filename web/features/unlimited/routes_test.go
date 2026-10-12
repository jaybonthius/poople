package unlimited_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
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

type fixture struct {
	backend *app.Backend
	handler http.Handler
	data    *ladder.Data
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
	handler, _, err := web.NewApp(t.Context(), backend, false, 1000)
	if err != nil {
		t.Fatal(err)
	}
	data, err := ladder.Load()
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{backend, handler, data}
}

func (f *fixture) query(t *testing.T, query string, args ...any) (n int64, text string) {
	t.Helper()
	if err := f.backend.Database().Read(t.Context(), func(conn *sqlite.Conn) error {
		return sqlitex.Execute(conn, query, &sqlitex.ExecOptions{Args: args, ResultFunc: func(stmt *sqlite.Stmt) error {
			n, text = stmt.ColumnInt64(0), stmt.ColumnText(0)
			return nil
		}})
	}); err != nil {
		t.Fatal(err)
	}
	return n, text
}

func (f *fixture) count(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	n, _ := f.query(t, query, args...)
	return n
}

func (f *fixture) get(t *testing.T, path, session string) string {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	if session != "" {
		r.AddCookie(&http.Cookie{Name: identity.SessionCookie, Value: session})
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s status=%d", path, w.Code)
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

// puzzle is the viewer's current unlimited puzzle as the tests know it.
type puzzle struct {
	id    int64
	start string
	path  []string // the shortest ladder the game service reveals
}

// newPuzzle posts New puzzle as the browser does and returns the session
// cookie value, new when the browser had none, and the puzzle it started.
func (f *fixture) newPuzzle(t *testing.T, session string) (string, puzzle) {
	t.Helper()
	w := f.post(t, "/unlimited/new", session, map[string]any{})
	if session == "" {
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "datastar-patch-signals") || !strings.Contains(w.Body.String(), `"_playerSession"`) {
			t.Fatalf("first New puzzle status=%d body=%s", w.Code, w.Body)
		}
		for _, cookie := range w.Result().Cookies() {
			if cookie.Name == identity.SessionCookie && cookie.HttpOnly && cookie.SameSite == http.SameSiteLaxMode {
				session = cookie.Value
			}
		}
		if session == "" {
			t.Fatal("first New puzzle set no session cookie")
		}
	} else if w.Code != http.StatusNoContent {
		t.Fatalf("New puzzle status=%d body=%s", w.Code, w.Body)
	}
	var p puzzle
	p.id, _ = f.query(t, "SELECT max(id) FROM puzzles WHERE kind = 'unlimited'")
	_, p.start = f.query(t, "SELECT start_word FROM puzzles WHERE id = ?", p.id)
	path, ok := f.data.ShortestPath(p.start)
	if !ok {
		t.Fatalf("no ladder from %q", p.start)
	}
	p.path = path
	return session, p
}

// guess posts word against the puzzle the browser shows.
func (f *fixture) guess(t *testing.T, session string, puzzleID int64, word string) {
	t.Helper()
	if w := f.post(t, "/unlimited/guess", session, map[string]any{"guess": word, "puzzleId": puzzleID}); w.Code != http.StatusNoContent {
		t.Fatalf("guess %q status=%d body=%s", word, w.Code, w.Body)
	}
}

func (f *fixture) server(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(f.handler)
	t.Cleanup(server.Close)
	return server
}

type stream struct {
	frames chan string
}

// stream follows GET /unlimited/state and delivers each full <main> frame.
func (f *fixture) stream(t *testing.T, server *httptest.Server, session string, shown map[string]any) *stream {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	target := server.URL + "/unlimited/state"
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
			if !strings.HasPrefix(frame, `<main id="main" class="play unlimited"`) {
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

// shows reports whether html is the play surface for the puzzle.
func shows(html string, p puzzle) bool {
	return strings.Contains(html, "&#34;puzzleId&#34;:"+strconv.FormatInt(p.id, 10)+",") &&
		strings.Contains(html, strings.ToUpper(p.start)+", start word")
}

// assertHidden fails when html reveals par or a solution word the ladder
// does not show yet.
func assertHidden(t *testing.T, html string, p puzzle, shownSteps int) {
	t.Helper()
	for _, secret := range []string{"result-score", ", par ", "A shortest ladder", "finish-title"} {
		if strings.Contains(html, secret) {
			t.Fatalf("unfinished response contains %q", secret)
		}
	}
	for _, word := range p.path[shownSteps+1 : len(p.path)-1] { // POOP is always the goal
		if strings.Contains(html, strings.ToUpper(word)) {
			t.Fatalf("unfinished response contains solution word %q", word)
		}
	}
}

func TestVisitorSeesTheStartStateAndCreatesNoPlayer(t *testing.T) {
	f := newFixture(t)
	server := f.server(t)
	html := f.get(t, "/unlimited", "")
	for _, want := range []string{
		"<title>Unlimited · Poople</title>",
		`href="/unlimited" aria-label="Unlimited" aria-current="page"`,
		"/unlimited/state",
		`data-on:click="@post(&#39;/unlimited/new&#39;)"`,
		"New puzzle",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("page missing %q", want)
		}
	}
	if strings.Contains(html, `class="keyboard"`) || strings.Contains(html, `class="ladder"`) {
		t.Fatal("start state shows a play surface")
	}
	if frame := f.stream(t, server, "", nil).next(t); !strings.Contains(frame, "New puzzle") || strings.Contains(frame, `class="ladder"`) {
		t.Fatalf("visitor stream frame = %s", frame)
	}
	// A guess from a browser without a puzzle is dropped without creating a player.
	f.guess(t, "", 0, "fuse")
	if got := f.count(t, "SELECT count(*) FROM players"); got != 0 {
		t.Fatalf("page views and a stray guess created %d players", got)
	}
}

func TestNewPuzzleCreatesThePlayerAndACommonPuzzle(t *testing.T) {
	f := newFixture(t)
	server := f.server(t)
	watching := f.stream(t, server, "", nil)
	watching.next(t)
	session, p := f.newPuzzle(t, "")
	if got := f.count(t, "SELECT count(*) FROM players"); got != 1 {
		t.Fatalf("players after New puzzle = %d", got)
	}
	par := f.count(t, "SELECT par FROM puzzles WHERE id = ?", p.id)
	if dist, _ := f.data.Distance(p.start); par < ladder.MinStartDistance || par != int64(dist) || !f.data.Common(p.start) {
		t.Fatalf("puzzle %q has par %d, want a common word with par >= %d", p.start, par, ladder.MinStartDistance)
	}
	if !slices.Contains(f.data.Unlimited(), p.start) {
		t.Fatalf("start word %q is not in the unlimited pool", p.start)
	}
	html := f.get(t, "/unlimited", session)
	if !shows(html, p) || !strings.Contains(html, "/unlimited/guess") {
		t.Fatal("page does not show the new puzzle")
	}
	assertHidden(t, html, p, 0)
	// The anonymous stream has no player to follow; the reopened one does.
	frame := f.stream(t, server, session, nil).next(t)
	if !shows(frame, p) {
		t.Fatal("reopened stream does not show the new puzzle")
	}
	assertHidden(t, frame, p, 0)
}

func TestGuessesFollowTheUnlimitedPuzzleAndRevealOnlyAfterFinishing(t *testing.T) {
	f := newFixture(t)
	server := f.server(t)
	session, p := f.newPuzzle(t, "")
	watching := f.stream(t, server, session, map[string]any{"puzzleId": p.id, "steps": 0})
	watching.next(t)

	f.guess(t, session, p.id, "zzzz")
	frame := watching.next(t)
	if !strings.Contains(frame, "Not in word list") || !strings.Contains(frame, "rung--rejected") {
		t.Fatal("not-in-list rejection not shown")
	}
	f.guess(t, session, p.id, ladder.Target) // listed, but par >= 4 puts it far from the start
	if frame = watching.next(t); !strings.Contains(frame, "Not one letter different") {
		t.Fatal("not-one-letter rejection not shown")
	}
	for i, word := range p.path[1 : len(p.path)-1] {
		f.guess(t, session, p.id, word)
		frame = watching.next(t)
		if !strings.Contains(frame, `id="rung-`+strconv.Itoa(i+1)+`" class="rung rung--fresh"`) {
			t.Fatalf("step %d does not flip in", i+1)
		}
		assertHidden(t, frame, p, i+1)
		assertHidden(t, f.get(t, "/unlimited", session), p, i+1)
	}
	f.guess(t, session, p.id, ladder.Target)
	par := len(p.path) - 1
	for _, html := range []string{watching.next(t), f.get(t, "/unlimited", session)} {
		for _, want := range []string{
			`<h2 class="verdict verdict--perfect" id="finish-title">Perfect!</h2>`,
			strings.ToUpper(p.start) + " → POOP",
			"<strong>" + strconv.Itoa(par) + " steps</strong>, par " + strconv.Itoa(par),
			"On par",
			"A shortest ladder",
			`<a class="button" href="/">Daily puzzle</a>`,
			"never count toward your statistics or the leaderboard",
		} {
			if !strings.Contains(html, want) {
				t.Fatalf("finished view missing %q", want)
			}
		}
		if n := strings.Count(html, "New puzzle"); n < 2 {
			t.Fatalf("finished view has %d New puzzle buttons, want the heading's and the sheet's", n)
		}
		for _, absent := range []string{"leaderboard</h3>", "standings", "share-result", "Next puzzle in", "Poople #"} {
			if strings.Contains(html, absent) {
				t.Fatalf("unlimited finish sheet contains %q", absent)
			}
		}
		for _, word := range p.path {
			if !strings.Contains(html, strings.ToUpper(word)+"</span>") {
				t.Fatalf("finished view lacks solution word %q", word)
			}
		}
	}
	if got := f.count(t, "SELECT count(*) FROM guesses"); got != int64(par) {
		t.Fatalf("guesses stored = %d, want %d", got, par)
	}
	// A finished game takes no more guesses.
	f.guess(t, session, p.id, p.path[len(p.path)-2])
	if got := f.count(t, "SELECT count(*) FROM guesses"); got != int64(par) {
		t.Fatal("a guess after finishing was stored")
	}
}

func TestGuessesUseThePuzzlesFrozenWordList(t *testing.T) {
	f := newFixture(t)
	session, p := f.newPuzzle(t, "")
	small, err := ladder.Parse([]byte("pads\npals\npods\npoop\npoos\n"), []byte("pals\n"), []byte("pals\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.LoadWords(t.Context(), f.backend.Database(), small); err != nil {
		t.Fatal(err)
	}
	// The started puzzle still validates against CSW24, which the new list lacks.
	for _, word := range p.path[1:] {
		f.guess(t, session, p.id, word)
	}
	if !strings.Contains(f.get(t, "/unlimited", session), "A shortest ladder") {
		t.Fatal("the old puzzle could not be finished on its own list")
	}
	// A new puzzle comes from the new list's pool: its only common word four from POOP.
	_, next := f.newPuzzle(t, session)
	if next.start != "pals" {
		t.Fatalf("new puzzle after a list change starts at %q, want pals", next.start)
	}
	f.guess(t, session, next.id, "pale") // in CSW24, not in the new list
	if html := f.get(t, "/unlimited", session); !strings.Contains(html, "Not in word list") {
		t.Fatal("the new puzzle accepted a word outside its list")
	}
}

func TestAnotherPlayerCannotGuessOnSomeoneElsesPuzzle(t *testing.T) {
	f := newFixture(t)
	alice, p := f.newPuzzle(t, "")
	// Bob has a player from his own puzzle, then posts against Alice's.
	bob, _ := f.newPuzzle(t, "")
	f.guess(t, bob, p.id, p.path[1])
	aliceGame := f.count(t, "SELECT id FROM games WHERE puzzle_id = ?", p.id)
	if got := f.count(t, "SELECT count(*) FROM guesses WHERE game_id = ?", aliceGame); got != 0 {
		t.Fatalf("bob's guess landed on alice's game: %d guesses", got)
	}
	if got := f.count(t, "SELECT count(*) FROM games WHERE puzzle_id = ?", p.id); got != 1 {
		t.Fatalf("alice's puzzle has %d games", got)
	}
	if html := f.get(t, "/unlimited", bob); shows(html, p) {
		t.Fatal("bob sees alice's puzzle")
	}
	f.guess(t, alice, p.id, p.path[1])
	if got := f.count(t, "SELECT count(*) FROM guesses WHERE game_id = ?", aliceGame); got != 1 {
		t.Fatalf("alice's own guess stored %d times", got)
	}
}

func TestNewPuzzleReplacesTheCurrentOne(t *testing.T) {
	f := newFixture(t)
	server := f.server(t)
	session, first := f.newPuzzle(t, "")
	f.guess(t, session, first.id, first.path[1])
	watching := f.stream(t, server, session, map[string]any{"puzzleId": first.id, "steps": 1})
	if frame := watching.next(t); !shows(frame, first) || strings.Contains(frame, "rung--fresh") {
		t.Fatal("stream does not show the first puzzle as the browser left it")
	}
	_, second := f.newPuzzle(t, session)
	if second.id == first.id {
		t.Fatal("New puzzle did not create a puzzle")
	}
	frame := watching.next(t)
	if !shows(frame, second) || strings.Contains(frame, `id="rung-1"`) || strings.Contains(frame, "rung--fresh") {
		t.Fatal("stream does not move to the new puzzle with an empty ladder")
	}
	assertHidden(t, frame, second, 0)
	// A guess typed on the replaced puzzle is dropped.
	f.guess(t, session, first.id, first.path[2])
	if got := f.count(t, "SELECT count(*) FROM guesses"); got != 1 {
		t.Fatalf("guesses after posting to the replaced puzzle = %d", got)
	}
	if got := f.count(t, "SELECT rejection_seq FROM games WHERE puzzle_id = ?", second.id); got != 0 {
		t.Fatal("a guess typed on the replaced puzzle was judged against the new one")
	}
	f.guess(t, session, second.id, second.path[1])
	if html := f.get(t, "/unlimited", session); !shows(html, second) || !strings.Contains(html, `<span class="visually-hidden">`+strings.ToUpper(second.path[1])) {
		t.Fatal("guess on the new puzzle not shown")
	}
}

func TestUnlimitedLeavesDailyPlayAlone(t *testing.T) {
	f := newFixture(t)
	daily := f.data.DailyStart(ladder.DailyNumber(time.Now()))
	dailyPath, _ := f.data.ShortestPath(daily)
	// Alice solves the daily puzzle; Bob plays only unlimited.
	var alice string
	for _, word := range dailyPath[1:] {
		w := f.post(t, "/play/guess", alice, map[string]any{"guess": word})
		if alice == "" {
			for _, cookie := range w.Result().Cookies() {
				if cookie.Name == identity.SessionCookie {
					alice = cookie.Value
				}
			}
		}
	}
	bob, p := f.newPuzzle(t, "")
	for _, word := range p.path[1:] {
		f.guess(t, bob, p.id, word)
	}
	if got := f.count(t, "SELECT count(*) FROM games g JOIN puzzles p ON p.id = g.puzzle_id WHERE p.kind = 'daily'"); got != 1 {
		t.Fatalf("daily games = %d, want alice's only", got)
	}
	if html := f.get(t, "/", alice); !strings.Contains(html, "Today’s leaderboard") || strings.Contains(html, "anonymous player also solved") {
		t.Fatal("bob's unlimited game shows on today's leaderboard")
	}
	html := f.get(t, "/", bob)
	if !strings.Contains(html, strings.ToUpper(daily)+", start word") || strings.Contains(html, `id="rung-1"`) || strings.Contains(html, "finish-title") {
		t.Fatal("bob's daily puzzle is not untouched")
	}
	// Daily guesses never land on the unlimited game, and vice versa.
	if w := f.post(t, "/play/guess", bob, map[string]any{"guess": dailyPath[1]}); w.Code != http.StatusNoContent {
		t.Fatalf("daily guess status=%d", w.Code)
	}
	unlimitedGame := f.count(t, "SELECT id FROM games WHERE puzzle_id = ?", p.id)
	if got := f.count(t, "SELECT count(*) FROM guesses WHERE game_id = ?", unlimitedGame); got != int64(len(p.path)-1) {
		t.Fatalf("unlimited game has %d guesses", got)
	}
}
