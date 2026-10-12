-- name: PuzzleStandings :many
-- Finished games on the puzzle, best first: fewest steps, then fastest solve,
-- then earliest finish. account_id is NULL for anonymous players.
SELECT g.player_id, a.id AS account_id, a.username, a.avatar_hash,
    (SELECT count(*) FROM guesses s WHERE s.game_id = g.id) AS steps,
    g.finished_at - g.started_at AS solve_time
FROM games g LEFT JOIN accounts a ON a.player_id = g.player_id
WHERE g.puzzle_id = @puzzle_id AND g.finished_at IS NOT NULL
ORDER BY steps, solve_time, g.finished_at, g.player_id;

-- name: PlayerDailyGames :many
-- The player's daily games with at least one accepted guess, oldest puzzle
-- first, with the par each puzzle froze.
SELECT coalesce(p.number, 0) AS puzzle_number, p.par,
    g.finished_at IS NOT NULL AS finished,
    (SELECT count(*) FROM guesses s WHERE s.game_id = g.id) AS steps
FROM games g JOIN puzzles p ON p.id = g.puzzle_id
WHERE g.player_id = @player_id AND p.kind = 'daily' AND g.started_at IS NOT NULL
ORDER BY p.number;

-- name: AccountDailyFinishes :many
-- Every finished daily game of every account holder, grouped by account and
-- oldest puzzle first, with the par each puzzle froze.
SELECT a.player_id, a.id AS account_id, a.username, a.avatar_hash,
    coalesce(p.number, 0) AS puzzle_number, p.par,
    (SELECT count(*) FROM guesses s WHERE s.game_id = g.id) AS steps
FROM accounts a
JOIN games g ON g.player_id = a.player_id
JOIN puzzles p ON p.id = g.puzzle_id
WHERE g.finished_at IS NOT NULL AND p.kind = 'daily'
ORDER BY a.id, p.number;
