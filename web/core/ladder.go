package core

import (
	"encoding/json"
	"strconv"
	"strings"
)

// LadderOptions carry the play state that Ladder draws around the words.
type LadderOptions struct {
	// Input shows the input rung, whose tiles show the letters of the $guess
	// signal, above the dashed gap. It is ignored once the last rung reaches
	// the target.
	Input bool
	// Fresh marks the newest rung so its tiles flip in. Pass true only when the
	// latest accepted guess created it.
	Fresh bool
	// Rejection is the game's rejection sequence while its latest guess stands
	// rejected, otherwise 0. Each new value re-creates the input rung, so the
	// wriggle replays; it turns tomato while $guess still holds the refused word.
	Rejection int
}

// letterTile is one tile: an upper-case letter and whether it equals the
// target's letter in the same position.
type letterTile struct {
	Letter string
	Match  bool
}

func letterTiles(word, target string) []letterTile {
	word, target = strings.ToUpper(word), strings.ToUpper(target)
	tiles := make([]letterTile, 0, len(word))
	for i := range len(word) {
		tiles = append(tiles, letterTile{Letter: word[i : i+1], Match: i < len(target) && target[i] == word[i]})
	}
	return tiles
}

// rungLabel is the hidden text naming a word and its letters in place.
func rungLabel(word, target string) string {
	matches := 0
	for _, tile := range letterTiles(word, target) {
		if tile.Match {
			matches++
		}
	}
	word = strings.ToUpper(word)
	switch matches {
	case 0:
		return word
	case 1:
		return word + ", 1 letter in place"
	default:
		return word + ", " + strconv.Itoa(matches) + " letters in place"
	}
}

// answerRungLabel is the hidden text for rung i of an AnswerLadder.
func answerRungLabel(i int, word, target string) string {
	if i == 0 {
		return upper(word) + ", start word"
	}
	return "Step " + strconv.Itoa(i) + ": " + rungLabel(word, target)
}

func reached(rungs []string, target string) bool {
	return len(rungs) > 0 && strings.EqualFold(rungs[len(rungs)-1], target)
}

func rungID(position int) string {
	return "rung-" + strconv.Itoa(position)
}

// inputRungID changes when the ladder grows or a new rejection arrives, so the
// morph replaces the input rung instead of keeping the browser's copy.
func inputRungID(position, rejection int) string {
	id := "rung-input-" + strconv.Itoa(position)
	if rejection > 0 {
		id += "-" + strconv.Itoa(rejection)
	}
	return id
}

func guessLetterExpr(i int) string {
	return "$guess[" + strconv.Itoa(i) + "] ?? ''"
}

func guessTileClassExpr(i int) string {
	n := strconv.Itoa(i)
	return "{'tile--typed': $guess.length > " + n + ", 'tile--cursor': $guess.length === " + n + "}"
}

// jsString quotes s as a JavaScript string literal for a Datastar expression.
func jsString(s string) string {
	quoted, _ := json.Marshal(s)
	return string(quoted)
}

// scrollIntoViewExpr brings a newly drawn rung into the board's view.
const scrollIntoViewExpr = "el.scrollIntoView({block: 'nearest'})"

// reachedRungInit clears letters typed after the last guess, so nothing is
// left to post once the ladder reaches the target, and shows the reached rung.
const reachedRungInit = "$guess = ''; " + scrollIntoViewExpr

// rejectedGuessExpr is true while the input rung still holds the refused word.
const rejectedGuessExpr = "$guess !== '' && $guess === $_rejectedGuess"

// guessBusyExpr marks Enter busy while a guess posted from the keyboard is in
// flight, including its retries.
const guessBusyExpr = "if (evt.detail.el?.closest?.('.keyboard') === el) { " +
	"if (evt.detail.type === 'started') el.guessesInFlight = (el.guessesInFlight ?? 0) + 1; " +
	"else if (evt.detail.type === 'finished') el.guessesInFlight = Math.max(0, (el.guessesInFlight ?? 0) - 1); " +
	"$_guessBusy = (el.guessesInFlight ?? 0) > 0 }"

// connectionExpr notes a lost connection when any request retries and clears it
// when the server next patches the page, which the live stream does on reconnect.
const connectionExpr = "if (evt.detail.type === 'retrying' || evt.detail.type === 'retries-failed') $_connectionLost = true; " +
	"else if (evt.detail.type === 'datastar-patch-elements') $_connectionLost = false"

// toastSeenKey is the key of the message a page loads with, "" for none.
func toastSeenKey(message, key string) string {
	if message == "" {
		return ""
	}
	return key
}

func submitGuessExpr(guessURL string) string {
	return "$guess.length === 4 && @post(" + jsString(guessURL) + ")"
}

// keydownExpr types on the physical keyboard unless the keyboard is inert, focus
// is on another control or a modifier is held; Enter and Backspace keep their
// usual meaning elsewhere.
func keydownExpr(guessURL string) string {
	return "if (!el.inert && !evt.ctrlKey && !evt.metaKey && !evt.altKey && !evt.isComposing && " +
		"(evt.target.closest('.keyboard') || !evt.target.closest('a, button, input, select, textarea, summary, dialog, [contenteditable]'))) { " +
		"if (evt.key === 'Enter') { evt.preventDefault(); " + submitGuessExpr(guessURL) + " } " +
		"else if (evt.key === 'Backspace') { evt.preventDefault(); $guess = $guess.slice(0, -1) } " +
		"else if (evt.key.length === 1 && /[a-z]/i.test(evt.key)) { $guess = ($guess + evt.key.toLowerCase()).slice(0, 4) } }"
}

func typeLetterExpr(letter string) string {
	return "$guess = ($guess + '" + letter + "').slice(0, 4)"
}

var keyboardRows = [3]string{"qwertyuiop", "asdfghjkl", "zxcvbnm"}

func upper(word string) string {
	return strings.ToUpper(word)
}

func rungClass(class string, fresh bool) string {
	if fresh {
		return class + " rung--fresh"
	}
	return class
}
