// Package unlimited serves unlimited mode: random puzzles on request, played
// on the daily play surface, with a finish sheet but no leaderboard.
package unlimited

import (
	"bytes"
	"context"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"time"

	"poople/lib/errorlog"
	"poople/lib/static"
	"poople/web/core"
	"poople/web/services/game"
	"poople/web/services/identity"
	"poople/web/services/live"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/starfederation/datastar-go/datastar"
)

type handlers struct {
	users  *identity.Service
	games  *game.Service
	maxFPS int
}

// shownSignals are the page signals a request carries: the guess being typed
// and what the browser shows (see unlimitedView.signals).
type shownSignals struct {
	Guess    string `json:"guess"`
	PuzzleID int64  `json:"puzzleId"`
	Steps    int    `json:"steps"`
}

// Routes registers the page, its live view stream and the new-puzzle and
// guess commands. It expects CrossOriginProtection, a body limit and
// identity.WithPlayer upstream.
func Routes(ctx context.Context, router chi.Router, users *identity.Service, games *game.Service, maxFPS int) {
	h := &handlers{users: users, games: games, maxFPS: maxFPS}
	router.Handle("/unlimited/static/*", static.Handler("/unlimited/static", StaticDirectory, "unlimited"))
	router.Get("/unlimited", h.page)
	router.Get("/unlimited/state", func(w http.ResponseWriter, r *http.Request) { h.state(ctx, w, r) })
	router.Post("/unlimited/new", h.newPuzzle)
	router.Post("/unlimited/guess", h.guess)
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

// state streams View. Each frame takes the player's topic before reading
// SQLite, so no commit between the read and the wait is missed. A visitor
// without a player has nothing to follow; the page reopens the stream once
// its first New puzzle creates one.
func (h *handlers) state(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var shown shownSignals
	_ = datastar.ReadSignals(r, &shown) // a stream without signals shows nothing yet
	hasPlayer := identity.ViewerFrom(r.Context()) != nil
	token := identity.SessionToken(r)
	freshSteps := -1
	_ = live.Stream(ctx, w, r, h.maxFPS, func() (templ.Component, <-chan struct{}, error) {
		var viewer *identity.Viewer
		if hasPlayer {
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
		return View(v), live.Either(r.Context(), changed...), nil
	})
}

// read returns the viewer's current unlimited game, if any, and the change
// channels, taken before the reads, that invalidate it. It never creates a player.
func (h *handlers) read(ctx context.Context, viewer *identity.Viewer) (unlimitedView, []<-chan struct{}, error) {
	var v unlimitedView
	if viewer == nil {
		return v, nil, nil
	}
	changed := []<-chan struct{}{h.games.PlayerChanges(viewer.PlayerID)}
	puzzle, ok, err := h.games.CurrentUnlimited(ctx, viewer.PlayerID)
	if err != nil || !ok {
		return v, changed, err
	}
	v.Playing = true
	v.Game, err = h.games.GameView(ctx, viewer.PlayerID, puzzle.ID)
	return v, changed, err
}

// newPuzzle is the new-puzzle command: it starts a random unlimited puzzle,
// replacing the viewer's current one, and returns no view. A browser's first
// command creates its player; the response then patches _playerSession so the
// page reopens its stream with the new cookie.
func (h *handlers) newPuzzle(w http.ResponseWriter, r *http.Request) {
	newPlayer := identity.ViewerFrom(r.Context()) == nil
	playerID, err := h.users.EnsurePlayer(w, r)
	if err != nil {
		h.fail(w, r, "create player", err)
		return
	}
	if _, err := h.games.StartUnlimited(r.Context(), playerID); err != nil {
		h.fail(w, r, "start unlimited puzzle", err)
		return
	}
	if !newPlayer {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	session := strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := datastar.NewSSE(w, r).PatchSignals([]byte(`{"_playerSession":"` + session + `"}`)); err != nil {
		errorlog.Request(r, "reopen stream after first puzzle", err)
	}
}

// guess is the guess command against the viewer's current unlimited puzzle: it
// commits the guess or its rejection and returns no view. A guess typed on a
// puzzle the viewer has since replaced, or by a browser with no puzzle, is dropped.
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
	viewer := identity.ViewerFrom(r.Context())
	if viewer == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	current, ok, err := h.games.CurrentUnlimited(r.Context(), viewer.PlayerID)
	if err != nil {
		h.fail(w, r, "open unlimited puzzle", err)
		return
	}
	if ok && signals.PuzzleID == current.ID {
		_, err = h.games.Guess(r.Context(), viewer.PlayerID, current.ID, signals.Guess)
	}
	if err != nil && !errors.Is(err, game.ErrGameFinished) {
		h.fail(w, r, "guess", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
