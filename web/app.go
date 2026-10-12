// Package web composes middleware and routes; it is the only place features meet.
package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"poople/app"
	"poople/lib/errorlog"
	"poople/web/core"
	"poople/web/features/account"
	"poople/web/features/admin"
	"poople/web/features/auth"
	"poople/web/features/leaderboard"
	"poople/web/features/play"
	"poople/web/features/stats"
	"poople/web/features/unlimited"
	"poople/web/services/game"
	"poople/web/services/identity"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// BodyLimit bounds every request body unless a route needs a larger one.
const BodyLimit = 16 << 10

// avatarReadTime lets a slow connection finish a picture upload; the server's
// ReadTimeout would otherwise cut it off.
const avatarReadTime = 2 * time.Minute

func avatarUpload(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Path == "/account/avatar"
}

// bodyLimit raises BodyLimit only for routes that need it.
func bodyLimit(r *http.Request) int64 {
	if avatarUpload(r) {
		return account.AvatarBodyLimit
	}
	return BodyLimit
}

// Option adjusts NewApp.
type Option func(*options)

type options struct {
	now          func() time.Time
	trustedProxy bool
}

// WithClock replaces time.Now as the clock that decides the daily puzzle.
func WithClock(now func() time.Time) Option {
	return func(o *options) { o.now = now }
}

// WithTrustedProxy takes each client's address from the last X-Forwarded-For
// entry instead of the connection. Use it only behind exactly one reverse proxy
// that appends that header; otherwise clients choose their own rate-limit keys.
func WithTrustedProxy() Option {
	return func(o *options) { o.trustedProxy = true }
}

// NewApp borrows the backend's storage. secureCookies requires HTTPS for the
// session cookie. The returned setup token is for the operator only and is empty
// once an account exists.
func NewApp(ctx context.Context, backend *app.Backend, secureCookies bool, maxFPS int, opts ...Option) (http.Handler, string, error) {
	if maxFPS <= 0 || time.Second/time.Duration(maxFPS) == 0 {
		return nil, "", errors.New("invalid UI frame rate")
	}
	o := options{now: time.Now}
	for _, opt := range opts {
		opt(&o)
	}
	games := game.New(backend, o.now)
	// Logging in moves this browser's anonymous games into the account.
	users, err := identity.New(ctx, backend.Database(), identity.Cookies{Secure: secureCookies}, games.MergePlayers)
	if err != nil {
		return nil, "", fmt.Errorf("initialize identity: %w", err)
	}
	setup, token, err := identity.NewSetup(ctx, users)
	if err != nil {
		return nil, "", fmt.Errorf("initialize admin setup: %w", err)
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
	// Rate limiters key on the address this resolves (core.ClientKey).
	if o.trustedProxy {
		router.Use(middleware.ClientIPFromXFF())
	} else {
		router.Use(middleware.ClientIPFromRemoteAddr)
	}
	router.Use(http.NewCrossOriginProtection().Handler)
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if avatarUpload(r) {
				// Extend this request's deadlines; the response follows the upload.
				controller := http.NewResponseController(w)
				deadline := time.Now().Add(avatarReadTime)
				for _, err := range []error{controller.SetReadDeadline(deadline), controller.SetWriteDeadline(deadline.Add(30 * time.Second))} {
					if err != nil && !errors.Is(err, http.ErrNotSupported) {
						errorlog.Request(r, "extend upload deadline", err)
					}
				}
			}
			r.Body = http.MaxBytesReader(w, r.Body, bodyLimit(r))
			next.ServeHTTP(w, r)
		})
	})
	router.Use(identity.WithPlayer(users, identityErrorPage))
	router.NotFound(notFound)
	core.Routes(ctx, router)
	auth.Routes(router, users, setup)
	account.Routes(ctx, router, users, maxFPS)
	admin.Routes(ctx, router, users, backend.Players().Changes, maxFPS, identityErrorPage)
	play.Routes(ctx, router, users, games, maxFPS)
	unlimited.Routes(ctx, router, users, games, maxFPS)
	leaderboard.Routes(ctx, router, users, games, maxFPS)
	stats.Routes(ctx, router, users, games, maxFPS)
	return router, token, nil
}
