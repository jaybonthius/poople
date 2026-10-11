// Package web composes middleware and routes; it is the only place features meet.
package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"poople/app"
	"poople/web/core"
	"poople/web/features/play"

	"github.com/go-chi/chi/v5"
)

// BodyLimit bounds every request body unless a route needs a larger one.
const BodyLimit = 16 << 10

// NewApp borrows the backend's storage. secureCookies requires HTTPS for the
// session cookies set by the identity service.
func NewApp(ctx context.Context, backend *app.Backend, secureCookies bool, maxFPS int) (http.Handler, error) {
	if maxFPS <= 0 || time.Second/time.Duration(maxFPS) == 0 {
		return nil, errors.New("invalid UI frame rate")
	}
	router := chi.NewRouter()
	// No request logger: setup and reset URLs and account bodies contain secrets.
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			next.ServeHTTP(w, r)
		})
	})
	router.Use(http.NewCrossOriginProtection().Handler)
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, BodyLimit)
			next.ServeHTTP(w, r)
		})
	})
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if navigationNotFound(r) {
			core.WriteErrorPage(w, r, http.StatusNotFound)
			return
		}
		http.NotFound(w, r)
	})
	core.Routes(ctx, router)
	play.Routes(ctx, router, backend.Database(), maxFPS)
	return router, nil
}
