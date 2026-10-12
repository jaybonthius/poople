package core

import (
	"strconv"
	"strings"
)

// FinishResult is a finished game as the finish sheet draws it. Build it only
// once the player has finished: it carries par and a shortest solution.
type FinishResult struct {
	Start    string
	Target   string
	Steps    int
	Par      int
	Solution []string // one shortest ladder, start word first
}

// Perfect is true when the player matched par.
func (r FinishResult) Perfect() bool { return r.Steps == r.Par }

// Route is "START → POOP".
func (r FinishResult) Route() string {
	return strings.ToUpper(r.Start) + " → " + strings.ToUpper(r.Target)
}

func (r FinishResult) parDelta() string {
	if over := r.Steps - r.Par; over > 0 {
		return "+" + strconv.Itoa(over) + " over par"
	}
	return "On par"
}

// StepCount is "1 step" or "n steps".
func StepCount(n int) string {
	if n == 1 {
		return "1 step"
	}
	return strconv.Itoa(n) + " steps"
}
