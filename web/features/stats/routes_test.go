package stats_test

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
	handler, _, err := web.NewApp(t.Context(), backend, false, 1000)
	if err != nil {
		t.Fatal(err)
	}
	data, err := ladder.Load()
	if err != nil {
		t.Fatal(err)
	}
	number := ladder.DailyNumber(time.Now())
	path, _ := data.ShortestPath(data.DailyStart(number))
	return &fixture{backend, handler, number, path, data.DailyStart(number + 1)}
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

// guess posts word as the browser does and returns the session, new when there was none.
func (f *fixture) guess(t *testing.T, session, word string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"guess": word})
	r := httptest.NewRequest("POST", "/play/guess", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Datastar-Request", "true")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if session != "" {
		r.AddCookie(&http.Cookie{Name: identity.SessionCookie, Value: session})
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if session != "" {
		if w.Code != http.StatusNoContent {
			t.Fatalf("guess %q status=%d", word, w.Code)
		}
		return session
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == identity.SessionCookie {
			return cookie.Value
		}
	}
	t.Fatalf("first guess set no session: %d %s", w.Code, w.Body)
	return ""
}

// finish plays today's shortest ladder after detours trips to a neighbour of
// the start word and back, from a fresh game.
func (f *fixture) finish(t *testing.T, session string, detours int) string {
	t.Helper()
	data, _ := ladder.Load()
	var words []string
	for range detours {
		words = append(words, data.Neighbors(f.path[0])[0], f.path[0])
	}
	for _, word := range append(words, f.path[1:]...) {
		session = f.guess(t, session, word)
	}
	return session
}

func (f *fixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if err := f.backend.Database().Write(t.Context(), func(conn *sqlite.Conn) error {
		return sqlitex.Execute(conn, query, &sqlitex.ExecOptions{Args: args})
	}); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) count(t *testing.T, query string) int64 {
	t.Helper()
	var n int64
	if err := f.backend.Database().Read(t.Context(), func(conn *sqlite.Conn) error {
		return sqlitex.Execute(conn, query, &sqlitex.ExecOptions{ResultFunc: func(stmt *sqlite.Stmt) error { n = stmt.ColumnInt64(0); return nil }})
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// pastGame records a game of the session's player on a puzzle before today
// with par 5; kind is 'daily' or 'unlimited' and finished games take steps guesses.
func (f *fixture) pastGame(t *testing.T, kind string, number, steps int, finished bool) {
	t.Helper()
	player := f.count(t, "SELECT max(id) FROM players")
	var finishedAt any
	if finished {
		finishedAt = 60000
	}
	f.exec(t, "INSERT INTO puzzles (kind, number, list_id, start_word, par, created_at) VALUES (?, CASE WHEN ? = 'daily' THEN ? END, 1, 'fuse', 5, 0)", kind, kind, number)
	f.exec(t, "INSERT INTO games (puzzle_id, player_id, started_at, finished_at) VALUES ((SELECT max(id) FROM puzzles), ?, 0, ?)", player, finishedAt)
	for i := range steps {
		f.exec(t, "INSERT INTO guesses (game_id, position, word, created_at) VALUES ((SELECT max(id) FROM games), ?, 'fuss', 0)", i+1)
	}
}

// assertNoSpoilers fails when html holds a word of today's ladder other than
// the start word and POOP, or tomorrow's start word.
func (f *fixture) assertNoSpoilers(t *testing.T, html string) {
	t.Helper()
	for _, word := range append([]string{f.tomorrow}, f.path[1:len(f.path)-1]...) {
		if strings.Contains(html, strings.ToUpper(word)) {
			t.Fatalf("statistics contain %q", word)
		}
	}
}

func TestVisitorSeesEmptyStatsWithoutBecomingAPlayer(t *testing.T) {
	f := newFixture(t)
	html := f.get(t, "/stats", "")
	for _, want := range []string{
		`aria-label="Statistics" aria-current="page"`,
		"<dt>Played</dt><dd>0</dd>", "<dt>Wins</dt><dd>0</dd>", "–",
		"No solves yet.", "Finish today’s puzzle to see how everyone did.",
		"Everyone #" + strconv.Itoa(f.number), "/stats/state",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("visitor's statistics lack %q", want)
		}
	}
	if strings.Contains(html, "dist-list") {
		t.Fatal("visitor's statistics draw a distribution")
	}
	if got := f.count(t, "SELECT count(*) FROM players"); got != 0 {
		t.Fatalf("viewing statistics created %d players", got)
	}
}

func TestStatsCountDailyGamesAndRevealEveryoneOnlyAfterFinishing(t *testing.T) {
	f := newFixture(t)
	// Another player finishes today first, 2 over par.
	f.finish(t, "", 1)
	session := f.guess(t, "", f.path[1])
	f.pastGame(t, "daily", f.number-3, 5, true)  // +0, before a gap
	f.pastGame(t, "daily", f.number-1, 6, true)  // +1, yesterday
	f.pastGame(t, "daily", f.number-2, 0, false) // played, not won
	f.pastGame(t, "unlimited", 0, 5, true)       // never counts

	html := f.get(t, "/stats", session)
	f.assertNoSpoilers(t, html)
	for _, want := range []string{
		"<dt>Played</dt><dd>4</dd>", "<dt>Wins</dt><dd>2</dd>", "0.50",
		"<dt>Current streak</dt><dd>1</dd>", "<dt>Best streak</dt><dd>1</dd>",
		`style="--share: 100%;"`, "Based on your 2 solves.",
		"Finish today’s puzzle to see how everyone did.",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("statistics before finishing lack %q", want)
		}
	}
	for _, secret := range []string{"Everyone today by extra steps", "dist-row--today", "You finished", "compare-title"} {
		if strings.Contains(html, secret) {
			t.Fatalf("statistics before finishing contain %q", secret)
		}
	}

	for _, word := range f.path[2:] {
		f.guess(t, session, word)
	}
	html = f.get(t, "/stats", session)
	f.assertNoSpoilers(t, html)
	for _, want := range []string{
		"<dt>Played</dt><dd>4</dd>", "<dt>Wins</dt><dd>3</dd>", "0.33",
		"<dt>Current streak</dt><dd>2</dd>",
		"Everyone today by extra steps", "2 players solved #" + strconv.Itoa(f.number) + ". Your +0 is outlined.",
		"Based on your 3 solves. Today is outlined.",
		"You finished on par", "Of the 1 other player who solved #" + strconv.Itoa(f.number) + ", 1 used more steps.",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("statistics after finishing lack %q", want)
		}
	}
}

func TestStatsStreamFollowsThePlayersGames(t *testing.T) {
	f := newFixture(t)
	session := f.guess(t, "", f.path[1])
	server := httptest.NewServer(f.handler)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	r, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/stats/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Accept-Encoding", "identity")
	r.Header.Set("Datastar-Request", "true")
	r.AddCookie(&http.Cookie{Name: identity.SessionCookie, Value: session})
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
	if frame := next(); !strings.Contains(frame, "<dt>Played</dt><dd>1</dd>") || !strings.Contains(frame, "<dt>Wins</dt><dd>0</dd>") {
		t.Fatal("first frame lacks the game in progress")
	}
	for _, word := range f.path[2:] {
		f.guess(t, session, word)
	}
	frame := next()
	for !strings.Contains(frame, "<dt>Wins</dt><dd>1</dd>") {
		frame = next()
	}
	f.assertNoSpoilers(t, frame)
}
