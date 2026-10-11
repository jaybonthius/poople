-- Account settings and admin user management.

-- name: DeleteOtherPlayerSessions :exec
DELETE FROM sessions WHERE player_id = @player_id AND token_hash <> @token_hash;

-- Games played counts games with at least one accepted guess.
-- name: ListAccounts :many
SELECT a.id AS listed_account_id, a.player_id, a.username, a.is_admin, a.avatar_hash, a.created_at,
    (SELECT count(*) FROM games g WHERE g.player_id = a.player_id AND g.started_at IS NOT NULL) AS games_played
FROM accounts a
ORDER BY a.username;

-- name: DeleteAccount :exec
DELETE FROM accounts WHERE id = @id;

-- name: GetIssuedPasswordReset :one
SELECT r.account_id AS issued_account_id, r.created_by
FROM password_resets r
WHERE r.token_hash = @token_hash AND r.used_at IS NULL AND r.expires_at > @now;
