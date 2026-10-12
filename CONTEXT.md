# poople

A private, server-rendered fork of [Poople](https://pooplegame.com/), the daily four-letter word ladder, for the owner and their friends. Each day the player turns a start word into POOP by changing one letter per guess; every guess must be an accepted word. The fork keeps Poople's start words in its own daily order and grows the accepted-word list, which the owner and friends find frustratingly small. [The Poople reference](docs/poople-reference.md) records the original game's lists, schedule formula and candidate word lists.

## Terms

- **Puzzle number:** 1 plus whole days since puzzle #1 opened at 2026-10-11 08:00 UTC; a new puzzle starts at 08:00 UTC. Before then it is #1; there is no #0.
- **Start word:** the puzzle's first word, from the calendar at `(puzzle number − 1) mod calendar length`.
- **Target:** always POOP.
- **Guess / step:** a four-letter accepted word differing from the previous word in exactly one position.
- **Accepted words:** every word a player may guess. Distinct from start words, which are a curated subset.
- **Ladder:** the start word followed by the player's guesses.
- **Par:** the fewest steps from the start word to POOP within the accepted words.

## Settled decisions (owner, 2026-10-11)

- Use Poople's start words and target, skipping start words whose par is below 4 in our list (1,092 remain). Staying identical to Poople is not required.
- Puzzle numbering starts at our launch, and the calendar has its own order (owner, 2026-10-12): a fixed-seed shuffle, repaired so that yesterday's shortest ladder, revealed the next day, never contains today's start word or a word one letter from it, including where the calendar wraps.
- Accepted words are Collins Scrabble Words 2024's four-letter words: 5,662 words, 5,586 reachable from POOP, covering every Poople start word. BOOP is the only Poople word lost.
- Par is recomputed from our accepted words, so it can differ from pooplegame.com's par.
- Word lists are versioned in SQLite. A puzzle freezes its list version, start word and par when first opened, so changing the list never alters past results.
- The server alone knows the calendar. The browser never receives future start words, par or solutions for unplayed puzzles. Hiding the accepted-word list is not a goal.
- Build it like the owner's other Go apps: Go, Datastar, Templ and SQLite, with CQRS full-view morphs. SQLite holds the words, the links between them, the calendar and all player state. The solver is reimplemented in Go.
- JavaScript beyond Datastar lives in Rocket web components.
- Anyone can play anonymously. Accounts are optional, password-protected and have a profile picture.
- Features beyond the daily puzzle: leaderboards, personal statistics, shareable results and an unlimited mode.
- The audience is the owner and friends. Licensing of Poople's lists is not a concern.
