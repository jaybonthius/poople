package stats

import (
	"fmt"
	"strconv"

	"poople/web/services/game"

	"github.com/a-h/templ"
)

// statsView is everything View draws, read from SQLite for one viewer.
type statsView struct {
	Puzzle game.Puzzle // today's daily puzzle
	Stats  game.Stats
}

func (v statsView) number() string { return "#" + strconv.Itoa(v.Puzzle.Number) }

func (v statsView) average() string {
	avg, ok := v.Stats.AverageExtra()
	if !ok {
		return "–"
	}
	return strconv.FormatFloat(avg, 'f', 2, 64)
}

// distRow is one bar of a distribution.
type distRow struct {
	Label string // +0 … +5, 6+
	Count int
	Share int  // percent of the largest bar
	Today bool // the bucket of the viewer's game today
}

func (v statsView) rows(d game.Distribution) []distRow {
	most := 0
	for _, n := range d {
		most = max(most, n)
	}
	today := -1
	if v.Stats.Today != nil {
		today = game.DistributionBucket(v.Stats.Today.Extra())
	}
	rows := make([]distRow, len(d))
	for i, n := range d {
		rows[i] = distRow{Label: "+" + strconv.Itoa(i), Count: n, Today: i == today}
		if i == len(d)-1 {
			rows[i].Label = strconv.Itoa(i) + "+"
		}
		if most > 0 {
			rows[i].Share = n * 100 / most
		}
	}
	return rows
}

// shareStyle sizes a bar against the largest one; it holds only a number, so
// it needs no sanitizing.
func shareStyle(share int) templ.SafeCSS {
	return templ.SafeCSS("--share: " + strconv.Itoa(share) + "%")
}

func (v statsView) youFootnote() string {
	text := "Based on your " + plural(v.Stats.Wins, "solve", "solves") + "."
	if v.Stats.Today != nil {
		text += " Today is outlined."
	}
	return text
}

func (v statsView) everyoneFootnote() string {
	return fmt.Sprintf("%s solved %s. Your %s is outlined.", plural(v.Stats.Everyone.Total(), "player", "players"), v.number(), extraLabel(v.Stats.Today.Extra()))
}

// todayHeading is the comparison section's title, such as "You finished 1 over par".
func (v statsView) todayHeading() string {
	if extra := v.Stats.Today.Extra(); extra > 0 {
		return "You finished " + strconv.Itoa(extra) + " over par"
	}
	return "You finished on par"
}

// comparison sets the viewer's steps today against the other finishers.
func (v statsView) comparison() string {
	s := v.Stats
	others := s.Better + s.Tied + s.Worse
	if others == 0 {
		return "You’re the first to solve " + v.number() + "."
	}
	return fmt.Sprintf("Of the %s who solved %s, %d did better, %d tied with you and %d used more steps.",
		plural(others, "other player", "other players"), v.number(), s.Better, s.Tied, s.Worse)
}

func extraLabel(extra int) string {
	if extra >= len(game.Distribution{})-1 {
		return strconv.Itoa(len(game.Distribution{})-1) + "+"
	}
	return "+" + strconv.Itoa(extra)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
