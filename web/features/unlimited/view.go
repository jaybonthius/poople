package unlimited

import (
	"encoding/json"

	"poople/ladder"
	"poople/web/core"
	"poople/web/services/game"
)

// unlimitedView is everything View draws, read from SQLite for one viewer.
type unlimitedView struct {
	// Playing is true when the viewer has a current unlimited game; otherwise
	// View shows the start state.
	Playing bool
	Game    game.Game
	// Fresh marks the newest rung as just accepted so its tiles flip in.
	Fresh bool
}

// signals are the page signals the view owns: the puzzle the browser shows,
// which guesses are posted against, and how many steps it shows.
func (v unlimitedView) signals() string {
	b, _ := json.Marshal(map[string]int64{"puzzleId": v.Game.Puzzle.ID, "steps": int64(v.Game.Steps)})
	return string(b)
}

// rejectionSeq keys the input rung and toast while the latest guess stands rejected.
func (v unlimitedView) rejectionSeq() int {
	if v.Game.Rejection == "" {
		return 0
	}
	return int(v.Game.RejectionSeq)
}

// result is the finish sheet's summary; par and the solution are set only
// once the game is finished.
func (v unlimitedView) result() core.FinishResult {
	return core.FinishResult{Start: v.Game.Puzzle.StartWord, Target: ladder.Target, Steps: v.Game.Steps, Par: v.Game.Par, Solution: v.Game.Solution}
}
