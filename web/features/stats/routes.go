// Package stats serves a player's daily statistics.
package stats

import (
	"bytes"
	"context"
	"net/http"
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
)

type handlers struct {
	users  *identity.Service
	games  *game.Service
	maxFPS int
}

// Routes registers the page and its live view stream; there are no commands.
// It expects CrossOriginProtection and identity.WithPlayer upstream.
func Routes(ctx context.Context, router chi.Router, users *identity.Service, games *game.Service, maxFPS int) {
	h := &handlers{users: users, games: games, maxFPS: maxFPS}
	router.Handle("/stats/static/*", static.Handler("/stats/static", StaticDirectory, "stats"))
	router.Get("/stats", h.page)
	router.Get("/stats/state", func(w http.ResponseWriter, r *http.Request) { h.state(ctx, w, r) })
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

// state streams View. Each frame takes today's puzzle topic, the player's topic
// and a timer for the next 08:00 UTC before reading SQLite, so no commit or
// rollover between the read and the wait is missed.
func (h *handlers) state(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	hasSession := identity.ViewerFrom(r.Context()) != nil
	token := identity.SessionToken(r)
	var rollover *time.Timer
	stopRollover := func() {
		if rollover != nil {
			rollover.Stop()
		}
	}
	defer stopRollover()
	_ = live.Stream(ctx, w, r, h.maxFPS, func() (templ.Component, <-chan struct{}, error) {
		var viewer *identity.Viewer
		if hasSession {
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
		stopRollover()
		opened := make(chan struct{})
		rollover = time.AfterFunc(time.Until(ladder.DailyOpens(v.Puzzle.Number+1)), func() { close(opened) })
		return View(v), live.Either(r.Context(), append(changed, opened)...), nil
	})
}

// read returns the viewer's statistics and the change channels, taken before
// the reads, that invalidate them. It never creates a player.
func (h *handlers) read(ctx context.Context, viewer *identity.Viewer) (statsView, []<-chan struct{}, error) {
	var v statsView
	var err error
	if v.Puzzle, err = h.games.DailyPuzzle(ctx); err != nil {
		return v, nil, err
	}
	changed := []<-chan struct{}{h.games.PuzzleChanges(v.Puzzle.ID)}
	var playerID int64
	if viewer != nil {
		playerID = viewer.PlayerID
		changed = append(changed, h.games.PlayerChanges(playerID))
	}
	if v.Stats, err = h.games.Stats(ctx, playerID, v.Puzzle); err != nil {
		return v, nil, err
	}
	return v, changed, nil
}

// shellViewer is the masthead's view of the viewer; nil until they have an account.
func shellViewer(viewer *identity.Viewer) *core.Viewer {
	if viewer == nil || viewer.Account == nil {
		return nil
	}
	return &core.Viewer{Username: viewer.Account.Username, AvatarURL: viewer.Account.AvatarPath(), IsAdmin: viewer.Account.IsAdmin}
}
