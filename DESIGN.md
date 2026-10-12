---
name: Poople
description: The daily four-letter word ladder to POOP, drawn as a real ladder on a board-game green.
colors:
  board: "#1d5b40"
  board-deep: "#164832"
  on-board: "#f2f7ee"
  on-board-muted: "#bcd5c4"
  wood: "#7a4726"
  wood-edge: "#59321a"
  wood-hover: "#683b1e"
  on-wood: "#fff8ee"
  wood-soft: "#f1e3d6"
  rail-ghost: "#b89a83"
  ground: "#edf2ea"
  surface: "#fbfcf7"
  surface-sunken: "#e3ebdf"
  ink: "#18221d"
  ink-muted: "#475a50"
  ink-faint: "#5a7064"
  line: "#c8d4c6"
  line-strong: "#a9b9a8"
  tile-face: "#fffdf6"
  tile-edge: "#c9d3c4"
  tile-empty: "#f6f9f2"
  key: "#dbe5d7"
  key-edge: "#b5c4b1"
  tomato: "#b8392a"
  tomato-soft: "#f8e1dc"
  mustard: "#e3a72f"
  mustard-soft: "#f8ebc9"
  sky: "#2a6fae"
  leaf: "#2f7a4c"
  focus: "#2a6fae"
  board-dark: "#123826"
  wood-dark: "#9a5c2e"
  ground-dark: "#0f1713"
  surface-dark: "#17221c"
  ink-dark: "#e7eee4"
  tile-face-dark: "#202d26"
  key-dark: "#2a3a31"
  tomato-dark: "#ef7a66"
  sky-dark: "#79b4e6"
  focus-dark: "#8cc2f0"
typography:
  tile:
    fontFamily: "Fredoka, ui-rounded, system-ui, sans-serif"
    fontSize: "calc(var(--tile) * 0.56)"
    fontWeight: 600
    lineHeight: 1
  display:
    fontFamily: "Fredoka, ui-rounded, system-ui, sans-serif"
    fontSize: "2.25rem"
    fontWeight: 600
    lineHeight: 1.1
  headline:
    fontFamily: "Fredoka, ui-rounded, system-ui, sans-serif"
    fontSize: "1.75rem"
    fontWeight: 600
    lineHeight: 1.1
  title:
    fontFamily: "Fredoka, ui-rounded, system-ui, sans-serif"
    fontSize: "1.125rem"
    fontWeight: 600
    lineHeight: 1.1
  body:
    fontFamily: "Figtree, system-ui, -apple-system, Segoe UI, sans-serif"
    fontSize: "1rem"
    fontWeight: 400
    lineHeight: 1.45
  label:
    fontFamily: "Figtree, system-ui, -apple-system, Segoe UI, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 700
    lineHeight: 1.25
rounded:
  mini-tile: "0.3rem"
  tile: "0.5rem"
  key: "0.5rem"
  control: "0.625rem"
  sheet: "1.25rem"
  pill: "999px"
spacing:
  "1": "0.25rem"
  "2": "0.5rem"
  "3": "0.75rem"
  "4": "1rem"
  "5": "1.25rem"
  "6": "1.5rem"
  "8": "2rem"
  "10": "2.5rem"
  "12": "3rem"
components:
  tile:
    backgroundColor: "{colors.tile-face}"
    textColor: "{colors.ink}"
    rounded: "{rounded.tile}"
    size: "clamp(2.75rem, min(12.5vw, 6.4dvh), 3.5rem)"
  tile-match:
    backgroundColor: "{colors.wood}"
    textColor: "{colors.on-wood}"
    rounded: "{rounded.tile}"
  tile-input:
    backgroundColor: "{colors.tile-empty}"
    textColor: "{colors.ink}"
    rounded: "{rounded.tile}"
  tile-rejected:
    backgroundColor: "{colors.tomato-soft}"
    textColor: "{colors.ink}"
    rounded: "{rounded.tile}"
  key:
    backgroundColor: "{colors.key}"
    textColor: "{colors.ink}"
    rounded: "{rounded.key}"
    height: "clamp(3rem, 6.6dvh, 3.5rem)"
  key-enter:
    backgroundColor: "{colors.wood}"
    textColor: "{colors.on-wood}"
    rounded: "{rounded.key}"
  button-primary:
    backgroundColor: "{colors.wood}"
    textColor: "{colors.on-wood}"
    rounded: "{rounded.control}"
    padding: "0 1.25rem"
    height: "3rem"
  button-primary-hover:
    backgroundColor: "{colors.wood-hover}"
  button-secondary:
    backgroundColor: "{colors.key}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "0 1.25rem"
    height: "3rem"
  masthead:
    backgroundColor: "{colors.board}"
    textColor: "{colors.on-board}"
    height: "3.5rem"
  toast:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.surface}"
    rounded: "{rounded.control}"
    padding: "0.75rem 1.25rem"
  sheet:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.sheet}"
    padding: "1.25rem"
    width: "30rem"
  input:
    backgroundColor: "{colors.tile-empty}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    height: "3rem"
---

# Design System: Poople

## Overview

**Creative North Star: "The Snakes and Ladders Board"**

Poople is a word ladder, so the board draws one. Two wooden rails run from the start word at the top to a fixed POOP rung at the foot; every accepted guess lands as a rung between them, and the stretch still to climb is a dashed pair of rails with nothing written on it. The world is a printed board game: a deep board-green band across the top, a pale green-tinted table under the play, ivory tiles with ink letters, ladder-wood brown for every letter already in place and for the rails themselves, and tomato, mustard and sky as the game's spare paint for state and data.

It is an Operate surface first. The shell, keyboard, sheets and tables are the controls every Wordle player already knows; the world contributes type, palette, density and one signature move, the ladder. Poop humour stays in small places: the mascot, the brown, the snake-like wriggle when a word is refused.

Prototype: `docs/design/prototype/` (open `index.html`; states by fragment, e.g. `#playing`, `#finish`). Screens: `docs/design/prototype/screens/`.

**Key Characteristics:**
- The ladder is literal: rails and rungs frame the tiles; POOP waits at the foot from the first move.
- Brown means "this letter is already where POOP needs it", and also marks the one primary action.
- Flat colour fields divide regions: header band, ground, keyboard tray, sheet. No gradients, no decorative borders.
- Every neutral is tinted green; ink is never pure black.
- Light and dark from the same tokens via `light-dark()`; the system preference wins unless the player pins one.

## Colors

A board-game print palette: one committed shell colour, one wood accent, three spare paints, green-tinted neutrals.

### Primary
- **Board Green** (`board`): the masthead band and the dark theme's header. Owns the top of every screen; nothing else uses it as a fill.
- **Ladder Wood** (`wood`): matching tiles, the ladder rails and crossbars, Enter, the primary button and the "You" distribution series. `wood-edge` is its bevel and border; `on-wood` its text. Wood used as text (the On-par chip, the Perfect! stamp, link hovers) is `wood-ink`, which lightens in dark so it keeps 4.5:1.

### Secondary
- **Tomato** (`tomato`): rejection only. Rejected input tiles (`tomato-soft` fill, tomato border) and field errors.
- **Mustard** (`mustard`): first place and focus-of-attention highlights; `mustard-soft` fills the player's own row in standings and tables and the over-par chip.
- **Sky** (`sky`): the "Everyone" distribution series and the focus ring (`focus`).
- **Leaf** (`leaf`): the "Solved" verdict stamp and the live indicator.

### Neutral
- **Board Table** (`ground`): page ground under the play and pages.
- **Card Stock** (`surface`): sheets and the account form; `surface-sunken` for the keyboard tray, segmented controls and rank chips.
- **Ink** (`ink`, `ink-muted`, `ink-faint`): text in three steps; all three clear 4.5:1 on ground and surface.
- **Rules** (`line`, `line-strong`): hairlines in tables and sheet sections, input borders.
- **Tile Ivory** (`tile-face`, `tile-edge`, `tile-empty`): tile faces, their bottom bevel and the empty input cells.
- **Ghost Rail** (`rail-ghost`): the dashed unclimbed rails and the goal rung's dashed outline. Decorative; never carries text.

### Named Rules
**The Brown Means Matched Rule.** Wood fill on a tile says exactly one thing: this letter equals POOP's letter in this position. Never use wood fill on a tile for selection, hover or emphasis.

**The Tinted Neutral Rule.** No `#000`, `#fff` or grey. Every neutral is drawn from board green; text on coloured fields is tinted from that field.

**The One Paint Per Job Rule.** Tomato is refusal, sky is everyone else, mustard is you and first place, leaf is solved. Do not trade them between jobs.

## Typography

**Display Font:** Fredoka (with ui-rounded, system-ui)
**Body Font:** Figtree (with system-ui, -apple-system, Segoe UI)

**Character:** Fredoka is the chunky, round-cornered lettering of a board-game box and its tiles; Figtree is a plain, friendly workhorse for everything you read rather than play. Both are OFL and self-hosted from `fonts/` (latin subset, woff2, variable weight).

### Hierarchy
- **Tile** (600, 56% of the tile size, 1): ladder tiles, keys, the countdown, stats figures, rank chips.
- **Display** (600, 2.25rem, 1.1): the finish route `FUSE → POOP` and stats figures.
- **Headline** (600, 1.75rem, 1.1): sheet titles, page titles, the verdict stamp (uppercase, 0.04em).
- **Title** (600, 1.125–1.375rem, 1.1): puzzle title, sheet section titles.
- **Body** (400, 1rem, 1.45): sentences, table cells. Sentences stay short; nothing runs past 65ch.
- **Label** (600–700, 0.75–0.875rem): stat labels, table headers, hints, chips. Sentence case; no tracked uppercase labels except the Enter key and the stamp.

### Named Rules
**The Play Face Rule.** Anything a player plays with or scores (tiles, keys, steps, times, ranks) is Fredoka; anything they read is Figtree. Numerals are tabular everywhere.

## Layout

Mobile first at 390px. The play page is a fixed `100dvh` grid: masthead (3.5rem), puzzle heading, the board (scrolls on its own, ladder vertically centred while it fits, `align-content: safe center`), and the keyboard tray pinned at the bottom with safe-area padding. Tile size is `clamp(2.75rem, min(12.5vw, 6.4dvh), 3.5rem)` so a seven-rung ladder and the keyboard fit at 390×844 and 1280×800 without page scroll; longer ladders scroll inside the board.

Desktop keeps the same single centred column; the keyboard row caps at 31rem and the tray spans the width as a colour field. Pages (leaderboard, account) use a centred column (`--column-page` 46rem; forms 24rem). Sheets are bottom sheets on phones and centred dialogs from 48rem (`--column-sheet` 30rem, max 88dvh). The masthead shows icon-only links below a 40rem container width and icon plus label above it (container query).

Spacing is a 4px scale (`--space-1` … `--space-12`); groups are tight (0.25–0.75rem), sections separate at 1.25–1.5rem with a hairline.

## Elevation & Depth

Mostly flat colour fields. Depth appears in three places only: tiles and keys carry a bottom bevel (an inset edge, like a lifted game piece; never an offset block shadow), the toast floats with a soft two-step shadow, and sheets lift off the board with an upward soft shadow over a green-tinted scrim.

### Shadow Vocabulary
- **Tile bevel** (`--shadow-tile: inset 0 -3px 0 tile-edge`): tiles; matched tiles use `wood-edge`.
- **Key bevel** (`--shadow-key: inset 0 -2px 0 key-edge`): keys and secondary buttons; pressed keys drop 1px.
- **Raise** (`--shadow-raise: 0 1px 2px`): selected segment, form surface.
- **Sheet** (`--shadow-sheet`): bottom sheet; desktop dialog uses the toast shadow.
- **Toast** (`--shadow-toast`): toasts.

### Named Rules
**The Pieces Not Panels Rule.** Bevels belong to things you play with (tiles, keys, the primary button). Panels and cards stay flat.

## Shapes

Softly rounded game pieces: tiles and keys 0.5rem, controls 0.625rem, sheets 1.25rem, chips and avatars fully round, rank chips square-ish (0.375rem) like board squares. Rails are 6px (`--rail`) with dashed style for the unclimbed stretch. The verdict stamp is the one rotated shape (−4°), with a double border (3px border plus 1px outline offset 3px).

## Components

Each component maps to one server-rendered Templ part. Views are full-`<main>` morphs; state classes come from SQLite state, never from client logic.

### Masthead
- **Style:** board-green band, 3.5rem; mascot plus wordmark left; Unlimited, Leaderboard, Stats, account (avatar or "Log in") right. Current page gets `aria-current` with a mustard underline.
- **Server part:** `web/core` shell header; account slot from identity service.

### Ladder (signature)
- **Structure:** `<ol class="ladder">` of `<li class="rung">`, each with four `<span class="tile">` plus visually hidden word text. Rung kinds: `rung--start`, plain accepted rungs, `rung--input` (the only lit rung), `rung--gap` (dashed rails, empty, `aria-hidden`), `rung--goal` (dashed POOP), and `rung--reached` when the goal is reached (gap and input rows disappear).
- **Tiles:** ivory with ink; `tile--match` in wood when the letter equals POOP's at that position; input cells `tile--typed` and `tile--cursor` (wood caret); `rung--rejected` turns the input row tomato.
- **Server part:** shared Templ component in `web/core` (tiles are reused by the finish sheet's mini rungs). The server decides `tile--match`; the letters in progress come from a Datastar signal bound into the input rung.
- **Hidden calendar:** the gap rung never shows a count; par, solution and leaderboard appear only in the finish sheet.

### Keyboard
- **Style:** `surface-sunken` tray; keys in `key` with bevel, Fredoka letters; Enter in wood; backspace drawn icon with "Delete letter" label. Keys are 3–3.5rem tall and share the row width, as in Wordle.
- **Server part:** static Templ part; buttons write the letters signal and post the guess command.

### Toast
- **Style:** ink pill below the masthead, `role="status"`. Copy names the problem: "Not in word list", "Not one letter different", "Copied to clipboard".
- **Server part:** rendered from `games.last_rejection`; keyed by `rejection_seq` so a repeat rejection re-enters.

### Sheet (finish, stats)
- **Style:** `<dialog>` bottom sheet on `surface`, scrim over the board, close button top right.
- **Finish:** verdict stamp as the `h2` ("Perfect!" in wood when on par, "Solved" in leaf otherwise), route `START → POOP`, steps against par with a chip ("On par" or "+n over par"), one shortest solution as mini rungs in a 3-column grid, Share (primary) and Play unlimited, countdown to 08:00 UTC, today's leaderboard (live) with avatars, links to stats and the full leaderboard.
- **Stats:** four figures (wins, avg extra steps, current streak, best streak), extra-steps distribution with a You / Everyone segmented control (radio inputs, CSS `:has()` switch), today's bar outlined, a plain-sentence comparison.
- **Server part:** finish sheet in `web/features/play`; stats in `web/services/game` views. Share is the only Rocket component (Web Share API, clipboard fallback). The countdown ticks client-side from a server-rendered `datetime`.

### Standings and leaderboard table
- **Style:** rank chip, round avatar (32px, 256px source), name, steps, time; the player's row in `mustard-soft` with a "You" tag; first place in mustard. The page uses a real `<table>` with caption and Today / All-time tabs as links with `aria-current`.
- **Server part:** `web/features/leaderboard`; the finish sheet reuses the standings list.

### Inputs / Fields
- **Style:** 3rem fields, 2px `line-strong` border on `tile-empty`, label above, hint below in `ink-muted`; errors in tomato with a drawn alert icon and `aria-invalid`/`aria-describedby`; file input's selector button styled as a secondary button.
- **Server part:** `web/features/auth` and `web/features/account`.

### Buttons
- **Primary:** wood, `on-wood` text, 3rem tall, bevel; hover `wood-hover`; press drops 1px. One primary per view.
- **Secondary:** `key` fill with key bevel. Text links in ink, hover wood.

### Motion (all CSS-only, inside `prefers-reduced-motion: no-preference`)
- **Accept:** the newest rung (`rung--fresh`, only on the rung the latest guess created; stable ids such as `rung-4` keep earlier rungs from replaying) flips each tile in on X, 420ms, staggered 90ms left to right.
- **Reject:** the input rung wriggles (translate plus skew, 420ms) like a snake.
- **Toast:** fades in from 0.5rem above, holds 1.8s, fades out.
- **Sheet:** slides up 360ms (pops in on desktop); the verdict stamp lands after it with a scale-and-rotate.
- **Caret:** wood caret blinks on the cursor cell.
- **Reduced motion:** no flip, wriggle, slide, stamp or blink; the toast appears and disappears without transition.

## Do's and Don'ts

### Do:
- **Do** draw every ladder with its rails, start rung at the top and the POOP goal at the foot, in that order on every screen size.
- **Do** compute `tile--match` on the server from the word and POOP's letters; apply it to mini rungs in the finish sheet too.
- **Do** give every tile row hidden text naming the word and how many letters are in place; tiles themselves are `aria-hidden`.
- **Do** keep focus visible: 3px `focus` ring, `on-board` ring inside the masthead.
- **Do** label synthetic sample data (players, avatars, times) wherever it appears in prototypes.

### Don't:
- **Don't** show a step count, par or any hint in the gap rung or anywhere before the player finishes.
- **Don't** use wood fill on tiles for anything but a matched letter.
- **Don't** add gradients, glass, offset block shadows or decorative borders; regions are divided by flat colour fields.
- **Don't** put a small label above a heading; the verdict stamp is itself the heading.
- **Don't** use emoji as icons; the mascot and icons are authored SVG in one 2px round stroke.
