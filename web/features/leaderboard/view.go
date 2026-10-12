package leaderboard

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"poople/ladder"
	"poople/web/services/game"
	"poople/web/services/identity"
)

// tab is one leaderboard period, served at its own path.
type tab int

const (
	today tab = iota
	allTime
)

func (t tab) path() string {
	if t == allTime {
		return "/leaderboard/all-time"
	}
	return "/leaderboard"
}

// boardView is everything View draws, read from SQLite for one viewer.
type boardView struct {
	Tab    tab
	Puzzle game.Puzzle // today's daily puzzle
	// SignedIn is true when the viewer has an account.
	SignedIn bool
	// Finished is true once the viewer has finished today's puzzle; Par is 0 until then.
	Finished bool
	Par      int
	// Standings is today's leaderboard (Today tab).
	Standings game.Standings
	// AllTime is the all-time leaderboard (All-time tab).
	AllTime game.AllTime
}

func (v boardView) streamExpr() string {
	return "@get('" + v.Tab.path() + "/state', {retry: 'always', retryMaxCount: Infinity})"
}

func (v boardView) title() string { return "Daily #" + strconv.Itoa(v.Puzzle.Number) }

func (v boardView) route() string {
	return strings.ToUpper(v.Puzzle.StartWord) + " → " + strings.ToUpper(ladder.Target)
}

// todayCaption names the puzzle and the ranking, and par once the viewer has finished.
func (v boardView) todayCaption() string {
	caption := v.title() + ", " + v.route() + "."
	if v.Finished {
		caption += " Par " + strconv.Itoa(v.Par) + "."
	}
	return caption + " Ranked by steps, then solve time."
}

// parDelta is a row's steps against par, shown once the viewer knows par.
func (v boardView) parDelta(row game.Standing) string {
	if over := row.Steps - v.Par; over > 0 {
		return "+" + strconv.Itoa(over)
	}
	return "par"
}

func (v boardView) unlistedFootnote() string {
	return fmt.Sprintf("%d players solved #%d; the top %d are listed.", v.Standings.Finishers, v.Puzzle.Number, game.StandingsShown)
}

func (v boardView) allTimeCaption() string {
	caption := fmt.Sprintf("Ranked by average steps over par, then best streak. Players need at least %d daily games.", game.AllTimeMinGames)
	if !v.AllTime.TodayCounted {
		caption += " Today’s puzzle counts once you finish it."
	}
	return caption
}

// listed is true when the viewer's account has a row on the all-time board.
func (v boardView) listed() bool {
	for _, row := range v.AllTime.Rows {
		if row.You {
			return true
		}
	}
	return false
}

func (v boardView) gamesNeeded() string {
	n := game.AllTimeMinGames - v.AllTime.ViewerGames
	games := "games"
	if n == 1 {
		games = "game"
	}
	return fmt.Sprintf("Finish %d more daily %s to join the all-time board.", n, games)
}

// average formats steps over par per game with two decimals.
func average(row game.AllTimeStanding) string {
	return strconv.FormatFloat(row.AverageExtra(), 'f', 2, 64)
}

// clock formats a duration as m:ss, or h:mm:ss from an hour.
func clock(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	if s < 0 {
		s = 0
	}
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// avatarURL is an account's picture, "" for anonymous players and accounts without one.
func avatarURL(accountID int64, hash string) string {
	return (&identity.Account{ID: accountID, AvatarHash: hash}).AvatarPath()
}

func current(on bool) string {
	if on {
		return "page"
	}
	return "false"
}
