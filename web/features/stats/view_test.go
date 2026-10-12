package stats

import (
	"testing"

	"poople/web/services/game"
)

func TestComparisonNamesOnlyNonZeroCounts(t *testing.T) {
	for _, tc := range []struct {
		better, tied, worse int
		want                string
	}{
		{0, 0, 0, "You’re the first to solve #423."},
		{1, 0, 0, "Of the 1 other player who solved #423, 1 did better."},
		{0, 2, 0, "Of the 2 other players who solved #423, 2 tied with you."},
		{1, 0, 3, "Of the 4 other players who solved #423, 1 did better and 3 used more steps."},
		{1, 1, 1, "Of the 3 other players who solved #423, 1 did better, 1 tied with you and 1 used more steps."},
	} {
		v := statsView{Puzzle: game.Puzzle{Number: 423}, Stats: game.Stats{Better: tc.better, Tied: tc.tied, Worse: tc.worse}}
		if got := v.comparison(); got != tc.want {
			t.Errorf("better=%d tied=%d worse=%d: %q, want %q", tc.better, tc.tied, tc.worse, got, tc.want)
		}
	}
}
