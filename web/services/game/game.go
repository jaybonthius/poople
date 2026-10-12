// Package game owns puzzles, games and guesses: the guess and start commands,
// game views and their change topics. A puzzle's word list, start word and par
// are frozen when the puzzle is first opened; everything here reads the
// puzzle's own list, never the current one.
package game

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"poople/app"
	"poople/ladder"
	"poople/store"
	"poople/store/zz"

	"zombiezen.com/go/sqlite"
)

// Kind tells daily puzzles from unlimited ones.
type Kind string

const (
	Daily     Kind = "daily"
	Unlimited Kind = "unlimited"
)

// Rejection is why the latest guess was refused.
type Rejection string

const (
	NotInList    Rejection = "not-in-list"
	NotOneLetter Rejection = "not-one-letter"
	Incomplete   Rejection = "incomplete"
)

// Message is the toast text for the rejection, "" for none.
func (r Rejection) Message() string {
	switch r {
	case NotInList:
		return "Not in word list"
	case NotOneLetter:
		return "Not one letter different"
	case Incomplete:
		return "Not enough letters"
	}
	return ""
}

var (
	ErrPuzzleNotFound = errors.New("puzzle not found")
	ErrGameFinished   = errors.New("game already finished")
	// ErrPuzzleClosed refuses guesses on a daily puzzle after the next one opens.
	ErrPuzzleClosed = errors.New("daily puzzle closed")
)

// Puzzle is what any player may see of a puzzle before finishing it; par and
// solutions are only in a finished Game.
type Puzzle struct {
	ID        int64
	Kind      Kind
	Number    int // daily puzzle number; 0 for unlimited
	StartWord string
}

// Game is one player's view of a puzzle.
type Game struct {
	Puzzle       Puzzle
	Ladder       []string  // start word, then accepted guesses
	Rejection    Rejection // latest refused guess since the last accepted one, or ""
	RejectionSeq int64     // increments on every refused guess
	Finished     bool
	Steps        int           // accepted guesses
	SolveTime    time.Duration // first guess to the guess reaching POOP
	Par          int           // 0 until finished
	Solution     []string      // one shortest ladder; nil until finished, and while a daily puzzle is today's
}

// Service runs game commands and views against the store and notifies the
// backend's topics after each commit.
type Service struct {
	db      *store.Store
	puzzles *app.Topic
	players *app.Topic
	now     func() time.Time
}

// New borrows the backend's store and topics. now is the clock (time.Now in production).
func New(backend *app.Backend, now func() time.Time) *Service {
	return &Service{db: backend.Database(), puzzles: backend.Puzzles(), players: backend.Players(), now: now}
}

// PuzzleChanges closes after the next committed guess on the puzzle. Take it
// before reading the view.
func (s *Service) PuzzleChanges(puzzleID int64) <-chan struct{} { return s.puzzles.Changes(puzzleID) }

// PlayerChanges closes after the next committed change to the player's games.
// Take it before reading the view.
func (s *Service) PlayerChanges(playerID int64) <-chan struct{} { return s.players.Changes(playerID) }

// DailyPuzzle returns today's puzzle, creating it from the current word list
// the first time anyone opens it.
func (s *Service) DailyPuzzle(ctx context.Context) (Puzzle, error) {
	return s.dailyPuzzle(ctx, ladder.DailyNumber(s.now()))
}

// Answer is a closed daily puzzle's best route: its frozen par and one
// shortest ladder against its frozen list.
type Answer struct {
	Puzzle   Puzzle
	Par      int
	Solution []string // start word first, POOP last
}

// YesterdayAnswer returns the daily puzzle before today's with its par and one
// shortest ladder, creating that puzzle from the current word list if nobody
// opened it. It returns false while today's puzzle is #0. It never reads
// today's or a later puzzle.
func (s *Service) YesterdayAnswer(ctx context.Context) (Answer, bool, error) {
	number := ladder.DailyNumber(s.now()) - 1
	if number < 0 {
		return Answer{}, false, nil
	}
	puzzle, err := s.dailyPuzzle(ctx, number)
	if err != nil {
		return Answer{}, false, err
	}
	answer := Answer{Puzzle: puzzle}
	err = s.db.Read(ctx, func(conn *sqlite.Conn) error {
		row, err := zz.OnceGetPuzzle(conn, puzzle.ID)
		if err != nil {
			return err
		}
		if row == nil {
			return ErrPuzzleNotFound
		}
		answer.Par = int(row.Par)
		answer.Solution, err = shortestPath(conn, row.ListId, row.StartWord, row.Par)
		return err
	})
	if err != nil {
		return Answer{}, false, err
	}
	return answer, true, nil
}

// dailyPuzzle returns daily puzzle number, creating it from the current word
// list the first time anyone opens it.
func (s *Service) dailyPuzzle(ctx context.Context, number int) (Puzzle, error) {
	puzzle := Puzzle{Kind: Daily, Number: number}
	var found *zz.FindDailyPuzzleRes
	if err := s.db.Read(ctx, func(conn *sqlite.Conn) (err error) {
		found, err = zz.OnceFindDailyPuzzle(conn, int64(number))
		return err
	}); err != nil {
		return puzzle, err
	}
	if found != nil {
		puzzle.ID, puzzle.StartWord = found.PuzzleId, found.StartWord
		return puzzle, nil
	}
	createdAt := s.now().UnixMilli()
	err := s.db.Write(ctx, func(conn *sqlite.Conn) error {
		found, err := zz.OnceFindDailyPuzzle(conn, int64(number))
		if err != nil {
			return err
		}
		if found != nil { // another request created it first
			puzzle.ID, puzzle.StartWord = found.PuzzleId, found.StartWord
			return nil
		}
		listID, err := currentList(conn)
		if err != nil {
			return err
		}
		count, err := zz.OnceCountCalendarEntries(conn, listID)
		if err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("word list %d has no calendar", listID)
		}
		start, err := zz.OnceCalendarWord(conn, zz.CalendarWordParams{ListId: listID, Position: int64(number) % count})
		if err != nil {
			return err
		}
		par, err := distance(conn, listID, start)
		if err != nil {
			return err
		}
		puzzle.StartWord = start
		puzzle.ID, err = zz.OnceInsertDailyPuzzle(conn, zz.InsertDailyPuzzleParams{
			Number: int64(number), ListId: listID, StartWord: start, Par: par, CreatedAt: createdAt,
		})
		return err
	})
	return puzzle, err
}

// StartUnlimited creates a puzzle from a random common word at least
// ladder.MinStartDistance from POOP in the current list and makes it the
// player's current unlimited game.
func (s *Service) StartUnlimited(ctx context.Context, playerID int64) (Puzzle, error) {
	puzzle := Puzzle{Kind: Unlimited}
	createdAt := s.now().UnixMilli()
	err := s.db.Write(ctx, func(conn *sqlite.Conn) error {
		listID, err := currentList(conn)
		if err != nil {
			return err
		}
		start, err := zz.OnceRandomUnlimitedWord(conn, zz.RandomUnlimitedWordParams{ListId: listID, MinDistance: ladder.MinStartDistance})
		if err != nil {
			return err
		}
		if start == "" {
			return fmt.Errorf("word list %d has no unlimited start word", listID)
		}
		par, err := distance(conn, listID, start)
		if err != nil {
			return err
		}
		puzzle.StartWord = start
		if puzzle.ID, err = zz.OnceInsertUnlimitedPuzzle(conn, zz.InsertUnlimitedPuzzleParams{
			ListId: listID, StartWord: start, Par: par, CreatedAt: createdAt,
		}); err != nil {
			return err
		}
		return zz.OnceEnsureGame(conn, zz.EnsureGameParams{PuzzleId: puzzle.ID, PlayerId: playerID})
	})
	if err != nil {
		return Puzzle{}, err
	}
	s.players.Notify(playerID)
	return puzzle, nil
}

// CurrentUnlimited returns the player's latest unlimited puzzle, and false
// when the player has never started one.
func (s *Service) CurrentUnlimited(ctx context.Context, playerID int64) (Puzzle, bool, error) {
	puzzle := Puzzle{Kind: Unlimited}
	err := s.db.Read(ctx, func(conn *sqlite.Conn) error {
		id, err := zz.OnceCurrentUnlimitedPuzzle(conn, playerID)
		if err != nil || id == 0 {
			return err
		}
		row, err := zz.OnceGetPuzzle(conn, id)
		if err != nil {
			return err
		}
		puzzle.ID, puzzle.StartWord = id, row.StartWord
		return nil
	})
	return puzzle, puzzle.ID != 0, err
}

// Guess validates word against the puzzle's frozen list and the player's
// ladder, then either appends it or records the rejection. It returns the
// rejection, "" when accepted. Re-submitting the ladder's last word changes
// nothing, so a repeated Enter is harmless. Unlimited puzzles accept guesses
// only from the player who started them; daily puzzles only while they are
// today's (ErrPuzzleClosed).
func (s *Service) Guess(ctx context.Context, playerID, puzzleID int64, word string) (Rejection, error) {
	word = strings.ToLower(word)
	now := s.now()
	at := now.UnixMilli()
	var rejection Rejection
	changed := true
	err := s.db.Write(ctx, func(conn *sqlite.Conn) error {
		puzzle, err := zz.OnceGetPuzzle(conn, puzzleID)
		if err != nil {
			return err
		}
		if puzzle == nil {
			return ErrPuzzleNotFound
		}
		if Kind(puzzle.Kind) == Daily && (puzzle.Number == nil || int(*puzzle.Number) != ladder.DailyNumber(now)) {
			return ErrPuzzleClosed
		}
		key := zz.GetGameParams{PuzzleId: puzzleID, PlayerId: playerID}
		if Kind(puzzle.Kind) != Unlimited {
			if err := zz.OnceEnsureGame(conn, zz.EnsureGameParams(key)); err != nil {
				return err
			}
		}
		game, err := zz.OnceGetGame(conn, key)
		if err != nil {
			return err
		}
		if game == nil {
			return ErrPuzzleNotFound
		}
		if game.FinishedAt != nil {
			return ErrGameFinished
		}
		guesses, err := zz.OnceListGuesses(conn, game.GameId)
		if err != nil {
			return err
		}
		last := puzzle.StartWord
		if len(guesses) > 0 {
			last = guesses[len(guesses)-1].Word
		}
		if word == last {
			changed = false
			return nil
		}
		listed, err := zz.OnceListedWord(conn, zz.ListedWordParams{ListId: puzzle.ListId, Word: word})
		if err != nil {
			return err
		}
		switch {
		case len(word) < len(ladder.Target):
			rejection = Incomplete
		case listed == nil:
			rejection = NotInList
		case !ladder.OneLetterApart(last, word):
			rejection = NotOneLetter
		}
		if rejection != "" {
			return zz.OnceRejectGuess(conn, zz.RejectGuessParams{Rejection: string(rejection), GameId: game.GameId})
		}
		if err := zz.OnceInsertGuess(conn, zz.InsertGuessParams{GameId: game.GameId, Word: word, CreatedAt: at}); err != nil {
			return err
		}
		accepted := zz.AcceptGuessParams{StartedAt: game.StartedAt, GameId: game.GameId}
		if accepted.StartedAt == nil {
			accepted.StartedAt = &at
		}
		if word == ladder.Target {
			accepted.FinishedAt = &at
		}
		return zz.OnceAcceptGuess(conn, accepted)
	})
	if err != nil {
		return "", err
	}
	if changed {
		s.puzzles.Notify(puzzleID)
		s.players.Notify(playerID)
	}
	return rejection, nil
}

// GameView reads the player's game on the puzzle. Par is included only once
// the player has finished, and a shortest solution too unless the puzzle is
// still today's daily puzzle: a daily answer waits for YesterdayAnswer.
func (s *Service) GameView(ctx context.Context, playerID, puzzleID int64) (Game, error) {
	var view Game
	err := s.db.Read(ctx, func(conn *sqlite.Conn) error {
		puzzle, err := zz.OnceGetPuzzle(conn, puzzleID)
		if err != nil {
			return err
		}
		if puzzle == nil {
			return ErrPuzzleNotFound
		}
		view.Puzzle = Puzzle{ID: puzzleID, Kind: Kind(puzzle.Kind), StartWord: puzzle.StartWord}
		if puzzle.Number != nil {
			view.Puzzle.Number = int(*puzzle.Number)
		}
		view.Ladder = []string{puzzle.StartWord}
		game, err := zz.OnceGetGame(conn, zz.GetGameParams{PuzzleId: puzzleID, PlayerId: playerID})
		if err != nil || game == nil {
			return err
		}
		guesses, err := zz.OnceListGuesses(conn, game.GameId)
		if err != nil {
			return err
		}
		for _, guess := range guesses {
			view.Ladder = append(view.Ladder, guess.Word)
		}
		view.Steps = len(guesses)
		if game.LastRejection != nil {
			view.Rejection = Rejection(*game.LastRejection)
		}
		view.RejectionSeq = game.RejectionSeq
		if game.FinishedAt == nil {
			return nil
		}
		view.Finished = true
		if game.StartedAt != nil {
			view.SolveTime = time.Duration(*game.FinishedAt-*game.StartedAt) * time.Millisecond
		}
		view.Par = int(puzzle.Par)
		if Kind(puzzle.Kind) == Daily && view.Puzzle.Number >= ladder.DailyNumber(s.now()) {
			return nil
		}
		view.Solution, err = shortestPath(conn, puzzle.ListId, puzzle.StartWord, puzzle.Par)
		return err
	})
	return view, err
}

// MergePlayers moves from's games to the player to inside the caller's
// transaction (identity's log-in Write). Where both played a puzzle, to keeps
// its game unless that game has no accepted guess; from's duplicates are
// deleted. Call the returned function after commit: it wakes views of both
// players and of every puzzle from played.
func (s *Service) MergePlayers(conn *sqlite.Conn, from, to int64) (func(), error) {
	if from == to {
		return func() {}, nil
	}
	puzzles, err := zz.OncePlayerPuzzles(conn, from)
	if err != nil {
		return nil, err
	}
	if err := zz.OnceDeleteUnstartedGamesOverlapping(conn, zz.DeleteUnstartedGamesOverlappingParams{ToPlayer: to, FromPlayer: from}); err != nil {
		return nil, err
	}
	if err := zz.OnceMoveGames(conn, zz.MoveGamesParams{ToPlayer: to, FromPlayer: from}); err != nil {
		return nil, err
	}
	if err := zz.OnceDeletePlayerGames(conn, from); err != nil {
		return nil, err
	}
	return func() {
		for _, puzzleID := range puzzles {
			s.puzzles.Notify(puzzleID)
		}
		s.players.Notify(from)
		s.players.Notify(to)
	}, nil
}

// Now is the service's clock, which decides the daily puzzle.
func (s *Service) Now() time.Time { return s.now() }

func currentList(conn *sqlite.Conn) (int64, error) {
	listID, err := zz.OnceCurrentWordList(conn)
	if err == nil && listID == 0 {
		err = errors.New("no word list loaded")
	}
	return listID, err
}

// distance returns word's distance to POOP in the list; word must reach it.
func distance(conn *sqlite.Conn, listID int64, word string) (int64, error) {
	listed, err := zz.OnceListedWord(conn, zz.ListedWordParams{ListId: listID, Word: word})
	if err != nil {
		return 0, err
	}
	if listed == nil || listed.Distance == nil {
		return 0, fmt.Errorf("word %q cannot reach %s in list %d", word, ladder.Target, listID)
	}
	return *listed.Distance, nil
}

// shortestPath follows ladder.ShortestPath's rule against the list in SQLite:
// from start, which is dist steps from POOP, take the alphabetically first
// one-letter neighbour one step closer.
func shortestPath(conn *sqlite.Conn, listID int64, start string, dist int64) ([]string, error) {
	path := []string{start}
	lookup := zz.ListedWord(conn)
	for word := start; dist > 0; dist-- {
		next := ""
		for _, candidate := range oneLetterChanges(word) {
			listed, err := lookup.Run(zz.ListedWordParams{ListId: listID, Word: candidate})
			if err != nil {
				return nil, err
			}
			if listed != nil && listed.Distance != nil && *listed.Distance == dist-1 {
				next = candidate
				break
			}
		}
		if next == "" {
			return nil, fmt.Errorf("no step from %q toward %s in list %d", word, ladder.Target, listID)
		}
		word = next
		path = append(path, word)
	}
	return path, nil
}

// oneLetterChanges returns every lowercase word one letter apart from word, sorted.
func oneLetterChanges(word string) []string {
	out := make([]string, 0, len(word)*25)
	b := []byte(word)
	for i := range b {
		orig := b[i]
		for c := byte('a'); c <= 'z'; c++ {
			if c != orig {
				b[i] = c
				out = append(out, string(b))
			}
		}
		b[i] = orig
	}
	slices.Sort(out)
	return out
}
