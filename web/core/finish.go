package core

import (
	"strconv"
	"strings"
)

// finishSheetEffect opens the finish sheet as a modal and focuses it while
// $_finishClosed is false, and closes it when it turns true.
const finishSheetEffect = "if ($_finishClosed) { el.open && el.close() } else if (!el.open) { el.showModal(); el.focus() }"

// finishSheetClosed records a close from any source (Close, Escape, backdrop)
// and moves focus to Show result once it is shown.
const finishSheetClosed = "$_finishClosed = true; requestAnimationFrame(() => document.getElementById('show-result')?.focus())"

// finishSheetBackdropClick closes the sheet on a click outside its box: a modal
// dialog receives clicks on its ::backdrop as clicks on itself.
const finishSheetBackdropClick = "if (evt.target === el) { const box = el.getBoundingClientRect(); " +
	"if (evt.clientX < box.left || evt.clientX > box.right || evt.clientY < box.top || evt.clientY > box.bottom) el.close() }"

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
