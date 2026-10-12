package play

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"poople/ladder"
	"poople/web/core"
	"poople/web/services/game"
	"poople/web/services/identity"
)

// playView is everything View draws, read from SQLite for one viewer.
type playView struct {
	Game game.Game
	// NextIn is the time left until the next daily puzzle opens.
	NextIn time.Duration
	// Fresh marks the newest rung as just accepted so its tiles flip in.
	Fresh bool
	// Standings is today's leaderboard; read only once the viewer has finished.
	Standings game.Standings
	// SignedIn is true when the viewer has an account.
	SignedIn bool
}

func (v playView) number() string { return strconv.Itoa(v.Game.Puzzle.Number) }

func (v playView) opens() time.Time { return ladder.DailyOpens(v.Game.Puzzle.Number) }

// signals are the page signals the view owns: the puzzle the browser shows,
// which guesses are posted against, and how many steps it shows.
func (v playView) signals() string {
	b, _ := json.Marshal(map[string]int64{"puzzleId": v.Game.Puzzle.ID, "steps": int64(v.Game.Steps)})
	return string(b)
}

// rejectionSeq keys the input rung and toast while the latest guess stands rejected.
func (v playView) rejectionSeq() int {
	if v.Game.Rejection == "" {
		return 0
	}
	return int(v.Game.RejectionSeq)
}

// toastKey names the rejection toast per puzzle and rejection, so a new
// puzzle's first rejection is not mistaken for the one the page loaded with.
func (v playView) toastKey() string {
	return strconv.FormatInt(v.Game.Puzzle.ID, 10) + "-" + strconv.Itoa(v.rejectionSeq())
}

// result is the finish sheet's summary; par and the solution are set only
// once the game is finished.
func (v playView) result() core.FinishResult {
	return core.FinishResult{Start: v.Game.Puzzle.StartWord, Target: ladder.Target, Steps: v.Game.Steps, Par: v.Game.Par, Solution: v.Game.Solution}
}

// shareText is the spoiler-free result: the puzzle, its route, steps against
// par and one row per rung, brown where the letter is POOP's in that position.
func (v playView) shareText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Poople #%d\n%s · %s (par %d)", v.Game.Puzzle.Number, v.result().Route(), core.StepCount(v.Game.Steps), v.Game.Par)
	for _, word := range v.Game.Ladder {
		b.WriteByte('\n')
		for i := range len(word) {
			if i < len(ladder.Target) && word[i] == ladder.Target[i] {
				b.WriteString("🟫")
			} else {
				b.WriteString("⬜")
			}
		}
	}
	return b.String()
}

// unlistedFootnote names how many finishers the standings leave out.
func (v playView) unlistedFootnote() string {
	return fmt.Sprintf("%d players solved #%d; the top %d are listed.", v.Standings.Finishers, v.Game.Puzzle.Number, game.StandingsShown)
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

// countdown is the remaining time as h:mm:ss and as an ISO 8601 duration.
func countdown(d time.Duration) (text, iso string) {
	s := int((d + time.Second - 1) / time.Second)
	if s < 0 {
		s = 0
	}
	return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60), fmt.Sprintf("PT%dH%dM%dS", s/3600, s/60%60, s%60)
}

// countdownInit anchors the browser's countdown to the server's remaining
// time, so a wrong device clock still counts to 08:00 UTC.
func countdownInit(d time.Duration) string {
	return "$_now = Date.now(); $_nextPuzzleAt = $_now + " + strconv.FormatInt(d.Milliseconds(), 10)
}

const (
	countdownSeconds = "Math.max(0, Math.ceil(($_nextPuzzleAt - $_now) / 1000))"
	countdownText    = "(s => Math.floor(s / 3600) + ':' + String(Math.floor(s / 60) % 60).padStart(2, '0') + ':' + String(s % 60).padStart(2, '0'))(" + countdownSeconds + ")"
	countdownISO     = "(s => 'PT' + Math.floor(s / 3600) + 'H' + (Math.floor(s / 60) % 60) + 'M' + (s % 60) + 'S')(" + countdownSeconds + ")"
)

// avatarURL is the row's picture, "" for anonymous players and accounts without one.
func avatarURL(row game.Standing) string {
	return (&identity.Account{ID: row.AccountID, AvatarHash: row.AvatarHash}).AvatarPath()
}
