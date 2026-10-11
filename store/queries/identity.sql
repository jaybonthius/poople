-- name: CreatePlayer :one
INSERT INTO players (created_at) VALUES (@created_at) RETURNING id;

-- name: CountAccounts :one
SELECT count(*) FROM accounts;

-- name: CreateAccount :one
INSERT INTO accounts (player_id, username, password_hash, is_admin, created_at)
VALUES (@player_id, @username, @password_hash, @is_admin, @created_at)
RETURNING id;

-- Aliases prevent the pinned plugin selecting a disabled CRUD model.
-- name: GetAccountByUsername :one
SELECT id AS account_id, player_id, username, password_hash, is_admin, avatar_hash
FROM accounts WHERE username = @username;

-- name: GetAccountByID :one
SELECT id AS account_id, player_id, username, password_hash, is_admin, avatar_hash
FROM accounts WHERE id = @id;

-- name: GetPlayerAccount :one
SELECT id AS account_id, player_id, username, password_hash, is_admin, avatar_hash
FROM accounts WHERE player_id = @player_id;

-- name: SetAccountPassword :exec
UPDATE accounts SET password_hash = @password_hash WHERE id = @id;

-- name: CreateSession :exec
INSERT INTO sessions (token_hash, player_id, created_at, expires_at)
VALUES (@token_hash, @player_id, @created_at, @expires_at);

-- name: GetSessionPlayer :one
SELECT player_id AS session_player_id FROM sessions WHERE token_hash = @token_hash AND expires_at > @now;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = @token_hash;

-- name: DeletePlayerSessions :exec
DELETE FROM sessions WHERE player_id = @player_id;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= @now;

-- name: CreatePasswordReset :exec
INSERT INTO password_resets (token_hash, account_id, created_by, created_at, expires_at)
VALUES (@token_hash, @account_id, @created_by, @created_at, @expires_at);

-- name: GetPasswordReset :one
SELECT r.account_id AS reset_account_id, a.username
FROM password_resets r JOIN accounts a ON a.id = r.account_id
WHERE r.token_hash = @token_hash AND r.used_at IS NULL AND r.expires_at > @now;

-- name: UsePasswordReset :exec
UPDATE password_resets SET used_at = @now
WHERE token_hash = @token_hash AND used_at IS NULL AND expires_at > @now;

-- name: SetAccountAvatar :exec
UPDATE accounts SET avatar = @avatar, avatar_hash = @avatar_hash WHERE id = @id;

-- name: RemoveAccountAvatar :exec
UPDATE accounts SET avatar = NULL, avatar_hash = NULL WHERE id = @id;

-- name: GetAccountAvatar :one
SELECT avatar FROM accounts WHERE id = @id AND avatar_hash = @avatar_hash;
