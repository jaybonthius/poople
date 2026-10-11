-- +goose Up
-- One version of the word data, identified by the checksum of its source files.
-- The newest row (highest id) is current; puzzles keep the version they froze.
CREATE TABLE word_lists (
    id INTEGER PRIMARY KEY,
    checksum TEXT NOT NULL UNIQUE,
    created_at INTEGER NOT NULL -- unix milliseconds
);

-- Accepted words of a version. distance is the fewest steps to POOP, NULL when
-- unreachable; common marks membership in Poople's frequency list.
CREATE TABLE words (
    list_id INTEGER NOT NULL REFERENCES word_lists(id),
    word TEXT NOT NULL CHECK (length(word) = 4),
    distance INTEGER CHECK (distance >= 0),
    common INTEGER NOT NULL CHECK (common IN (0, 1)),
    PRIMARY KEY (list_id, word)
) WITHOUT ROWID;

-- Daily start words of a version in Poople's calendar order, from position 0.
CREATE TABLE calendar_entries (
    list_id INTEGER NOT NULL,
    position INTEGER NOT NULL CHECK (position >= 0),
    word TEXT NOT NULL,
    PRIMARY KEY (list_id, position),
    FOREIGN KEY (list_id, word) REFERENCES words(list_id, word)
) WITHOUT ROWID;

-- A puzzle freezes its word list, start word and par when first opened and never changes.
-- Daily puzzles are numbered; unlimited puzzles have no number.
CREATE TABLE puzzles (
    id INTEGER PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('daily', 'unlimited')),
    number INTEGER CHECK ((kind = 'daily') = (number IS NOT NULL)),
    list_id INTEGER NOT NULL,
    start_word TEXT NOT NULL,
    par INTEGER NOT NULL CHECK (par > 0),
    created_at INTEGER NOT NULL, -- unix milliseconds
    UNIQUE (kind, number),
    FOREIGN KEY (list_id, start_word) REFERENCES words(list_id, word)
);

-- One player's attempt at one puzzle. started_at is the first accepted guess,
-- finished_at the guess reaching POOP. The latest rejected guess is kept so the
-- view can show it; rejection_seq counts rejections.
CREATE TABLE games (
    id INTEGER PRIMARY KEY,
    puzzle_id INTEGER NOT NULL REFERENCES puzzles(id),
    player_id INTEGER NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    started_at INTEGER, -- unix milliseconds
    finished_at INTEGER, -- unix milliseconds
    last_rejection TEXT CHECK (last_rejection IN ('not-in-list', 'not-one-letter', 'incomplete')),
    rejection_seq INTEGER NOT NULL DEFAULT 0,
    UNIQUE (puzzle_id, player_id)
);
CREATE INDEX games_player ON games(player_id, puzzle_id);

-- Accepted guesses of a game; position 1 is the first guess after the start word.
CREATE TABLE guesses (
    game_id INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position > 0),
    word TEXT NOT NULL,
    created_at INTEGER NOT NULL, -- unix milliseconds
    PRIMARY KEY (game_id, position)
) WITHOUT ROWID;

-- +goose Down
DROP TABLE guesses;
DROP TABLE games;
DROP TABLE puzzles;
DROP TABLE calendar_entries;
DROP TABLE words;
DROP TABLE word_lists;
