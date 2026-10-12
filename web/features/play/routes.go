// Package play serves the daily puzzle and its finish sheet.
package play

import (
	"bytes"
	"context"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"time"

	"poople/ladder"
	"poople/lib/errorlog"
	"poople/lib/static"
	"poople/web/core"
	"poople/web/services/game"
	"poople/web/services/identity"
	"poople/web/services/live"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httprate"
	"github.com/starfederation/datastar-go/datastar"
)

// newPlayersPerMinute bounds the players one client address may create, since
// each first guess stores a player and a year-long session.
const newPlayersPerMinute = 10

type handlers struct {
	users      *identity.Service
	games      *game.Service
	maxFPS     int
	newPlayers *httprate.RateLimiter
}

// shownSignals are the page signals a request carries: the guess being typed
// and what the browser shows (see playView.signals).
type shownSignals struct {
	Guess    string `json:"guess"`
	PuzzleID int64  `json:"puzzleId"`
	Steps    int    `json:"steps"`
}

// Routes registers the page, its live view stream and the guess command.
// It expects CrossOriginProtection, a body limit and identity.WithPlayer upstream.
func Routes(ctx context.Context, router chi.Router, users *identity.Service, games *game.Service, maxFPS int) {
	h := &handlers{users: users, games: games, maxFPS: maxFPS, newPlayers: httprate.NewRateLimiter(newPlayersPerMinute, time.Minute)}
	router.Handle("/play/static/*", static.Handler("/play/static", StaticDirectory, "play"))
	router.Get("/", h.page)
	router.Get("/play/state", func(w http.ResponseWriter, r *http.Request) { h.state(ctx, w, r) })
	router.Post("/play/guess", h.guess)
}

func (h *handlers) page(w http.ResponseWriter, r *http.Request) {
	viewer := identity.ViewerFrom(r.Context())
	v, _, err := h.read(r.Context(), viewer)
	if err != nil {
		core.WriteErrorPage(w, r, http.StatusInternalServerError, err)
		return
	}
	var body bytes.Buffer
	if err := Page(shellViewer(viewer), v).Render(r.Context(), &body); err != nil {
		core.WriteErrorPage(w, r, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := body.WriteTo(w); err != nil {
		errorlog.Request(r, "write page", err)
	}
}

// state streams View. Each frame takes today's puzzle topic, the player's topic,
// account changes (names and pictures on the leaderboard) and a timer for the
// next 08:00 UTC before reading SQLite, so no commit or rollover between the
// read and the wait is missed.
func (h *handlers) state(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var shown shownSignals
	_ = datastar.ReadSignals(r, &shown) // a stream without signals shows nothing yet
	signedIn := identity.ViewerFrom(r.Context()) != nil
	token := identity.SessionToken(r)
	var rollover *time.Timer
	stopRollover := func() {
		if rollover != nil {
			rollover.Stop()
		}
	}
	defer stopRollover()
	freshSteps := -1
	_ = live.Stream(ctx, w, r, h.maxFPS, func() (templ.Component, <-chan struct{}, error) {
		var viewer *identity.Viewer
		if signedIn {
			var err error
			// Resolve the session every frame so a revoked session ends the stream.
			if viewer, err = h.users.ViewerForSession(r.Context(), token); err != nil {
				return nil, nil, err
			}
		}
		v, changed, err := h.read(r.Context(), viewer)
		if err != nil {
			return nil, nil, err
		}
		// A step that appears while this browser watches flips in; it keeps its
		// class until the next step so later frames do not cut the flip short.
		if v.Game.Puzzle.ID == shown.PuzzleID && v.Game.Steps > shown.Steps {
			freshSteps = v.Game.Steps
		} else if v.Game.Puzzle.ID != shown.PuzzleID || v.Game.Steps < shown.Steps {
			freshSteps = -1
		}
		shown.PuzzleID, shown.Steps = v.Game.Puzzle.ID, v.Game.Steps
		v.Fresh = v.Game.Steps > 0 && v.Game.Steps == freshSteps

		stopRollover()
		opened := make(chan struct{})
		rollover = time.AfterFunc(v.NextIn, func() { close(opened) })
		return View(v), live.Either(r.Context(), append(changed, opened)...), nil
	})
}

// read returns the viewer's view of today's puzzle and yesterday's answer, and
// the change channels, taken before the reads, that invalidate it. It never
// creates a player.
func (h *handlers) read(ctx context.Context, viewer *identity.Viewer) (playView, []<-chan struct{}, error) {
	var v playView
	puzzle, err := h.games.DailyPuzzle(ctx)
	if err != nil {
		return v, nil, err
	}
	var playerID int64
	changed := []<-chan struct{}{h.games.PuzzleChanges(puzzle.ID), h.users.AccountChanges()}
	if viewer != nil {
		playerID = viewer.PlayerID
		v.SignedIn = viewer.Account != nil
		changed = append(changed, h.games.PlayerChanges(playerID))
	}
	if v.Game, err = h.games.GameView(ctx, playerID, puzzle.ID); err != nil {
		return v, nil, err
	}
	if v.Game.Finished {
		if v.Standings, err = h.games.Standings(ctx, puzzle.ID, playerID); err != nil {
			return v, nil, err
		}
	}
	if v.Yesterday, v.HasYesterday, err = h.games.YesterdayAnswer(ctx); err != nil {
		return v, nil, err
	}
	v.NextIn = ladder.DailyOpens(puzzle.Number + 1).Sub(h.games.Now())
	return v, changed, nil
}

// guess is the guess command: it commits the guess or its rejection and
// returns no view. A browser's first guess creates its player; the response
// then patches _playerSession so the page reopens its stream with the new cookie.
func (h *handlers) guess(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	var signals shownSignals
	if err == nil && mediaType == "application/json" {
		err = datastar.ReadSignals(r, &signals)
	} else if err == nil {
		err = errors.New("guesses require JSON signals")
	}
	if err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	newPlayer := identity.ViewerFrom(r.Context()) == nil
	if newPlayer && h.newPlayers.OnLimit(w, r, core.ClientKey(r)) {
		http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
		return
	}
	playerID, err := h.users.EnsurePlayer(w, r)
	if err != nil {
		h.fail(w, r, "create player", err)
		return
	}
	today, err := h.games.DailyPuzzle(r.Context())
	if err != nil {
		h.fail(w, r, "open daily puzzle", err)
		return
	}
	// A guess typed on a puzzle that has closed is dropped; the view moves to
	// today's puzzle when it opens.
	if signals.PuzzleID == 0 || signals.PuzzleID == today.ID {
		_, err = h.games.Guess(r.Context(), playerID, today.ID, signals.Guess)
	}
	if err != nil && !errors.Is(err, game.ErrPuzzleClosed) && !errors.Is(err, game.ErrGameFinished) {
		h.fail(w, r, "guess", err)
		return
	}
	if !newPlayer {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	session := strconv.FormatInt(time.Now().UnixNano(), 36)
	// NewSSE sets no-cache; restore no-store before its first header flush.
	sse := datastar.NewSSE(w, r, func(_ *datastar.ServerSentEventGenerator) { w.Header().Set("Cache-Control", "no-store") })
	if err := sse.PatchSignals([]byte(`{"_playerSession":"` + session + `"}`)); err != nil {
		errorlog.Request(r, "reopen stream after first guess", err)
	}
}

func (h *handlers) fail(w http.ResponseWriter, r *http.Request, operation string, err error) {
	errorlog.Request(r, operation, err)
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

// shellViewer is the masthead's view of the viewer; nil until they have an account.
func shellViewer(viewer *identity.Viewer) *core.Viewer {
	if viewer == nil || viewer.Account == nil {
		return nil
	}
	return &core.Viewer{Username: viewer.Account.Username, AvatarURL: viewer.Account.AvatarPath(), IsAdmin: viewer.Account.IsAdmin}
}
