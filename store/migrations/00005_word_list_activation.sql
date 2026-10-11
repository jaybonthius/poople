-- +goose Up
-- The current word list is the most recently activated one, so data that
-- reverts to an earlier checksum makes that version current again.
ALTER TABLE word_lists ADD COLUMN activated_at INTEGER NOT NULL DEFAULT 0; -- unix milliseconds
UPDATE word_lists SET activated_at = created_at;

-- +goose Down
ALTER TABLE word_lists DROP COLUMN activated_at;
