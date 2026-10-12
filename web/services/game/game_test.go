package game_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"poople/app"
	"poople/ladder"
	"poople/web/services/game"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// day423 is during daily puzzle #423 (FUSE, par 5 in CSW24).
var day423 = time.Date(2026, time.October, 11, 9, 0, 0, 0, time.UTC)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

type fixture struct {
	backend *app.Backend
	games   *game.Service
	clock   *clock
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	backend, err := app.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	c := &clock{now: day423}
	return &fixture{backend: backend, games: game.New(backend, c.Now), clock: c}
}

func (f *fixture) player(t *testing.T) int64 {
	t.Helper()
	var id int64
	if err := f.backend.Database().Write(t.Context(), func(conn *sqlite.Conn) error {
		if err := sqlitex.Execute(conn, "INSERT INTO players(created_at) VALUES (0)", nil); err != nil {
			return err
		}
		id = conn.LastInsertRowID()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *fixture) count(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := f.backend.Database().Read(t.Context(), func(conn *sqlite.Conn) error {
		return sqlitex.Execute(conn, query, &sqlitex.ExecOptions{Args: args, ResultFunc: func(stmt *sqlite.Stmt) error {
			n = stmt.ColumnInt64(0)
			return nil
		}})
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *fixture) daily(t *testing.T) game.Puzzle {
	t.Helper()
	puzzle, err := f.games.DailyPuzzle(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return puzzle
}

func (f *fixture) guess(t *testing.T, player, puzzle int64, word string, want game.Rejection) {
	t.Helper()
	got, err := f.games.Guess(t.Context(), player, puzzle, word)
	if err != nil {
		t.Fatalf("Guess(%q): %v", word, err)
	}
	if got != want {
		t.Fatalf("Guess(%q) rejection = %q, want %q", word, got, want)
	}
}

func (f *fixture) view(t *testing.T, player, puzzle int64) game.Game {
	t.Helper()
	view, err := f.games.GameView(t.Context(), player, puzzle)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func csw(t *testing.T) *ladder.Data {
	t.Helper()
	data, err := ladder.Load()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDailyPuzzleFollowsCalendarAndChangesAt0800UTC(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	today := f.daily(t)
	if today.Kind != game.Daily || today.Number != 423 || today.StartWord != "fuse" {
		t.Fatalf("daily at %v = %+v, want #423 fuse", day423, today)
	}
	if got := f.count(t, "SELECT par FROM puzzles WHERE id = ?", today.ID); got != 5 {
		t.Fatalf("frozen par = %d, want 5", got)
	}
	f.clock.Set(time.Date(2026, time.October, 12, 7, 59, 59, 0, time.UTC))
	if again := f.daily(t); again != today {
		t.Fatalf("07:59:59 next day = %+v, want %+v", again, today)
	}
	f.clock.Set(time.Date(2026, time.October, 12, 8, 0, 0, 0, time.UTC))
	if next := f.daily(t); next.Number != 424 || next.ID == today.ID {
		t.Fatalf("08:00 next day = %+v, want a new #424", next)
	}
	f.clock.Set(time.Date(2025, time.August, 14, 8, 0, 0, 0, time.UTC))
	if first := f.daily(t); first.Number != 0 || first.StartWord != "dawn" {
		t.Fatalf("daily #0 = %+v, want dawn", first)
	}
	if got := f.count(t, "SELECT count(*) FROM puzzles"); got != 3 {
		t.Fatalf("puzzles = %d, want one per opened day", got)
	}
}

func TestGuessRejectionsAreStoredAndSequenced(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	player, puzzle := f.player(t), f.daily(t).ID
	path, _ := csw(t).ShortestPath("fuse")
	for i, c := range []struct {
		word string
		want game.Rejection
	}{
		{"fus", game.Incomplete},
		{"", game.Incomplete},
		{"boop", game.NotInList},
		{"fusee", game.NotInList},
		{"poop", game.NotOneLetter},
	} {
		f.guess(t, player, puzzle, c.word, c.want)
		view := f.view(t, player, puzzle)
		if view.Rejection != c.want || view.RejectionSeq != int64(i+1) || view.Steps != 0 {
			t.Fatalf("after %q view = %+v", c.word, view)
		}
	}
	f.guess(t, player, puzzle, path[1], "")
	view := f.view(t, player, puzzle)
	if view.Rejection != "" || view.RejectionSeq != 5 || !slices.Equal(view.Ladder, path[:2]) {
		t.Fatalf("after accepted guess view = %+v", view)
	}
	// Two steps from the latest guess on a shortest path, so never one letter away.
	f.guess(t, player, puzzle, path[3], game.NotOneLetter)
	if view := f.view(t, player, puzzle); view.RejectionSeq != 6 || len(view.Ladder) != 2 {
		t.Fatalf("after rejected guess view = %+v", view)
	}
}

func TestFinishingRevealsParAndSolutionOnlyAfterward(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	player, other := f.player(t), f.player(t)
	puzzle := f.daily(t).ID
	path, _ := csw(t).ShortestPath("fuse")
	f.guess(t, player, puzzle, "zzzz", game.NotInList) // a rejection does not start the clock
	for i, word := range path[1:] {
		f.clock.Set(day423.Add(time.Duration(i+1) * time.Minute))
		view := f.view(t, player, puzzle)
		if view.Finished || view.Par != 0 || view.Solution != nil {
			t.Fatalf("unfinished view leaks the answer: %+v", view)
		}
		f.guess(t, player, puzzle, strings.ToUpper(word), "") // guesses are case-insensitive
	}
	view := f.view(t, player, puzzle)
	if !view.Finished || view.Steps != 5 || view.Par != 5 || !slices.Equal(view.Solution, path) || !slices.Equal(view.Ladder, path) {
		t.Fatalf("finished view = %+v, want 5 steps, par 5, solution %v", view, path)
	}
	if view.SolveTime != 4*time.Minute {
		t.Fatalf("solve time = %v, want first to last guess, 4m", view.SolveTime)
	}
	if _, err := f.games.Guess(t.Context(), player, puzzle, "poos"); !errors.Is(err, game.ErrGameFinished) {
		t.Fatalf("guess after finishing = %v", err)
	}
	if again := f.view(t, player, puzzle); again.RejectionSeq != view.RejectionSeq || len(again.Ladder) != len(view.Ladder) {
		t.Fatalf("refused guess changed the game: %+v", again)
	}
	if theirs := f.view(t, other, puzzle); theirs.Finished || theirs.Par != 0 || theirs.Solution != nil || !slices.Equal(theirs.Ladder, []string{"fuse"}) {
		t.Fatalf("another player's view = %+v", theirs)
	}
}

func TestNewWordListLeavesExistingPuzzlesFrozen(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	player := f.player(t)
	today := f.daily(t)
	fixture, err := ladder.Parse([]byte("pads\npals\npods\npoop\npoos\n"), []byte("pals\n"), []byte("pals\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.LoadWords(t.Context(), f.backend.Database(), fixture); err != nil {
		t.Fatal(err)
	}
	if again := f.daily(t); again != today {
		t.Fatalf("today's puzzle after a new list = %+v, want %+v", again, today)
	}
	if got := f.count(t, "SELECT count(*) FROM puzzles WHERE id = ? AND list_id = 1 AND start_word = 'fuse' AND par = 5", today.ID); got != 1 {
		t.Fatal("existing puzzle changed")
	}
	// Today's puzzle still validates and solves against its own list.
	path, _ := csw(t).ShortestPath("fuse")
	for _, word := range path[1:] {
		f.guess(t, player, today.ID, word, "")
	}
	if view := f.view(t, player, today.ID); view.Par != 5 || !slices.Equal(view.Solution, path) {
		t.Fatalf("old puzzle view = %+v", view)
	}
	f.clock.Set(day423.Add(24 * time.Hour))
	tomorrow := f.daily(t)
	if tomorrow.StartWord != "pals" {
		t.Fatalf("tomorrow = %+v, want pals from the new list", tomorrow)
	}
	f.guess(t, player, tomorrow.ID, "pale", game.NotInList) // in CSW24, not in the new list
	for _, word := range []string{"pads", "pods", "poos", "poop"} {
		f.guess(t, player, tomorrow.ID, word, "")
	}
	if view := f.view(t, player, tomorrow.ID); view.Par != 4 || !slices.Equal(view.Solution, []string{"pals", "pads", "pods", "poos", "poop"}) {
		t.Fatalf("new puzzle view = %+v", view)
	}
}

func TestUnlimitedPuzzlesComeFromThePoolAndBelongToOnePlayer(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	data := csw(t)
	alice, bob := f.player(t), f.player(t)
	if _, ok, err := f.games.CurrentUnlimited(t.Context(), alice); err != nil || ok {
		t.Fatalf("current before starting = %v, %v", ok, err)
	}
	var last game.Puzzle
	for range 20 {
		puzzle, err := f.games.StartUnlimited(t.Context(), alice)
		if err != nil {
			t.Fatal(err)
		}
		if puzzle.Kind != game.Unlimited || !slices.Contains(data.Unlimited(), puzzle.StartWord) {
			t.Fatalf("unlimited puzzle %+v not from the pool", puzzle)
		}
		dist, _ := data.Distance(puzzle.StartWord)
		if got := f.count(t, "SELECT par FROM puzzles WHERE id = ?", puzzle.ID); got != int64(dist) {
			t.Fatalf("par %d, want distance %d", got, dist)
		}
		last = puzzle
	}
	if current, ok, err := f.games.CurrentUnlimited(t.Context(), alice); err != nil || !ok || current != last {
		t.Fatalf("alice's current = %+v, %v, %v; want %+v", current, ok, err, last)
	}
	if _, ok, err := f.games.CurrentUnlimited(t.Context(), bob); err != nil || ok {
		t.Fatalf("bob has an unlimited game: %v, %v", ok, err)
	}
	if _, err := f.games.Guess(t.Context(), bob, last.ID, "zzzz"); !errors.Is(err, game.ErrPuzzleNotFound) {
		t.Fatalf("bob guessing on alice's unlimited puzzle = %v", err)
	}
	path, _ := data.ShortestPath(last.StartWord)
	f.guess(t, alice, last.ID, path[1], "")
	if view := f.view(t, alice, last.ID); !slices.Equal(view.Ladder, path[:2]) {
		t.Fatalf("alice's unlimited ladder = %v", view.Ladder)
	}
	if _, err := f.games.Guess(t.Context(), alice, 1_000_000, "fuse"); !errors.Is(err, game.ErrPuzzleNotFound) {
		t.Fatalf("guess on a missing puzzle = %v", err)
	}
}

func TestMergePlayersKeepsTheAccountsGames(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	anonymous, account := f.player(t), f.player(t)
	data := csw(t)
	yesterday := f.daily(t)
	yesterdayPath, _ := data.ShortestPath(yesterday.StartWord)
	f.guess(t, anonymous, yesterday.ID, yesterdayPath[1], "")
	// The account only had a guess rejected: it has a game row but no ladder,
	// which must not displace the anonymous player's progress.
	f.guess(t, account, yesterday.ID, "zzzz", game.NotInList)
	f.clock.Set(day423.Add(24 * time.Hour))
	today := f.daily(t)
	todayPath, _ := data.ShortestPath(today.StartWord)
	f.guess(t, anonymous, today.ID, todayPath[1], "")
	f.guess(t, anonymous, today.ID, todayPath[2], "")
	f.guess(t, account, today.ID, todayPath[1], "")

	waits := map[string]<-chan struct{}{
		"yesterday's puzzle": f.games.PuzzleChanges(yesterday.ID),
		"today's puzzle":     f.games.PuzzleChanges(today.ID),
		"the account":        f.games.PlayerChanges(account),
		"the anonymous":      f.games.PlayerChanges(anonymous),
	}
	var committed func()
	if err := f.backend.Database().Write(t.Context(), func(conn *sqlite.Conn) (err error) {
		if committed, err = f.games.MergePlayers(conn, anonymous, account); err != nil {
			return err
		}
		return sqlitex.Execute(conn, "DELETE FROM players WHERE id = ?", &sqlitex.ExecOptions{Args: []any{anonymous}})
	}); err != nil {
		t.Fatal(err)
	}
	for name, wait := range waits {
		select {
		case <-wait:
			t.Fatalf("%s woke before the commit hook ran", name)
		default:
		}
	}
	committed()
	for name, wait := range waits {
		select {
		case <-wait:
		default:
			t.Fatalf("merge did not wake views of %s", name)
		}
	}
	if view := f.view(t, account, yesterday.ID); !slices.Equal(view.Ladder, yesterdayPath[:2]) {
		t.Fatalf("moved game ladder = %v", view.Ladder)
	}
	if view := f.view(t, account, today.ID); !slices.Equal(view.Ladder, todayPath[:2]) {
		t.Fatalf("account's own game was replaced: %v", view.Ladder)
	}
	if got := f.count(t, "SELECT count(*) FROM games WHERE player_id = ?", anonymous); got != 0 {
		t.Fatalf("anonymous games left = %d", got)
	}
	if got := f.count(t, "SELECT count(*) FROM games WHERE player_id = ?", account); got != 2 {
		t.Fatalf("account games = %d, want 2", got)
	}
	if got := f.count(t, "SELECT count(*) FROM guesses"); got != 2 {
		t.Fatalf("guesses = %d, want the duplicate game's guesses dropped", got)
	}
}

func TestConcurrentGuessesKeepEachLadder(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	puzzle := f.daily(t).ID
	path, _ := csw(t).ShortestPath("fuse")
	players := []int64{f.player(t), f.player(t)}
	changes := f.games.PuzzleChanges(puzzle)
	var wg sync.WaitGroup
	for _, player := range players {
		wg.Go(func() {
			for _, word := range path[1:] {
				// Two identical concurrent guesses, as from a double Enter: one is
				// accepted; the other repeats the last word and changes nothing, or
				// finds the game already finished.
				var both sync.WaitGroup
				for range 2 {
					both.Go(func() {
						rejection, err := f.games.Guess(context.Background(), player, puzzle, word)
						if err != nil && !(word == ladder.Target && errors.Is(err, game.ErrGameFinished)) {
							t.Error(err)
						}
						if rejection != "" {
							t.Errorf("player %d: copy of %q rejected: %s", player, word, rejection)
						}
					})
				}
				both.Wait()
			}
		})
	}
	wg.Wait()
	select {
	case <-changes:
	default:
		t.Fatal("puzzle topic not notified")
	}
	for _, player := range players {
		view := f.view(t, player, puzzle)
		if !view.Finished || !slices.Equal(view.Ladder, path) || view.RejectionSeq != 0 {
			t.Fatalf("player %d view = %+v", player, view)
		}
	}
}

func TestRepeatingTheLastWordChangesNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	player, puzzle := f.player(t), f.daily(t).ID
	path, _ := csw(t).ShortestPath("fuse")
	f.guess(t, player, puzzle, "zzzz", game.NotInList)
	unchanged := func(word string) {
		t.Helper()
		changes := f.games.PuzzleChanges(puzzle)
		f.guess(t, player, puzzle, word, "")
		select {
		case <-changes:
			t.Fatalf("repeating %q notified the puzzle topic", word)
		default:
		}
	}
	unchanged("FUSE") // the start word is the last word until a guess lands
	f.guess(t, player, puzzle, path[1], "")
	unchanged(path[1])
	view := f.view(t, player, puzzle)
	if !slices.Equal(view.Ladder, path[:2]) || view.RejectionSeq != 1 || view.Rejection != "" {
		t.Fatalf("after repeating %q view = %+v", path[1], view)
	}
}

func TestGuessesOnYesterdaysPuzzleAreRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	player := f.player(t)
	yesterday := f.daily(t)
	path, _ := csw(t).ShortestPath(yesterday.StartWord)
	f.guess(t, player, yesterday.ID, path[1], "")
	f.clock.Set(time.Date(2026, time.October, 12, 8, 0, 0, 0, time.UTC))
	if _, err := f.games.Guess(t.Context(), player, yesterday.ID, path[2]); !errors.Is(err, game.ErrPuzzleClosed) {
		t.Fatalf("guess after the next puzzle opened = %v, want ErrPuzzleClosed", err)
	}
	if view := f.view(t, player, yesterday.ID); !slices.Equal(view.Ladder, path[:2]) || view.RejectionSeq != 0 {
		t.Fatalf("closed puzzle changed: %+v", view)
	}
	today := f.daily(t)
	todayPath, _ := csw(t).ShortestPath(today.StartWord)
	f.guess(t, player, today.ID, todayPath[1], "")
}
