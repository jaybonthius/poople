-- +goose Up
-- An optional password-protected account attached to one player. Usernames are
-- ASCII, kept as typed and unique regardless of case.
CREATE TABLE accounts (
    id INTEGER PRIMARY KEY,
    player_id INTEGER NOT NULL UNIQUE REFERENCES players(id),
    username TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT NOT NULL,
    is_admin INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
    avatar BLOB, -- 256 px PNG
    avatar_hash TEXT, -- hex SHA-256 of avatar, part of its cacheable URL
    created_at INTEGER NOT NULL, -- unix milliseconds
    CHECK ((avatar IS NULL) = (avatar_hash IS NULL))
);

-- A browser's session cookie, stored only as the SHA-256 of its random token.
CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    player_id INTEGER NOT NULL REFERENCES players(id),
    created_at INTEGER NOT NULL, -- unix milliseconds
    expires_at INTEGER NOT NULL -- unix milliseconds
) WITHOUT ROWID;
CREATE INDEX sessions_player_id ON sessions(player_id);
CREATE INDEX sessions_expires_at ON sessions(expires_at);

-- Single-use links an admin issues so an account can set a new password.
CREATE TABLE password_resets (
    token_hash TEXT PRIMARY KEY,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    created_by INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL, -- unix milliseconds
    expires_at INTEGER NOT NULL, -- unix milliseconds
    used_at INTEGER -- unix milliseconds
) WITHOUT ROWID;
CREATE INDEX password_resets_account_id ON password_resets(account_id);
CREATE INDEX password_resets_created_by ON password_resets(created_by);

-- +goose Down
DROP TABLE password_resets;
DROP TABLE sessions;
DROP TABLE accounts;
