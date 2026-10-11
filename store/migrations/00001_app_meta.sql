-- +goose Up
-- Facts about the database itself, keyed by name.
CREATE TABLE app_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
) WITHOUT ROWID;
INSERT INTO app_meta(key, value) VALUES ('app', 'poople');

-- +goose Down
DROP TABLE app_meta;
