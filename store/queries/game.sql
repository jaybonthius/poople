-- name: FindWordList :one
SELECT id AS list_id FROM word_lists WHERE checksum = @checksum;

-- name: InsertWordList :one
-- A new version is activated as it is created.
INSERT INTO word_lists (checksum, created_at, activated_at)
VALUES (@checksum, @created_at, (SELECT max(CAST(@created_at AS INTEGER), coalesce(max(w.activated_at), 0) + 1) FROM word_lists w))
RETURNING id;

-- name: ActivateWordList :exec
-- Makes a stored version current again. activated_at stays strictly increasing
-- so versions activated within one millisecond keep their order.
UPDATE word_lists
SET activated_at = (SELECT max(CAST(@now AS INTEGER), coalesce(max(w.activated_at), 0) + 1) FROM word_lists w)
WHERE word_lists.id = @list_id;

-- name: InsertWord :exec
INSERT INTO words (list_id, word, distance, common) VALUES (@list_id, @word, @distance, @common);

-- name: InsertCalendarEntry :exec
INSERT INTO calendar_entries (list_id, position, word) VALUES (@list_id, @position, @word);

-- name: CurrentWordList :one
SELECT id AS list_id FROM word_lists ORDER BY activated_at DESC, id DESC LIMIT 1;

-- name: ListedWord :one
-- A word in the list; distance is NULL when it cannot reach POOP.
SELECT word AS listed_word, distance FROM words WHERE list_id = @list_id AND word = @word;

-- name: CountCalendarEntries :one
SELECT count(*) FROM calendar_entries WHERE list_id = @list_id;

-- name: CalendarWord :one
SELECT word FROM calendar_entries WHERE list_id = @list_id AND position = @position;

-- name: RandomUnlimitedWord :one
SELECT word FROM words
WHERE list_id = @list_id AND common = 1 AND distance >= CAST(@min_distance AS INTEGER)
ORDER BY random() LIMIT 1;

-- name: FindDailyPuzzle :one
SELECT id AS puzzle_id, list_id, start_word, par FROM puzzles WHERE kind = 'daily' AND number = CAST(@number AS INTEGER);

-- name: InsertDailyPuzzle :one
INSERT INTO puzzles (kind, number, list_id, start_word, par, created_at)
VALUES ('daily', CAST(@number AS INTEGER), @list_id, @start_word, @par, @created_at)
RETURNING id;

-- name: InsertUnlimitedPuzzle :one
INSERT INTO puzzles (kind, number, list_id, start_word, par, created_at)
VALUES ('unlimited', NULL, @list_id, @start_word, @par, @created_at)
RETURNING id;

-- name: GetPuzzle :one
SELECT kind, number, list_id, start_word, par FROM puzzles WHERE id = @puzzle_id;

-- name: CurrentUnlimitedPuzzle :one
SELECT g.puzzle_id FROM games g JOIN puzzles p ON p.id = g.puzzle_id
WHERE g.player_id = @player_id AND p.kind = 'unlimited'
ORDER BY g.puzzle_id DESC LIMIT 1;

-- name: EnsureGame :exec
INSERT INTO games (puzzle_id, player_id) VALUES (@puzzle_id, @player_id)
ON CONFLICT (puzzle_id, player_id) DO NOTHING;

-- name: GetGame :one
SELECT id AS game_id, started_at, finished_at, last_rejection, rejection_seq
FROM games WHERE puzzle_id = @puzzle_id AND player_id = @player_id;

-- name: ListGuesses :many
SELECT word, created_at FROM guesses WHERE game_id = @game_id ORDER BY position;

-- name: InsertGuess :exec
INSERT INTO guesses (game_id, position, word, created_at)
VALUES (@game_id, (SELECT count(*) + 1 FROM guesses WHERE game_id = @game_id), @word, @created_at);

-- name: AcceptGuess :exec
-- Records the clock after an accepted guess and clears the shown rejection.
UPDATE games SET started_at = @started_at, finished_at = @finished_at, last_rejection = NULL
WHERE id = @game_id;

-- name: RejectGuess :exec
UPDATE games SET last_rejection = CAST(@rejection AS TEXT), rejection_seq = rejection_seq + 1
WHERE id = @game_id;

-- name: MoveGames :exec
-- Moves from's games to the other player, except puzzles that player already has.
UPDATE games SET player_id = @to_player
WHERE games.player_id = @from_player
  AND games.puzzle_id NOT IN (SELECT kept.puzzle_id FROM games kept WHERE kept.player_id = @to_player);

-- name: DeletePlayerGames :exec
DELETE FROM games WHERE player_id = @player_id;

-- name: PuzzleStandings :many
-- Finished games on the puzzle, best first: fewest steps, then fastest solve,
-- then earliest finish. account_id is NULL for anonymous players.
SELECT g.player_id, a.id AS account_id, a.username, a.avatar_hash,
    (SELECT count(*) FROM guesses s WHERE s.game_id = g.id) AS steps,
    g.finished_at - g.started_at AS solve_time
FROM games g LEFT JOIN accounts a ON a.player_id = g.player_id
WHERE g.puzzle_id = @puzzle_id AND g.finished_at IS NOT NULL
ORDER BY steps, solve_time, g.finished_at, g.player_id;
