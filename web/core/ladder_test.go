package core_test

import (
	"regexp"
	"strings"
	"testing"

	"poople/web/core"

	"github.com/a-h/templ"
)

func render(t *testing.T, component templ.Component) string {
	t.Helper()
	var body strings.Builder
	if err := component.Render(t.Context(), &body); err != nil {
		t.Fatal(err)
	}
	return body.String()
}

var rungPattern = regexp.MustCompile(`<li id="([^"]*)" class="([^"]*)"[^>]*>(.*?)</li>`)
var tilePattern = regexp.MustCompile(`<span class="(tile[^"]*)" aria-hidden="true"[^>]*>([^<]*)</span>`)

type renderedRung struct {
	id, class, tiles, label string
}

// rungs reads each identified rung's classes, its tiles as letters with * after
// a matched letter, and its hidden text.
func rungs(t *testing.T, html string) []renderedRung {
	t.Helper()
	var found []renderedRung
	for _, m := range rungPattern.FindAllStringSubmatch(html, -1) {
		var tiles strings.Builder
		for _, tile := range tilePattern.FindAllStringSubmatch(m[3], -1) {
			tiles.WriteString(tile[2])
			if strings.Contains(tile[1], "tile--match") {
				tiles.WriteString("*")
			}
		}
		label := regexp.MustCompile(`<span class="visually-hidden"[^>]*>([^<]*)</span>`).FindStringSubmatch(m[3])
		if label == nil {
			t.Fatalf("rung %s has no hidden text", m[1])
		}
		found = append(found, renderedRung{m[1], m[2], tiles.String(), label[1]})
	}
	return found
}

func TestLadderMarksMatchesFreshRungAndGoal(t *testing.T) {
	html := render(t, core.Ladder("fuse", []string{"fuss", "foss", "loop"}, "poop", core.LadderOptions{Input: true, Fresh: true}))
	want := []renderedRung{
		{"rung-0", "rung rung--start", "FUSE", "FUSE, start word"},
		{"rung-1", "rung", "FUSS", "FUSS"},
		{"rung-2", "rung", "FO*SS", "FOSS, 1 letter in place"},
		{"rung-3", "rung rung--fresh", "LO*O*P*", "LOOP, 3 letters in place"},
		{"rung-input-4", "rung rung--input", "", "Your next word: empty"},
		{"rung-goal", "rung rung--goal", "POOP", "Goal: POOP"},
	}
	got := rungs(t, html)
	if len(got) != len(want) {
		t.Fatalf("rungs=%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rung %d=%+v, want %+v", i, got[i], want[i])
		}
	}
	if strings.Count(html, `class="rung rung--gap" aria-hidden="true"`) != 1 || !strings.Contains(html, `aria-label="Your ladder from FUSE to POOP"`) {
		t.Fatal("ladder missing its dashed gap or accessible name")
	}
	input := html[strings.Index(html, `id="rung-input-4"`):strings.Index(html, `id="rung-goal"`)]
	for _, want := range []string{"data-ignore-morph", `data-init="$guess = ''"`, `data-text="$guess[0] ?? &#39;&#39;"`, `data-text="$guess[3] ?? &#39;&#39;"`, "tile--cursor"} {
		if !strings.Contains(input, want) {
			t.Fatalf("input rung missing %q: %s", want, input)
		}
	}
	if strings.Count(input, `class="tile`) != 4 {
		t.Fatal("input rung must have four tiles")
	}
}

func TestLadderWithoutFreshOrInputShowsNoInputRung(t *testing.T) {
	html := render(t, core.Ladder("FUSE", []string{"FUSS"}, "POOP", core.LadderOptions{}))
	if strings.Contains(html, "rung--fresh") || strings.Contains(html, "rung--input") || strings.Contains(html, "$guess = ") {
		t.Fatal("ladder drew a fresh or input rung it was not asked for")
	}
	if !strings.Contains(html, `id="rung-goal"`) || !strings.Contains(html, "rung--gap") {
		t.Fatal("unfinished ladder must still show the gap and goal")
	}
}

func TestLadderReachedTargetEndsTheLadder(t *testing.T) {
	html := render(t, core.Ladder("poos", []string{"poop"}, "poop", core.LadderOptions{Input: true, Fresh: true, Rejection: 3}))
	got := rungs(t, html)
	want := []renderedRung{
		{"rung-0", "rung rung--start", "P*O*O*S", "POOS, start word"},
		{"rung-1", "rung rung--reached rung--fresh", "P*O*O*P*", "POOP, reached"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("rungs=%+v", got)
	}
	if strings.Contains(html, "rung--gap") || strings.Contains(html, "rung-goal") || strings.Contains(html, "rung--input") {
		t.Fatal("reached ladder still offers input, gap or goal")
	}
}

func TestLadderRejectionReplacesInputRungPerSequence(t *testing.T) {
	first := render(t, core.Ladder("fuse", []string{"fuss"}, "poop", core.LadderOptions{Input: true, Rejection: 7}))
	second := render(t, core.Ladder("fuse", []string{"fuss"}, "poop", core.LadderOptions{Input: true, Rejection: 8}))
	for seq, html := range map[string]string{"7": first, "8": second} {
		for _, want := range []string{`id="rung-input-2-` + seq + `"`, `class="rung rung--input rung--rejected"`, "data-ignore-morph", `data-init="$_rejectedGuess = $guess"`, `data-class:rung--rejected="$guess === $_rejectedGuess"`} {
			if !strings.Contains(html, want) {
				t.Fatalf("rejection %s missing %q", seq, want)
			}
		}
		if strings.Contains(html, `$guess = ''"`) {
			t.Fatal("a rejection must keep the refused letters in $guess")
		}
	}
	accepted := render(t, core.Ladder("fuse", []string{"fuss", "foss"}, "poop", core.LadderOptions{Input: true}))
	if !strings.Contains(accepted, `id="rung-input-3"`) || strings.Contains(accepted, "rung--rejected") {
		t.Fatal("accepted guess must give the input rung a new id without the rejection")
	}
}

func TestKeyboardWritesGuessSignalAndPostsFourLetters(t *testing.T) {
	html := render(t, core.Keyboard("/play/guess"))
	letters := regexp.MustCompile(`<button type="button" class="key" data-on:click="\$guess = \(\$guess \+ &#39;([a-z])&#39;\)\.slice\(0, 4\)">([a-z])</button>`).FindAllStringSubmatch(html, -1)
	var typed strings.Builder
	for _, key := range letters {
		if key[1] != key[2] {
			t.Fatalf("key %q types %q", key[2], key[1])
		}
		typed.WriteString(key[1])
	}
	if typed.String() != "qwertyuiopasdfghjklzxcvbnm" {
		t.Fatalf("letter keys=%q", typed.String())
	}
	for _, want := range []string{
		`data-signals:guess__ifmissing="''"`,
		`class="key key--wide key--enter" data-on:click="$guess.length === 4 &amp;&amp; @post(&#34;/play/guess&#34;)"`,
		`aria-label="Delete letter" data-on:click="$guess = $guess.slice(0, -1)"`,
		`data-on:keydown__window="`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("keyboard missing %q", want)
		}
	}
	if !strings.Contains(render(t, core.Keyboard(`/x");alert(1)//`)), `@post(&#34;/x\&#34;);alert(1)//&#34;)`) {
		t.Fatal("guess URL is not quoted as a JavaScript string")
	}
}

func TestToastKeepsRegionAndKeysMessage(t *testing.T) {
	if html := render(t, core.Toast("", 0)); html != `<div class="toast-region" role="status"></div>` {
		t.Fatalf("empty toast=%q", html)
	}
	html := render(t, core.Toast("Not <in> word list", 4))
	if !strings.Contains(html, `<p id="toast-4" class="toast">Not &lt;in&gt; word list</p>`) {
		t.Fatalf("toast=%q", html)
	}
}

func TestMiniWordMatchesTarget(t *testing.T) {
	html := render(t, core.MiniWord("poss", "poop"))
	if strings.Count(html, `class="mini-tile tile--match"`) != 2 || strings.Count(html, `class="mini-tile"`) != 2 || !strings.Contains(html, `<span class="visually-hidden">POSS</span>`) {
		t.Fatalf("mini word=%q", html)
	}
}
