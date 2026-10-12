# Poople reference

Observed facts about the original game, gathered 2026-10-11 from pooplegame.com and poople.io. Read before changing word lists, the daily schedule or par. Bundle names change with every deploy; re-extract rather than trusting the paths.

## Sources

- `../pooplegame` is the MIT `poople` npm engine (TypeScript BFS solver). It ships no word list; its only list is 20 demo words.
- pooplegame.com (Next.js) embeds the lists in chunk `/_next/static/chunks/6080-391ff6b80303964d.js`, webpack module `55046`. Schedule code: module `75719` (chunk `8157-…`); constants: module `87643`.
- poople.io (Vite) embeds the same lists in `/assets/index-CbbZj1rp.js` as template literals `xE`, `FE` and `D0` with `\r\n` line endings.

To re-extract: fetch the page, download every script it loads (pooplegame lazy-loads chunks, so take the list from browser network requests), and search for `"POOP,0` and `"PICK,5`.

## Lists

| List | Rows | Row format | Meaning |
|---|---|---|---|
| Accepted words (`S` / `xE`) | 2,398 | `WORD,distance` | The only valid guesses. Distance is the shortest ladder length to POOP. Identical on both sites. |
| Start words (`R` / `FE`) | 1,137 | `WORD,par` | The daily calendar, in order. Par is the shortest ladder to POOP in the accepted list. |
| Frequency list (`D0`, poople.io only) | 2,749 | `WORD,count` | Common four-letter web words with corpus counts. |

The accepted list is exactly the subset of the frequency list connected to POOP by one-letter steps (verified). Common words outside that component are rejected, for example: also, into, used, high, able, sign, once, upon, edit, exit, iron, okay, ugly, unto. This is the main reason the list feels small.

The target is always POOP (`bP = "POOP"`, length 4).

## Daily schedule

pooplegame.com:

```js
epoch = Date.UTC(2025, 7, 14, 8, 0, 0)        // 2025-08-14 08:00 UTC
n     = now <= epoch ? 0 : floor((now - epoch) / 86_400_000)
start = START_WORDS[((n % 1137) + 1137) % 1137]
```

A new puzzle starts at 08:00 UTC; the page shows `#n`. The calendar wraps to the first word on 2028-09-24. Verified: 2026-10-11 is #423 TIPS on the live site.

Our fork does not follow this schedule: its numbering starts at its own launch and its calendar has its own order (see `CONTEXT.md`).

poople.io counts local-time days back to 2025-08-15 08:00 UTC and indexes without wrapping (`undefined` after the list ends). It agrees with pooplegame.com except around DST changes and after the list runs out.

## Candidate accepted-word lists

Four-letter words reachable from POOP in each list, checked 2026-10-11. Frequency order comes from Norvig's `count_1w.txt`.

| List | Reachable | Poople words missing | Start words missing |
|---|---|---|---|
| Collins Scrabble Words 2024 (CSW24) | 5,586 | 1 (boop) | 0 |
| Collins Scrabble Words 2021 (CSW21) | 5,545 | 1 (boop) | 0 |
| NASPA Word List 2023 (NWL2023) | 4,150 | 32 (incl. xray, woah, sars, goss) | 1 (xray) |
| SCOWL 80 (Debian `wamerican-huge` 2020.12.07, lowercase entries) | 5,124 | 40 (incl. gifs, vlog, noob, nano) | 5 (cafe, gyno, nano, tyre, xray) |
| SCOWL 70 (`wamerican-large`) | 3,311 | 139 | 24 |
| ENABLE (`enable1.txt`) | 3,807 | 98 | 19 |
| SCOWL 50 (`wamerican`) | 2,297 | 330 | 64 |

CSW24 additions over Poople range from common (also, into, used, high, able, edit, exit, okay, iron, onto, euro) through middling (agog, doff, wynd, kobo) to obscure (cwms, fohn, fozy, knar, caph, wems). Slang from the 2024 edition is included: yeet, bruh, stan, welp, derp, nerf. Words in no list: yolo, and isolated words with no one-letter neighbours such as evil.

CSW24 is the chosen list (owner, 2026-10-11): 5,662 four-letter words from Scrabble New Zealand's [CSW24 2, 3 and 4 letter word lists](https://scrabble.org.nz/wp-content/uploads/2025/02/csw24-234s.pdf), extracted with `pdftotext -layout` and the `[A-Z]{4}` tokens after the "CSW24 4 letter words" heading. It matches CSW21 plus WESPA's [new CSW24 words](https://wespa.org/docs/CSW24CompleteNewWords.pdf) except that it adds faps, folx and ints and drops alfs (one of the 2024 removals).

Other sources: CSW21 and NWL2023 text files from [scrabblewords/scrabblewords](https://github.com/scrabblewords/scrabblewords) (`words/British/CSW21.txt`, `words/North-American/NWL2023.txt`); SCOWL lists from Debian's `scowl` 2020.12.07-2 packages.
