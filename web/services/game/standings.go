package game

import (
	"context"
	"time"

	"poople/store/zz"

	"zombiezen.com/go/sqlite"
)

// StandingsShown is how many of a puzzle's best games Standings lists before
// the viewer's own row.
const StandingsShown = 20

// Standing is one finished game on a puzzle's leaderboard.
type Standing struct {
	// Rank counts distinct (steps, solve time) results above this one plus one,
	// so tied games share a rank and ranks have no gaps.
	Rank       int
	PlayerID   int64
	AccountID  int64  // 0 for anonymous players
	Username   string // "" for anonymous players
	AvatarHash string // "" without a picture
	Steps      int
	SolveTime  time.Duration
	You        bool // the viewer's own game
}

// Standings is a puzzle's leaderboard as one viewer sees it.
type Standings struct {
	// Rows are the best StandingsShown finished games, anonymous players
	// included, ranked by steps then solve time (then who finished first).
	// When the viewer finished below them, their row follows as the last row.
	Rows []Standing
	// Finishers counts every finished game on the puzzle.
	Finishers int
	// ViewerAfterGap is true when the last row is the viewer's and other
	// finishers rank between it and the rows before it.
	ViewerAfterGap bool
}

// Unlisted counts the finishers Rows leave out.
func (s Standings) Unlisted() int { return s.Finishers - len(s.Rows) }

// Standings reads the finished games on the puzzle for viewerPlayerID, which
// is 0 for a visitor who has not played. Rows hold steps and solve times but
// no words, so they reveal neither ladders nor solutions.
func (s *Service) Standings(ctx context.Context, puzzleID, viewerPlayerID int64) (Standings, error) {
	var standings Standings
	err := s.db.Read(ctx, func(conn *sqlite.Conn) error {
		rows, err := zz.OncePuzzleStandings(conn, puzzleID)
		if err != nil {
			return err
		}
		standings = rankStandings(rows, viewerPlayerID)
		return nil
	})
	return standings, err
}

func rankStandings(rows []zz.PuzzleStandingsRes, viewerPlayerID int64) Standings {
	standings := Standings{Finishers: len(rows)}
	rank := 0
	for i, row := range rows {
		if i == 0 || row.Steps != rows[i-1].Steps || row.SolveTime != rows[i-1].SolveTime {
			rank++
		}
		you := viewerPlayerID != 0 && row.PlayerId == viewerPlayerID
		if i >= StandingsShown && !you {
			continue
		}
		standing := Standing{
			Rank: rank, PlayerID: row.PlayerId, Steps: int(row.Steps),
			SolveTime: time.Duration(row.SolveTime) * time.Millisecond, You: you,
		}
		if row.AccountId != nil {
			standing.AccountID = *row.AccountId
		}
		if row.Username != nil {
			standing.Username = *row.Username
		}
		if row.AvatarHash != nil {
			standing.AvatarHash = *row.AvatarHash
		}
		standings.Rows = append(standings.Rows, standing)
		if i >= StandingsShown {
			standings.ViewerAfterGap = i > StandingsShown
			break
		}
	}
	return standings
}
