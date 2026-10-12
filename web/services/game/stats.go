package game

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"poople/store/zz"

	"zombiezen.com/go/sqlite"
)

// Statistics count daily puzzles only; unlimited games never count. Extra
// steps are steps over the par the puzzle froze when it was first opened.

// Distribution counts finished games by extra steps over par: index 0 to 5
// for that many extra steps, index 6 for six or more.
type Distribution [7]int

// DistributionBucket is the Distribution index for extra steps over par.
func DistributionBucket(extra int) int { return min(max(extra, 0), len(Distribution{})-1) }

// Total is the number of games counted.
func (d Distribution) Total() int {
	total := 0
	for _, n := range d {
		total += n
	}
	return total
}

// Result is one finished game against its puzzle's frozen par.
type Result struct {
	Steps int
	Par   int
}

// Extra is the steps over par.
func (r Result) Extra() int { return r.Steps - r.Par }

// Stats is one player's record on daily puzzles.
type Stats struct {
	Played int // daily games with at least one accepted guess
	Wins   int // finished daily games
	// ExtraSteps is the total steps over par across wins.
	ExtraSteps int
	// CurrentStreak counts consecutive daily puzzles finished up to today's,
	// or up to yesterday's while today's is unfinished.
	CurrentStreak int
	BestStreak    int
	// Distribution counts wins by extra steps.
	Distribution Distribution
	// Today is the player's finished game on today's puzzle, nil until they finish it.
	Today *Result
	// Everyone counts every finished game on today's puzzle by extra steps.
	// Read only once the player has finished it: next to today's standings it
	// would reveal par.
	Everyone Distribution
	// Better, Tied and Worse count the other players who finished today's
	// puzzle in fewer, as many or more steps than the player; read with Everyone.
	Better, Tied, Worse int
}

// AverageExtra is the mean steps over par per win; ok is false without wins.
func (s Stats) AverageExtra() (avg float64, ok bool) {
	if s.Wins == 0 {
		return 0, false
	}
	return float64(s.ExtraSteps) / float64(s.Wins), true
}

// Stats reads playerID's daily record, 0 for a visitor who has not played,
// with streaks counted against today, today's daily puzzle.
func (s *Service) Stats(ctx context.Context, playerID int64, today Puzzle) (Stats, error) {
	var stats Stats
	err := s.db.Read(ctx, func(conn *sqlite.Conn) error {
		games, err := zz.OncePlayerDailyGames(conn, playerID)
		if err != nil {
			return err
		}
		var finished []int
		for _, game := range games {
			stats.Played++
			if !game.Finished {
				continue
			}
			result := Result{Steps: int(game.Steps), Par: int(game.Par)}
			stats.Wins++
			stats.ExtraSteps += result.Extra()
			stats.Distribution[DistributionBucket(result.Extra())]++
			finished = append(finished, int(game.PuzzleNumber))
			if int(game.PuzzleNumber) == today.Number {
				stats.Today = &result
			}
		}
		stats.CurrentStreak, stats.BestStreak = streaks(finished, today.Number)
		if stats.Today == nil {
			return nil
		}
		rows, err := zz.OncePuzzleStandings(conn, today.ID)
		if err != nil {
			return err
		}
		for _, row := range rows {
			stats.Everyone[DistributionBucket(int(row.Steps)-stats.Today.Par)]++
			switch {
			case row.PlayerId == playerID:
			case int(row.Steps) < stats.Today.Steps:
				stats.Better++
			case int(row.Steps) == stats.Today.Steps:
				stats.Tied++
			default:
				stats.Worse++
			}
		}
		return nil
	})
	return stats, err
}

// streaks returns the run of consecutive puzzle numbers ending at today, or at
// yesterday when today is missing, and the longest run. finished is ascending.
func streaks(finished []int, today int) (current, best int) {
	run := 0
	for i, number := range finished {
		if i > 0 && number == finished[i-1]+1 {
			run++
		} else {
			run = 1
		}
		best = max(best, run)
	}
	if n := len(finished); n > 0 && (finished[n-1] == today || finished[n-1] == today-1) {
		current = run
	}
	return current, best
}

// AllTimeMinGames is how many counted daily games an account needs to be ranked all-time.
const AllTimeMinGames = 5

// AllTimeStanding is one account on the all-time leaderboard.
type AllTimeStanding struct {
	// Rank counts distinct (average, best streak, current streak) results above
	// this one plus one, so ties share a rank and ranks have no gaps.
	Rank          int
	PlayerID      int64
	AccountID     int64
	Username      string
	AvatarHash    string // "" without a picture
	Games         int    // finished daily games counted in the average
	ExtraSteps    int    // total steps over par across those games
	CurrentStreak int
	BestStreak    int
	You           bool
}

// AverageExtra is the mean steps over par per counted game.
func (a AllTimeStanding) AverageExtra() float64 {
	return float64(a.ExtraSteps) / float64(a.Games)
}

// AllTime is the all-time leaderboard as one viewer sees it.
type AllTime struct {
	// Rows are the accounts with at least AllTimeMinGames counted games, by
	// average steps over par, then best streak, then current streak.
	Rows []AllTimeStanding
	// ViewerGames is the viewer's counted games, so an unlisted account can
	// see how many more it needs.
	ViewerGames int
	// TodayCounted is true when averages include today's puzzle, which they do
	// only once the viewer has finished it: a change in someone's average
	// next to their steps today would reveal today's par.
	TodayCounted bool
}

// AllTime ranks account holders over their finished daily games for
// viewerPlayerID, 0 for a visitor who has not played.
func (s *Service) AllTime(ctx context.Context, today Puzzle, viewerPlayerID int64) (AllTime, error) {
	var board AllTime
	err := s.db.Read(ctx, func(conn *sqlite.Conn) error {
		if viewerPlayerID != 0 {
			game, err := zz.OnceGetGame(conn, zz.GetGameParams{PuzzleId: today.ID, PlayerId: viewerPlayerID})
			if err != nil {
				return err
			}
			board.TodayCounted = game != nil && game.FinishedAt != nil
		}
		rows, err := zz.OnceAccountDailyFinishes(conn)
		if err != nil {
			return err
		}
		var finished []int
		for i, row := range rows {
			if i == 0 || row.AccountId != rows[i-1].AccountId {
				board.Rows = append(board.Rows, AllTimeStanding{
					PlayerID: row.PlayerId, AccountID: row.AccountId, Username: row.Username,
					You: viewerPlayerID != 0 && row.PlayerId == viewerPlayerID,
				})
				if row.AvatarHash != nil {
					board.Rows[len(board.Rows)-1].AvatarHash = *row.AvatarHash
				}
				finished = finished[:0]
			}
			standing := &board.Rows[len(board.Rows)-1]
			number := int(row.PuzzleNumber)
			finished = append(finished, number)
			if number != today.Number || board.TodayCounted {
				standing.Games++
				standing.ExtraSteps += int(row.Steps - row.Par)
			}
			if i == len(rows)-1 || rows[i+1].AccountId != row.AccountId {
				standing.CurrentStreak, standing.BestStreak = streaks(finished, today.Number)
			}
		}
		return nil
	})
	if err != nil {
		return AllTime{}, err
	}
	board.Rows = slices.DeleteFunc(board.Rows, func(row AllTimeStanding) bool {
		if row.You {
			board.ViewerGames = row.Games
		}
		return row.Games < AllTimeMinGames
	})
	slices.SortFunc(board.Rows, func(a, b AllTimeStanding) int {
		return cmp.Or(compareAllTime(a, b), cmp.Compare(strings.ToLower(a.Username), strings.ToLower(b.Username)), cmp.Compare(a.AccountID, b.AccountID))
	})
	for i := range board.Rows {
		board.Rows[i].Rank = 1
		if i > 0 {
			board.Rows[i].Rank = board.Rows[i-1].Rank
			if compareAllTime(board.Rows[i-1], board.Rows[i]) != 0 {
				board.Rows[i].Rank++
			}
		}
	}
	return board, nil
}

// compareAllTime orders by lower average steps over par, compared exactly,
// then longer best streak, then longer current streak.
func compareAllTime(a, b AllTimeStanding) int {
	return cmp.Or(
		cmp.Compare(a.ExtraSteps*b.Games, b.ExtraSteps*a.Games),
		cmp.Compare(b.BestStreak, a.BestStreak),
		cmp.Compare(b.CurrentStreak, a.CurrentStreak),
	)
}
