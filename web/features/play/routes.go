// Package play serves the daily puzzle.
package play

import (
	"bytes"
	"context"
	"net/http"

	"poople/lib/errorlog"
	"poople/store"
	"poople/store/zz"
	"poople/web/core"
	"poople/web/services/live"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"zombiezen.com/go/sqlite"
)

// Routes registers the page and its live view stream.
func Routes(ctx context.Context, router chi.Router, db *store.Store, maxFPS int) {
	router.Get("/", func(w http.ResponseWriter, r *http.Request) {
		app, err := readApp(r.Context(), db)
		if err != nil {
			core.WriteErrorPage(w, r, http.StatusInternalServerError, err)
			return
		}
		var body bytes.Buffer
		if err := Page(app).Render(r.Context(), &body); err != nil {
			core.WriteErrorPage(w, r, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := body.WriteTo(w); err != nil {
			errorlog.Request(r, "write page", err)
		}
	})
	router.Get("/play/state", func(w http.ResponseWriter, r *http.Request) {
		_ = live.Stream(ctx, w, r, maxFPS, func() (templ.Component, <-chan struct{}, error) {
			app, err := readApp(r.Context(), db)
			// No topic yet: a nil channel never fires, so the view renders once per connection.
			return View(app), nil, err
		})
	})
}

func readApp(ctx context.Context, db *store.Store) (app string, err error) {
	err = db.Read(ctx, func(conn *sqlite.Conn) error {
		app, err = zz.OnceGetAppMeta(conn, "app")
		return err
	})
	return app, err
}
