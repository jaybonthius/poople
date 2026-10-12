package leaderboard_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

type fixture struct {
	backend  *app.Backend
	handler  http.Handler
	number   int      // today's puzzle number
	path     []string // a shortest ladder for today's puzzle
	tomorrow string   // tomorrow's start word, which must never be sent
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
	handler, setupToken, err := web.NewApp(t.Context(), backend, false, 1000)
	if err != nil {
		t.Fatal(err)
	}
	data, err := ladder.Load()
	if err != nil {
		t.Fatal(err)
	}
	number := ladder.DailyNumber(time.Now())
	path, _ := data.ShortestPath(data.DailyStart(number))
	f := &fixture{backend, handler, number, path, data.DailyStart(number + 1)}
	// Sign-up opens once the admin account exists.
	sessionCookie(t, f.post(t, "/setup", "", map[string]string{"setupToken": setupToken, "username": "admin", "password": "correct horse battery"}))
	return f
}

func (f *fixture) get(t *testing.T, target, session string) string {
	t.Helper()
	r := httptest.NewRequest("GET", target, nil)
	if session != "" {
		r.AddCookie(&http.Cookie{Name: identity.SessionCookie, Value: session})
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s status=%d", target, w.Code)
	}
	return w.Body.String()
}

func (f *fixture) post(t *testing.T, target, session string, signals any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(signals)
	r := httptest.NewRequest("POST", target, bytes.NewReader(body))
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

func sessionCookie(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == identity.SessionCookie {
			return cookie.Value
		}
	}
	t.Fatalf("no session cookie: %d %s", w.Code, w.Body)
	return ""
}

// finish plays today's shortest ladder, after detours trips to a neighbour
// of the start word and back, and returns the session, new when there was none.
func (f *fixture) finish(t *testing.T, session string, detours int) string {
	t.Helper()
	data, _ := ladder.Load()
	var words []string
	for range detours {
		words = append(words, data.Neighbors(f.path[0])[0], f.path[0])
	}
	for _, word := range append(words, f.path[1:]...) {
		w := f.post(t, "/play/guess", session, map[string]string{"guess": word})
		if session == "" {
			session = sessionCookie(t, w)
		} else if w.Code != http.StatusNoContent {
			t.Fatalf("guess %q status=%d", word, w.Code)
		}
	}
	return session
}

func (f *fixture) signup(t *testing.T, username string) string {
	t.Helper()
	return sessionCookie(t, f.post(t, "/signup", "", map[string]string{"username": username, "password": "correct horse battery"}))
}

func (f *fixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if err := f.backend.Database().Write(t.Context(), func(conn *sqlite.Conn) error {
		return sqlitex.Execute(conn, query, &sqlitex.ExecOptions{Args: args})
	}); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) playerID(t *testing.T, username string) int64 {
	t.Helper()
	var id int64
	if err := f.backend.Database().Read(t.Context(), func(conn *sqlite.Conn) error {
		return sqlitex.Execute(conn, "SELECT player_id FROM accounts WHERE username = ?", &sqlitex.ExecOptions{
			Args: []any{username}, ResultFunc: func(stmt *sqlite.Stmt) error { id = stmt.ColumnInt64(0); return nil },
		})
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// pastFinish records a finished game on daily puzzle number, which must be
// before today, with par 5 and the given steps.
func (f *fixture) pastFinish(t *testing.T, playerID int64, number, steps int) {
	t.Helper()
	f.exec(t, "INSERT INTO puzzles (kind, number, list_id, start_word, par, created_at) VALUES ('daily', ?, 1, 'fuse', 5, 0) ON CONFLICT DO NOTHING", number)
	f.exec(t, "INSERT INTO games (puzzle_id, player_id, started_at, finished_at) SELECT id, ?, 0, 60000 FROM puzzles WHERE kind = 'daily' AND number = ?", playerID, number)
	for i := range steps {
		f.exec(t, "INSERT INTO guesses (game_id, position, word, created_at) SELECT g.id, ?, 'fuss', 0 FROM games g JOIN puzzles p ON p.id = g.puzzle_id WHERE p.number = ? AND g.player_id = ?", i+1, number, playerID)
	}
}

// assertNoSpoilers fails when html holds a word of today's ladder other than
// the start word and POOP, or tomorrow's start word.
func (f *fixture) assertNoSpoilers(t *testing.T, html string) {
	t.Helper()
	for _, word := range append([]string{f.tomorrow}, f.path[1:len(f.path)-1]...) {
		if strings.Contains(html, strings.ToUpper(word)) {
			t.Fatalf("leaderboard contains %q", word)
		}
	}
}

func TestTodayRanksEveryFinisherAndShowsParOnlyToFinishers(t *testing.T) {
	f := newFixture(t)
	mira := f.signup(t, "mira")
	f.finish(t, mira, 1)
	anonymous := f.finish(t, "", 0)
	f.signup(t, "idle")

	visitor := f.get(t, "/leaderboard", "")
	f.assertNoSpoilers(t, visitor)
	for _, want := range []string{
		`<a href="/leaderboard" aria-current="page">Today</a>`,
		"Daily #" + strconv.Itoa(f.number) + ", " + strings.ToUpper(f.path[0]) + " → POOP. Ranked by steps, then solve time.",
		`<span class="rank rank--first">1</span>`, "Anonymous", "mira",
		"Par appears once you finish.", `href="/signup">Create an account</a>`, "/leaderboard/state",
	} {
		if !strings.Contains(visitor, want) {
			t.Fatalf("visitor's leaderboard lacks %q", want)
		}
	}
	for _, secret := range []string{"Par " + strconv.Itoa(len(f.path)-1), "steps-delta", "idle", "row--you"} {
		if strings.Contains(visitor, secret) {
			t.Fatalf("visitor's leaderboard contains %q", secret)
		}
	}
	// The anonymous finisher ranks first, above mira's detour, as "You".
	finisher := f.get(t, "/leaderboard", anonymous)
	f.assertNoSpoilers(t, finisher)
	for _, want := range []string{
		"Par " + strconv.Itoa(len(f.path)-1) + ".",
		`<span class="steps-delta">par</span>`, `<span class="steps-delta">+2</span>`,
		`href="/signup">Create an account</a>`,
	} {
		if !strings.Contains(finisher, want) {
			t.Fatalf("finisher's leaderboard lacks %q", want)
		}
	}
	first := finisher[strings.Index(finisher, "<tbody>"):strings.Index(finisher, "mira")]
	if !strings.Contains(first, `<tr class="row--you">`) || !strings.Contains(first, "Anonymous") || !strings.Contains(first, `<span class="you-tag">You</span>`) {
		t.Fatalf("the anonymous finisher's own row is not first: %s", first)
	}
	if signedIn := f.get(t, "/leaderboard", mira); strings.Contains(signedIn, "Create an account") {
		t.Fatal("an account is invited to create one")
	}
}

func (f *fixture) players(t *testing.T) int64 {
	t.Helper()
	var n int64
	if err := f.backend.Database().Read(t.Context(), func(conn *sqlite.Conn) error {
		return sqlitex.Execute(conn, "SELECT count(*) FROM players", &sqlitex.ExecOptions{ResultFunc: func(stmt *sqlite.Stmt) error { n = stmt.ColumnInt64(0); return nil }})
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTodayEmptyStateCreatesNoPlayer(t *testing.T) {
	f := newFixture(t)
	before := f.players(t)
	html := f.get(t, "/leaderboard", "")
	if !strings.Contains(html, "No one has solved Daily #"+strconv.Itoa(f.number)+" yet.") || strings.Contains(html, "<table") {
		t.Fatal("empty leaderboard missing its empty state")
	}
	f.assertNoSpoilers(t, html)
	f.get(t, "/leaderboard/all-time", "")
	if after := f.players(t); after != before {
		t.Fatalf("viewing the leaderboard created %d players", after-before)
	}
}

func TestAllTimeListsAccountsWithFiveGames(t *testing.T) {
	f := newFixture(t)
	jay, bex, ollie := f.signup(t, "jay"), f.signup(t, "bex"), f.signup(t, "ollie")
	f.finish(t, "", 0)                           // finished today, but anonymous
	for i, steps := range []int{5, 6, 5, 5, 5} { // the last five days: avg 0.20, streak 5
		f.pastFinish(t, f.playerID(t, "jay"), f.number-5+i, steps)
	}
	for i, steps := range []int{5, 5, 5, 5, 5, 7} { // five days from #today-9, then yesterday: avg 0.33, best 5
		number := f.number - 9 + i
		if i == 5 {
			number = f.number - 1
		}
		f.pastFinish(t, f.playerID(t, "bex"), number, steps)
	}
	for i := range 4 {
		f.pastFinish(t, f.playerID(t, "ollie"), f.number-4+i, 5)
	}

	html := f.get(t, "/leaderboard/all-time", jay)
	f.assertNoSpoilers(t, html)
	rows := html[strings.Index(html, "<tbody>"):]
	jayRow, bexRow := strings.Index(rows, "jay"), strings.Index(rows, "bex")
	if jayRow < 0 || bexRow < jayRow || strings.Contains(rows, "ollie") || strings.Contains(rows, "Anonymous") {
		t.Fatalf("all-time rows wrong: %s", rows)
	}
	for _, want := range []string{
		`<a href="/leaderboard/all-time" aria-current="page">All-time</a>`,
		`<tr class="row--you">`, "0.20", "0.33",
		"Today’s puzzle counts once you finish it.",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("all-time board lacks %q", want)
		}
	}
	if !strings.Contains(f.get(t, "/leaderboard/all-time", ollie), "Finish 1 more daily game to join the all-time board.") {
		t.Fatal("ollie is not told how many games remain")
	}
	if !strings.Contains(f.get(t, "/leaderboard/all-time", bex), `<tr class="row--you">`) {
		t.Fatal("bex's row is not highlighted")
	}
	if !strings.Contains(f.get(t, "/leaderboard/all-time", ""), "to appear on the all-time board") {
		t.Fatal("a visitor is not invited to create an account")
	}
}

func TestTodayStreamShowsNewFinishers(t *testing.T) {
	f := newFixture(t)
	server := httptest.NewServer(f.handler)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	r, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/leaderboard/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Accept-Encoding", "identity")
	r.Header.Set("Datastar-Request", "true")
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	frames := make(chan string, 16)
	go func() {
		defer close(frames)
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		var lines []string
		for scanner.Scan() {
			if element, ok := strings.CutPrefix(scanner.Text(), "data: elements "); ok {
				lines = append(lines, element)
			} else if scanner.Text() == "" && len(lines) > 0 {
				select {
				case frames <- strings.Join(lines, "\n"):
				case <-ctx.Done():
					return
				}
				lines = nil
			}
		}
	}()
	next := func() string {
		t.Helper()
		select {
		case frame, ok := <-frames:
			if !ok {
				t.Fatal("stream ended")
			}
			return frame
		case <-ctx.Done():
			t.Fatal("no frame before the deadline")
			return ""
		}
	}
	if frame := next(); !strings.Contains(frame, "No one has solved") {
		t.Fatal("first frame is not the empty leaderboard")
	}
	f.finish(t, "", 0)
	frame := next()
	for !strings.Contains(frame, "Anonymous") {
		frame = next()
	}
	f.assertNoSpoilers(t, frame)
}
