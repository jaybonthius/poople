-- +goose Up
-- Everyone who has played, signed in or not. Accounts and games refer to a player.
CREATE TABLE players (
    id INTEGER PRIMARY KEY,
    created_at INTEGER NOT NULL -- unix milliseconds
);

-- +goose Down
DROP TABLE players;
