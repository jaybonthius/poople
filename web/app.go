// Package web composes middleware and routes; it is the only place features meet.
package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"poople/app"
	"poople/web/core"
	"poople/web/features/account"
	"poople/web/features/admin"
	"poople/web/features/auth"
	"poople/web/features/play"
	"poople/web/services/game"
	"poople/web/services/identity"

	"github.com/go-chi/chi/v5"
)

// BodyLimit bounds every request body unless a route needs a larger one.
const BodyLimit = 16 << 10

// bodyLimit raises BodyLimit only for routes that need it.
func bodyLimit(r *http.Request) int64 {
	if r.Method == http.MethodPost && r.URL.Path == "/account/avatar" {
		return account.AvatarBodyLimit
	}
	return BodyLimit
}

// NewApp borrows the backend's storage. secureCookies requires HTTPS for the
// session cookie. The returned setup token is for the operator only and is empty
// once an account exists.
func NewApp(ctx context.Context, backend *app.Backend, secureCookies bool, maxFPS int) (http.Handler, string, error) {
	if maxFPS <= 0 || time.Second/time.Duration(maxFPS) == 0 {
		return nil, "", errors.New("invalid UI frame rate")
	}
	// Logging in moves this browser's anonymous games into the account.
	users, err := identity.New(ctx, backend.Database(), identity.Cookies{Secure: secureCookies}, game.MergePlayers)
	if err != nil {
		return nil, "", fmt.Errorf("initialize identity: %w", err)
	}
	setup, token, err := identity.NewSetup(ctx, users)
	if err != nil {
		return nil, "", fmt.Errorf("initialize admin setup: %w", err)
	}
	games := game.New(backend, time.Now)
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
			r.Body = http.MaxBytesReader(w, r.Body, bodyLimit(r))
			next.ServeHTTP(w, r)
		})
	})
	router.Use(identity.WithPlayer(users, identityErrorPage))
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if navigationNotFound(r) {
			core.WriteErrorPage(w, r, http.StatusNotFound)
			return
		}
		http.NotFound(w, r)
	})
	core.Routes(ctx, router)
	auth.Routes(router, users, setup)
	account.Routes(ctx, router, users, maxFPS)
	admin.Routes(ctx, router, users, backend.Players().Changes, maxFPS, identityErrorPage)
	play.Routes(ctx, router, users, games, maxFPS)
	return router, token, nil
}
