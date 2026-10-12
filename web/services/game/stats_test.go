package game_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"poople/app"
	"poople/ladder"
	"poople/web/services/game"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// account gives a new player an account, with a picture when avatar is set.
func (f *fixture) account(t *testing.T, name string, avatar bool) int64 {
	t.Helper()
	player := f.player(t)
	var hash any
	if avatar {
		hash = name + "-hash"
	}
	if err := f.backend.Database().Write(t.Context(), func(conn *sqlite.Conn) error {
		return sqlitex.Execute(conn, "INSERT INTO accounts(player_id, username, password_hash, avatar, avatar_hash, created_at) VALUES (?, ?, 'x', CASE WHEN ? IS NULL THEN NULL ELSE x'00' END, ?, 0)",
			&sqlitex.ExecOptions{Args: []any{player, name, hash, hash}})
	}); err != nil {
		t.Fatal(err)
	}
	return player
}

// solve finishes the puzzle for player from at, one guess every gap. Each
// detour steps to a neighbour of the start word and back first, adding two
// steps over par.
func (f *fixture) solve(t *testing.T, player int64, puzzle game.Puzzle, at time.Time, gap time.Duration, detours int) {
	t.Helper()
	data := csw(t)
	path, ok := data.ShortestPath(puzzle.StartWord)
	if !ok {
		t.Fatalf("no ladder from %q", puzzle.StartWord)
	}
	var words []string
	for range detours {
		words = append(words, data.Neighbors(puzzle.StartWord)[0], puzzle.StartWord)
	}
	for i, word := range append(words, path[1:]...) {
		f.clock.Set(at.Add(time.Duration(i) * gap))
		f.guess(t, player, puzzle.ID, word, "")
	}
}

// dailyOn opens daily puzzle #1+offset at 09:00 UTC that day.
func (f *fixture) dailyOn(t *testing.T, offset int) (game.Puzzle, time.Time) {
	t.Helper()
	at := day1.AddDate(0, 0, offset)
	f.clock.Set(at)
	return f.daily(t), at
}

func (f *fixture) stats(t *testing.T, player int64) game.Stats {
	t.Helper()
	stats, err := f.games.Stats(t.Context(), player, f.daily(t))
	if err != nil {
		t.Fatal(err)
	}
	return stats
}

func TestStandingsRankEveryFinisherWithoutGaps(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	puzzle := f.daily(t)
	slow, fast, long, unfinished := f.account(t, "slow", true), f.account(t, "fast", false), f.account(t, "long", false), f.account(t, "unfinished", false)
	anonymous, viewer := f.player(t), f.player(t)
	f.solve(t, slow, puzzle, day1, 2*time.Minute, 0)                     // 5 steps in 8 minutes
	f.solve(t, fast, puzzle, day1.Add(time.Hour), time.Minute, 0)        // 5 steps in 4 minutes
	f.solve(t, anonymous, puzzle, day1.Add(2*time.Hour), time.Minute, 0) // ties fast, finishing later
	f.solve(t, long, puzzle, day1.Add(3*time.Hour), time.Minute, 1)      // 7 steps in 6 minutes
	f.solve(t, viewer, puzzle, day1.Add(4*time.Hour), 2*time.Minute, 1)  // 7 steps in 12 minutes
	path, _ := csw(t).ShortestPath(puzzle.StartWord)
	f.guess(t, unfinished, puzzle.ID, path[1], "")

	got, err := f.games.Standings(t.Context(), puzzle.ID, viewer)
	if err != nil {
		t.Fatal(err)
	}
	want := []game.Standing{
		{Rank: 1, PlayerID: fast, Username: "fast", Steps: 5, SolveTime: 4 * time.Minute},
		{Rank: 1, PlayerID: anonymous, Steps: 5, SolveTime: 4 * time.Minute},
		{Rank: 2, PlayerID: slow, Username: "slow", AvatarHash: "slow-hash", Steps: 5, SolveTime: 8 * time.Minute},
		{Rank: 3, PlayerID: long, Username: "long", Steps: 7, SolveTime: 6 * time.Minute},
		{Rank: 4, PlayerID: viewer, Steps: 7, SolveTime: 12 * time.Minute, You: true},
	}
	if len(got.Rows) != len(want) {
		t.Fatalf("standings rows = %+v, want %+v", got.Rows, want)
	}
	for i, row := range got.Rows {
		if (row.AccountID != 0) != (want[i].Username != "") {
			t.Fatalf("row %d account id = %d", i, row.AccountID)
		}
		want[i].AccountID = row.AccountID
	}
	if !slices.Equal(got.Rows, want) || got.Finishers != 5 || got.ViewerAfterGap || got.Unlisted() != 0 {
		t.Fatalf("standings for the anonymous viewer = %+v, want rows %+v", got, want)
	}
	byAccount, err := f.games.Standings(t.Context(), puzzle.ID, fast)
	if err != nil {
		t.Fatal(err)
	}
	if len(byAccount.Rows) != 5 || !byAccount.Rows[0].You || byAccount.Rows[4].You {
		t.Fatalf("standings for an account = %+v", byAccount)
	}
	if visitor, err := f.games.Standings(t.Context(), puzzle.ID, 0); err != nil || len(visitor.Rows) != 5 || slices.ContainsFunc(visitor.Rows, func(s game.Standing) bool { return s.You }) {
		t.Fatalf("standings for a visitor = %+v, %v", visitor, err)
	}
}

func TestStandingsListTheTopTwentyThenTheViewer(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	puzzle := f.daily(t)
	players := make([]int64, game.StandingsShown+3)
	for i := range players {
		players[i] = f.player(t)
		// Later players take longer, so players[i] finishes in place i.
		f.solve(t, players[i], puzzle, day1.Add(time.Duration(i)*10*time.Minute), time.Duration(i+1)*time.Second, 0)
	}
	for _, c := range []struct {
		place   int
		rows    int
		lastYou bool
		gap     bool
	}{
		{place: 4, rows: game.StandingsShown},
		{place: game.StandingsShown - 1, rows: game.StandingsShown, lastYou: true},
		{place: game.StandingsShown, rows: game.StandingsShown + 1, lastYou: true},
		{place: len(players) - 1, rows: game.StandingsShown + 1, lastYou: true, gap: true},
	} {
		got, err := f.games.Standings(t.Context(), puzzle.ID, players[c.place])
		if err != nil {
			t.Fatal(err)
		}
		last := got.Rows[len(got.Rows)-1]
		if len(got.Rows) != c.rows || last.You != c.lastYou || got.ViewerAfterGap != c.gap || got.Finishers != len(players) {
			t.Fatalf("viewer in place %d: %d rows, last %+v, gap %v", c.place, len(got.Rows), last, got.ViewerAfterGap)
		}
		you := slices.IndexFunc(got.Rows, func(s game.Standing) bool { return s.You })
		if you < 0 || got.Rows[you].Rank != c.place+1 || got.Rows[you].PlayerID != players[c.place] {
			t.Fatalf("viewer in place %d has row %d: %+v", c.place, you, got.Rows)
		}
		for i, row := range got.Rows[:game.StandingsShown] {
			if row.Rank != i+1 {
				t.Fatalf("row %d rank = %d", i, row.Rank)
			}
		}
	}
}

func TestStatsCountDailyFinishesAndStreaks(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	player, other := f.player(t), f.player(t)
	// Finish #1-#3 (+0, +2, +6), skip #4, finish #5 and #6 (+0, +4),
	// start #7 without finishing.
	extra := map[int]int{0: 0, 1: 1, 2: 3, 4: 0, 5: 2}
	for _, offset := range []int{0, 1, 2, 4, 5} {
		puzzle, at := f.dailyOn(t, offset)
		f.solve(t, player, puzzle, at, time.Minute, extra[offset])
	}
	puzzle, at := f.dailyOn(t, 6)
	path, _ := csw(t).ShortestPath(puzzle.StartWord)
	f.guess(t, player, puzzle.ID, path[1], "")
	f.guess(t, player, puzzle.ID, path[2], "")
	// An unlimited game, even finished, never counts.
	unlimited, err := f.games.StartUnlimited(t.Context(), player)
	if err != nil {
		t.Fatal(err)
	}
	f.solve(t, player, unlimited, at, time.Second, 0)

	got := f.stats(t, player)
	want := game.Stats{
		Played: 6, Wins: 5, ExtraSteps: 0 + 2 + 6 + 0 + 4,
		CurrentStreak: 2, BestStreak: 3, // today's unfinished game keeps yesterday's streak
		Distribution: game.Distribution{2, 0, 1, 0, 1, 0, 1},
	}
	if got != want {
		t.Fatalf("stats during #7 = %+v, want %+v", got, want)
	}
	if avg, ok := got.AverageExtra(); !ok || avg != 2.4 {
		t.Fatalf("average extra = %v, %v", avg, ok)
	}

	// Finishing today extends the streak and compares against today's other finishers.
	f.solve(t, other, puzzle, at, time.Second, 1)
	for i, word := range path[3:] { // two guesses already made
		f.clock.Set(at.Add(time.Hour + time.Duration(i)*time.Second))
		f.guess(t, player, puzzle.ID, word, "")
	}
	got = f.stats(t, player)
	if got.Wins != 6 || got.CurrentStreak != 3 || got.BestStreak != 3 || got.Today == nil || got.Today.Extra() != 0 {
		t.Fatalf("stats after finishing #7 = %+v", got)
	}
	if got.Everyone != (game.Distribution{1, 0, 1, 0, 0, 0, 0}) || got.Better != 0 || got.Tied != 0 || got.Worse != 1 {
		t.Fatalf("everyone today = %+v", got)
	}

	// Two days on, the streak has ended but the best stays.
	f.dailyOn(t, 8)
	if got := f.stats(t, player); got.CurrentStreak != 0 || got.BestStreak != 3 || got.Today != nil || got.Everyone.Total() != 0 {
		t.Fatalf("stats two days later = %+v", got)
	}
	// A visitor who has never played has an empty record.
	if got := f.stats(t, 0); got != (game.Stats{}) {
		t.Fatalf("visitor stats = %+v", got)
	}
}

func TestStatsHideEveryoneUntilThePlayerFinishes(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	player, other := f.player(t), f.player(t)
	puzzle := f.daily(t)
	f.solve(t, other, puzzle, day1, time.Second, 0)
	path, _ := csw(t).ShortestPath(puzzle.StartWord)
	f.guess(t, player, puzzle.ID, path[1], "")
	if got := f.stats(t, player); got.Today != nil || got.Everyone.Total() != 0 || got.Better+got.Tied+got.Worse != 0 {
		t.Fatalf("unfinished player sees today's results: %+v", got)
	}
}

func TestStatsUseThePuzzlesFrozenPar(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	player := f.player(t)
	pave := f.daily(t)
	f.solve(t, player, pave, day1, time.Second, 1) // par 5, 7 steps
	// A new word list without PAVE leaves #1's par alone.
	fixture, err := ladder.Parse([]byte("pads\npals\npods\npoop\npoos\n"), []byte("pals\n"), []byte("pals\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.LoadWords(t.Context(), f.backend.Database(), fixture); err != nil {
		t.Fatal(err)
	}
	pals, at := f.dailyOn(t, 1)
	for i, word := range []string{"pads", "pods", "poos", "poop"} { // par 4
		f.clock.Set(at.Add(time.Duration(i) * time.Second))
		f.guess(t, player, pals.ID, word, "")
	}
	got := f.stats(t, player)
	if got.Wins != 2 || got.ExtraSteps != 2 || got.Distribution != (game.Distribution{1, 0, 1}) {
		t.Fatalf("stats across word lists = %+v", got)
	}
}

func TestAllTimeRanksAccountsWithEnoughGames(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ann, bea, cat, dan, eve := f.account(t, "ann", false), f.account(t, "Bea", true), f.account(t, "cat", false), f.account(t, "dan", false), f.account(t, "eve", false)
	anonymous := f.player(t)
	// Days #1-#6; extra steps are 2 per detour.
	plays := map[int64][]int{ // detours by day offset, -1 for no finish
		ann:       {0, 0, 1, 0, 0, 0},   // avg 2/6, best 6, current 6
		bea:       {1, 0, 0, -1, 0, 0},  // avg 2/5, best 3, current 2
		cat:       {0, 1, 0, 0, 0, -1},  // avg 2/5, best 5, current 5 (yesterday)
		dan:       {0, 0, 0, 0, -1, -1}, // 4 games: unlisted
		eve:       {0, 0, 1, -1, 0, 0},  // avg 2/5, best 3, current 2: ties Bea
		anonymous: {0, 0, 0, 0, 0, 0},   // never listed
	}
	for offset := range 6 {
		puzzle, at := f.dailyOn(t, offset)
		for player, detours := range plays {
			if detours[offset] >= 0 {
				f.solve(t, player, puzzle, at, time.Second, detours[offset])
			}
		}
	}
	// It is now #6: ann, Bea, eve and the anonymous player have finished it.
	today := f.daily(t)
	board, err := f.games.AllTime(t.Context(), today, bea)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		rank             int
		name             string
		current, best, n int
		you              bool
	}
	var got []row
	for _, s := range board.Rows {
		got = append(got, row{s.Rank, s.Username, s.CurrentStreak, s.BestStreak, s.Games, s.You})
	}
	want := []row{
		{1, "ann", 6, 6, 6, false},
		{2, "cat", 5, 5, 5, false},
		{3, "Bea", 2, 3, 5, true},
		{3, "eve", 2, 3, 5, false},
	}
	if !slices.Equal(got, want) || !board.TodayCounted || board.ViewerGames != 5 {
		t.Fatalf("all-time for Bea = %+v (counted today %v), want %+v", got, board.TodayCounted, want)
	}
	if board.Rows[2].AvatarHash != "Bea-hash" || board.Rows[0].AverageExtra() != 2.0/6 {
		t.Fatalf("rows = %+v", board.Rows)
	}

	// cat and dan have not finished today, so today's games are left out of
	// averages and counts until they do; streaks still count today.
	forCat, err := f.games.AllTime(t.Context(), today, cat)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range forCat.Rows {
		names = append(names, s.Username)
	}
	if forCat.TodayCounted || strings.Join(names, " ") != "ann cat" || forCat.Rows[0].Games != 5 || forCat.Rows[0].CurrentStreak != 6 {
		t.Fatalf("all-time for cat = %+v", forCat)
	}
	forDan, err := f.games.AllTime(t.Context(), today, dan)
	if err != nil || slices.ContainsFunc(forDan.Rows, func(s game.AllTimeStanding) bool { return s.You }) || forDan.ViewerGames != 4 {
		t.Fatalf("all-time for dan = %+v, %v", forDan, err)
	}
}
