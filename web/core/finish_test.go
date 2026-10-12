package core_test

import (
	"strings"
	"testing"

	"poople/web/core"
)

// The finish sheet must be a real modal: opened with showModal (the page
// behind turns inert and focus stays inside), never rendered already open,
// and on close focus moves to Show result instead of being lost.
func TestFinishSheetOpensAsModalAndReturnsFocus(t *testing.T) {
	html := render(t, core.FinishSheet(core.FinishResult{Start: "fuse", Target: "poop", Steps: 5, Par: 5, Solution: []string{"fuse", "poop"}}))
	dialog := html[:strings.Index(html, ">")]
	if !strings.HasPrefix(dialog, `<dialog id="finish" class="sheet"`) || strings.Contains(dialog, " open") {
		t.Fatalf("finish sheet must start closed and open as a modal: %s", dialog)
	}
	for _, want := range []string{`data-preserve-attr="open"`, "el.showModal(); el.focus()", "el.open &amp;&amp; el.close()", `data-on:close="$_finishClosed = true; requestAnimationFrame(() =&gt; document.getElementById(&#39;show-result&#39;)?.focus())"`} {
		if !strings.Contains(dialog, want) {
			t.Fatalf("finish sheet missing %q: %s", want, dialog)
		}
	}
	if strings.Contains(html, "sheet-scrim") {
		t.Fatal("a modal sheet uses its ::backdrop, not a scrim element")
	}
	if !strings.Contains(render(t, core.ShowResultButton()), `id="show-result"`) {
		t.Fatal("Show result has no id to return focus to")
	}
}

// A daily finish passes no solution and the sheet draws no ladder; unlimited
// passes one and gets it as a vertical answer ladder.
func TestFinishSheetDrawsTheSolutionOnlyWhenGiven(t *testing.T) {
	daily := render(t, core.FinishSheet(core.FinishResult{Start: "fuse", Target: "poop", Steps: 6, Par: 5}))
	if strings.Contains(daily, "A shortest ladder") || strings.Contains(daily, "ladder--answer") {
		t.Fatalf("finish sheet without a solution draws one: %s", daily)
	}
	for _, want := range []string{"Solved", "FUSE → POOP", "<strong>6 steps</strong>, par 5", "+1 over par"} {
		if !strings.Contains(daily, want) {
			t.Fatalf("finish sheet missing %q", want)
		}
	}
	unlimited := render(t, core.FinishSheet(core.FinishResult{Start: "fuse", Target: "poop", Steps: 5, Par: 5, Solution: []string{"fuse", "fuss", "foss", "poss", "poos", "poop"}}))
	if !strings.Contains(unlimited, "A shortest ladder") || !strings.Contains(unlimited, `<ol class="ladder ladder--answer"`) || !strings.Contains(unlimited, "Step 3: POSS") {
		t.Fatalf("finish sheet with a solution lacks the answer ladder: %s", unlimited)
	}
}
