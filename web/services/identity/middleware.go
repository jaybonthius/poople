package identity

import (
	"context"
	"errors"
	"net/http"
	"time"

	"poople/lib/errorlog"

	"github.com/starfederation/datastar-go/datastar"
)

type viewerContextKey struct{}

// ErrorPageRenderer returns false for non-page requests so their plain errors survive.
// HTTP assembly supplies presentation without making identity depend on the shell.
type ErrorPageRenderer func(http.ResponseWriter, *http.Request, int) bool

// SessionCookie holds every player's session token, anonymous or signed in.
const SessionCookie = "poople_session"

// Cookies sets host-only, HttpOnly, SameSite=Lax session cookies. Secure requires HTTPS.
type Cookies struct {
	Secure bool
}

func (c Cookies) Set(w http.ResponseWriter, session Session) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: session.Token, Path: "/",
		Expires: session.ExpiresAt, MaxAge: int(sessionDuration.Seconds()),
		HttpOnly: true, Secure: c.Secure, SameSite: http.SameSiteLaxMode,
	})
}

func (c Cookies) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Path: "/", Expires: time.Unix(1, 0).UTC(), MaxAge: -1,
		HttpOnly: true, Secure: c.Secure, SameSite: http.SameSiteLaxMode,
	})
}

// SessionToken is the request's raw session cookie value, or "".
func SessionToken(r *http.Request) string {
	cookie, err := r.Cookie(SessionCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// WithPlayer resolves the session cookie to a Viewer in the request context
// without writing to storage. A stale cookie is cleared and the request continues
// with no viewer; a storage failure fails closed.
func WithPlayer(service *Service, pageErrors ...ErrorPageRenderer) func(http.Handler) http.Handler {
	var pageError ErrorPageRenderer
	if len(pageErrors) > 0 {
		pageError = pageErrors[0]
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := SessionToken(r)
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}
			viewer, err := service.ViewerForSession(r.Context(), token)
			if errors.Is(err, ErrInvalidSession) {
				service.cookies.Clear(w)
				next.ServeHTTP(w, r)
				return
			}
			if err != nil {
				errorlog.Request(r, "read session", err)
				if pageError == nil || !pageError(w, r, http.StatusInternalServerError) {
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), viewerContextKey{}, viewer)))
		})
	}
}

// ViewerFrom returns the request's viewer, or nil when the browser has no live session.
func ViewerFrom(ctx context.Context) *Viewer {
	viewer, _ := ctx.Value(viewerContextKey{}).(*Viewer)
	return viewer
}

// RequireAccount admits only signed-in accounts. Others go to /login: page
// requests by HTTP redirect, Datastar requests (streams and commands) by a
// Datastar redirect, so a revoked session's open page navigates too.
func RequireAccount() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if viewer := ViewerFrom(r.Context()); viewer == nil || viewer.Account == nil {
				toLogin(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAdmin supplements RequireAccount: a signed-in account that is not an
// admin gets 403, shown as an error page for navigations when pageErrors renders one.
func RequireAdmin(pageErrors ...ErrorPageRenderer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			viewer := ViewerFrom(r.Context())
			if viewer == nil || viewer.Account == nil {
				toLogin(w, r)
				return
			}
			if !viewer.Account.IsAdmin {
				if len(pageErrors) == 0 || !pageErrors[0](w, r, http.StatusForbidden) {
					http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				}
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func toLogin(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Datastar-Request") == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	sse := datastar.NewSSE(w, r, func(_ *datastar.ServerSentEventGenerator) { w.Header().Set("Cache-Control", "no-store") })
	if err := sse.Redirect("/login"); err != nil {
		errorlog.Request(r, "redirect to log in", err)
	}
}
