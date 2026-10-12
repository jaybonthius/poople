# Build plan

Approved by the owner on 2026-10-11. Agents implement it in isolated worktrees; the orchestrator integrates. Read `AGENTS.md`, `CONTEXT.md` and the `sqlite` skill first. getajob (`/home/jay/Code/getajob`) is the primary implementation reference: copy its matching code (Taskfile, `cmd/web/bundle`, `cmd/worktree`, `lib/static`, `lib/errorlog`, `web/core`, `web/services/live`, `web/services/identity`, `web/app.go`, `cmd/web/main.go`, `app/server.go`) and adapt names, keeping license headers. Its `docs/web-server-plan.md` explains the asset, live-reload, identity and stream decisions this plan reuses.

## Layout

```text
cmd/web/            launcher: flags (--data-dir required, --listen, --secure-cookie, --ui-fps), prints the one-time admin setup link
cmd/web/bundle/     esbuild build/watch (getajob)
cmd/worktree/       per-checkout dev address (getajob)
app/                opens the store, loads word data, owns change topics and shutdown
store/              Open/Close, Read/Write, embedded Goose migrations, queries.sql, zz/ (generated)
ladder/             pure Go: word graph, BFS distances, neighbours, calendar, unlimited pool; data/ holds the source lists
lib/errorlog, lib/static
web/app.go          middleware and route composition; the only place features meet
web/core/           shell, error pages, shared Templ parts (tiles, keyboard), Rocket runtime, design tokens
web/services/identity/  players, sessions, setup, sign-up, login, password-reset links, avatars
web/services/live/      full-<main> SSE stream capped by --ui-fps (getajob)
web/services/game/      guess/start commands, game views, stats, leaderboards
web/features/play/        daily puzzle and finish sheet
web/features/unlimited/
web/features/leaderboard/
web/features/auth/        setup, sign up, log in, log out, reset-link use
web/features/account/     profile picture, password change
web/features/admin/       users: reset link, remove picture or account
```

Features never import each other; shared behaviour is a service passed in from `web/app.go`. `ladder` imports nothing from the app. Module path: `poople`.

## Store

`store.Open(ctx, dataDir)` runs embedded Goose migrations on a `database/sql` + `modernc.org/sqlite` connection, closes it, then opens `github.com/delaneyj/toolbelt/db.Database` (one write connection, CPU-sized read pool) on `poople.sqlite`. Follow northstar's `lib/db/db.go` `InitDatabase` shape and read back the pragmas (WAL, `synchronous = NORMAL`, `foreign_keys = ON`, busy timeout). No `application_id` ownership check: toolbelt's built-in migrator requires `application_id = 0`, and the data directory is dedicated to poople. Expose `Read(ctx, func(*sqlite.Conn) error)` and `Write(ctx, func(*sqlite.Conn) error)` (immediate transaction). One migration history in `store/migrations`; one `store/queries.sql` per concern is fine (`store/queries/*.sql`); generated code only in `store/zz`.

## Word data (versioned)

Source files are tracked in `ladder/data/` and embedded: `csw24-four-letter.txt` (5,662 words), `poople-calendar.txt` (Poople's 1,137 start words in order), `poople-common.txt` (Poople's 2,749-word frequency list). Provenance: `docs/poople-reference.md`.

At startup `app` hashes the three files. If the checksum is new it computes everything in Go and commits one transaction:

- `word_lists(id, checksum UNIQUE, created_at)`; the newest row is current.
- `words(list_id, word, distance, common)`, `PRIMARY KEY(list_id, word)`, `WITHOUT ROWID`. `distance` is the BFS distance to POOP, NULL when unreachable. `common` marks membership in the frequency list.
- `calendar_entries(list_id, position, word)`: Poople's calendar order, keeping only words with distance ≥ 4 in this list (1,092 entries for CSW24).

Neighbours are generated in Go (each position × 26 letters) and checked against `words`; no links table.

`puzzles(id, kind 'daily'|'unlimited', number, list_id, start_word, par, created_at)`, unique `(kind, number)` for daily. A puzzle row is created the first time anyone opens it and never changes: it freezes the list version, start word and par. Games, guesses, stats and leaderboards reference `puzzles`, never the current list. Daily puzzle `n` = whole days since 2025-08-14 08:00 UTC; its start word is `calendar_entries[n mod count]` of the current list. Unlimited puzzles pick a random current-list word that is `common` with distance ≥ 4 (2,201 candidates).

Expected values for tests (CSW24): 5,586 words reachable from POOP; BOOP absent; daily #0 is DAWN (par 4); daily #423 (2026-10-11 08:00 UTC to 2026-10-12 08:00 UTC) is FUSE (par 5); TIPS has distance 4.

## Migration numbers

00001 app_meta (skeleton), 00002 players (shared), 00003 identity (accounts, sessions, reset links), 00004 word data and games (word_lists, words, calendar_entries, puzzles, games, guesses). Later lanes take the next free number at integration.

## Players and games

Every visitor who acts is a player (`players`, migration 00002), identified through a session cookie: a random opaque token stored only as a hash. Player rows are created on a player's first command, not on page views. An account (`accounts`: username, Argon2id hash, avatar) attaches to a player. Signing up keeps the current player's history. Logging in moves this browser's anonymous games into the account except puzzles the account already has, then the anonymous player is retired. Sessions follow getajob identity: opaque tokens, hashes stored, expiry, revocation on logout, host-only HttpOnly SameSite=Lax cookies, `--secure-cookie`, cross-origin protection, bounded bodies, rate-limited login. The first account comes from the one-time `/setup?token=…` link printed at startup when no account exists; it is the admin. Sign-up is otherwise open. The admin can issue a single-use password-reset link (stored hashed, 24-hour expiry, like getajob invites) and remove an avatar or account.

`games(id, puzzle_id, player_id, started_at, finished_at, last_rejection, rejection_seq)`, unique `(puzzle_id, player_id)`. `guesses(game_id, position, word, created_at)`. Steps = guesses after the start word; solve time = first guess to the guess reaching POOP. Avatars: upload limit 2 MiB, PNG/JPEG/GIF/WebP accepted, decoded, centre-cropped and resized to 256 px with `golang.org/x/image/draw`, re-encoded PNG, stored as a BLOB with a content hash for caching.

## Commands and views (CQRS)

Pages render the shell; `data-init` opens `GET /<page>/state`, a `live.Stream` that sends the full `<main>` on connect and after each notification. Commands are POSTs that validate, commit in one `Write`, notify the topic and return 204; they never render. Rejections ("Not in word list", "Not one letter different") are stored on the game (`last_rejection`, incremented `rejection_seq`) so the view shows them and shakes the row keyed by the sequence. Letters in progress are a Datastar signal fed by `data-on:keydown__window` and on-screen key buttons; the view clears the signal when the ladder grows. Topics: one per puzzle (guesses and leaderboard) plus one per player (stats). Views read SQLite every render.

The browser receives only the current puzzle's start word and the player's own ladder. Par, one shortest solution and the leaderboard appear after the player finishes. Tests assert that no future start word, unplayed par or solution reaches a response.

## Screens

- **Play (`/`):** puzzle number and start word, ladder rows of four tiles (a tile is highlighted when its letter matches POOP in that position, as in Poople), current input row, on-screen keyboard, toast for rejections. Finish sheet: "Perfect!" or "Solved", start → POOP, steps against par, one shortest solution, share, countdown to 08:00 UTC, today's leaderboard (live).
- **Leaderboard (`/leaderboard`):** Today (steps, then solve time) and All-time (current streak, best streak, average steps over par with at least 5 games). Today ranks every finisher (steps, then solve time, dense ranks); accounts show name and avatar, others show as Anonymous; top 20 plus the viewer's row. Spoiler rules: before the viewer finishes today, par marks, the Everyone distribution and today's contribution to all-time averages stay hidden; words are never shown.
- **Stats:** wins, average extra steps, current and best streak, extra-steps distribution for you and for everyone today.
- **Unlimited (`/unlimited`):** same play surface, new random puzzle on request, no leaderboard.
- **Account:** sign up, log in, log out, profile picture, password change. **Admin (`/admin/users`):** list, reset link, remove picture or account.

Share is the only Rocket component: copy an emoji summary, using the Web Share API when available. Everything else is Datastar.

## Design

`PRODUCT.md` holds product truth; the design lane writes `DESIGN.md` through Impeccable new-work. Mobile first, Poople-recognisable (tiles, poop-brown accent, bottom keyboard), Wordle-grade polish: toasts, tile flip on accept, shake on rejection, stats sheet, light and dark themes, reduced motion respected. Self-host fonts; no CDN.

## Waves

1. **Skeleton** (A), **ladder** (B) and **design** (C) in parallel.
2. **Identity** (players, sessions, setup, sign-up, login, reset links, avatars, admin service) and **puzzles** (word-data loader, puzzles, games, guess command, game service) in parallel.
3. **Play** feature with the finish sheet, using the design.
4. **Stats and share**, **leaderboards**, **unlimited**, **account and admin** screens in parallel.
5. Impeccable polish and desktop/phone QA, then independent review.

Each lane: one writer in its own worktree, `task check` and `task race` green (once the skeleton exists), tests for plausible failures, a handoff listing changed files, validation and open questions. Lanes do not commit; the orchestrator integrates.
