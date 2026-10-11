// Package admin serves the admin's user list: reset links, picture removal and account deletion.
package admin

import (
	"bytes"
	"context"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strconv"

	"poople/lib/errorlog"
	"poople/lib/static"
	"poople/web/core"
	"poople/web/services/identity"
	"poople/web/services/live"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/starfederation/datastar-go/datastar"
)

// GameChanges closes after the next committed change to a player's games, so
// the list's games-played counts stay current.
type GameChanges func(playerID int64) <-chan struct{}

type handlers struct {
	users *identity.Service
}

// IssuedReset is a reset link the admin just issued, shown only while its URL
// carries the raw token and the link is still unused.
type IssuedReset struct {
	AccountID int64
	Username  string
	Path      string // /reset?token=…; the page turns it into an absolute URL
}

// Routes expects CrossOriginProtection and identity.WithPlayer upstream.
// forbidden renders the 403 page for signed-in non-admins.
func Routes(ctx context.Context, router chi.Router, users *identity.Service, gameChanges GameChanges, maxFPS int, forbidden identity.ErrorPageRenderer) {
	router.Handle("/admin/static/*", static.Handler("/admin/static", StaticDirectory, "admin"))
	h := &handlers{users: users}
	router.Group(func(router chi.Router) {
		router.Use(identity.RequireAdmin(forbidden))
		router.Get("/admin/users", h.page)
		router.Get("/admin/users/state", func(w http.ResponseWriter, r *http.Request) {
			token := identity.SessionToken(r)
			// Players listed by the previous frame; their game topics are taken before the next read.
			var followed []int64
			_ = live.Stream(ctx, w, r, maxFPS, func() (templ.Component, <-chan struct{}, error) {
				changes := []<-chan struct{}{users.AccountChanges()}
				subscribed := make(map[int64]bool, len(followed))
				for _, playerID := range followed {
					changes = append(changes, gameChanges(playerID))
					subscribed[playerID] = true
				}
				viewer, err := users.ViewerForSession(r.Context(), token)
				if err != nil {
					return nil, nil, err
				}
				if viewer.Account == nil || !viewer.Account.IsAdmin {
					return nil, nil, identity.ErrAdminRequired
				}
				view, accounts, err := h.view(r, viewer.Account.ID)
				if err != nil {
					return nil, nil, err
				}
				followed = followed[:0]
				for _, account := range accounts {
					followed = append(followed, account.PlayerID)
					// A newly listed player is followed from now; the account change that listed it woke this frame.
					if !subscribed[account.PlayerID] {
						changes = append(changes, gameChanges(account.PlayerID))
					}
				}
				return view, live.Either(r.Context(), changes...), nil
			})
		})
		router.Post("/admin/users/{accountID}/reset", h.issueReset)
		router.Post("/admin/users/{accountID}/avatar/remove", h.removeAvatar)
		router.Post("/admin/users/{accountID}/delete", h.deleteAccount)
	})
}

// adminID is the signed-in admin; RequireAdmin guarantees one.
func adminID(r *http.Request) int64 {
	return identity.ViewerFrom(r.Context()).Account.ID
}

func targetID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "accountID"), 10, 64)
	return id, err == nil && id > 0
}

// view reads the committed account list. A just-issued reset link comes only
// from the request URL and is shown while it is still usable.
func (h *handlers) view(r *http.Request, adminID int64) (templ.Component, []identity.ListedAccount, error) {
	accounts, err := h.users.ListAccounts(r.Context(), adminID)
	if err != nil {
		return nil, nil, err
	}
	var issued *IssuedReset
	notice := ""
	if token := r.URL.Query().Get("reset"); token != "" {
		accountID, _ := strconv.ParseInt(r.URL.Query().Get("account"), 10, 64)
		username, err := h.users.IssuedPasswordReset(r.Context(), adminID, accountID, token)
		switch {
		case err == nil:
			issued = &IssuedReset{AccountID: accountID, Username: username, Path: "/reset?" + url.Values{"token": {token}}.Encode()}
		case errors.Is(err, identity.ErrInvalidReset):
			notice = "That reset link has been used or has expired. Issue another if needed."
		default:
			return nil, nil, err
		}
	}
	return View(accounts, adminID, issued, notice), accounts, nil
}

func (h *handlers) page(w http.ResponseWriter, r *http.Request) {
	admin := identity.ViewerFrom(r.Context()).Account
	view, _, err := h.view(r, admin.ID)
	if err != nil {
		core.WriteErrorPage(w, r, http.StatusInternalServerError, err)
		return
	}
	statePath := "/admin/users/state"
	query := url.Values{}
	for _, key := range []string{"account", "reset"} {
		if value := r.URL.Query().Get(key); value != "" {
			query.Set(key, value)
		}
	}
	if len(query) > 0 {
		statePath += "?" + query.Encode()
	}
	var body bytes.Buffer
	if err := Page(admin, view, statePath).Render(r.Context(), &body); err != nil {
		core.WriteErrorPage(w, r, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := body.WriteTo(w); err != nil {
		errorlog.Request(r, "write page", err)
	}
}

// issueReset is a plain form POST answered by 303: the raw token reaches the
// admin only in the redirect URL, never in storage, logs or a stream signal.
func (h *handlers) issueReset(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" || r.ParseForm() != nil {
		http.Error(w, "Invalid reset request.", http.StatusBadRequest)
		return
	}
	accountID, ok := targetID(r)
	if !ok {
		http.Error(w, "Invalid reset request.", http.StatusBadRequest)
		return
	}
	token, err := h.users.IssuePasswordReset(r.Context(), adminID(r), accountID)
	if err != nil {
		if !errors.Is(err, identity.ErrAccountNotFound) && !errors.Is(err, identity.ErrAdminRequired) {
			errorlog.Request(r, "issue password reset", err)
		}
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}
	query := url.Values{"account": {strconv.FormatInt(accountID, 10)}, "reset": {token}}
	http.Redirect(w, r, "/admin/users?"+query.Encode(), http.StatusSeeOther)
}

func (h *handlers) removeAvatar(w http.ResponseWriter, r *http.Request) {
	accountID, ok := targetID(r)
	if !ok {
		http.Error(w, "Invalid request.", http.StatusBadRequest)
		return
	}
	command(w, r, "remove picture", h.users.AdminRemoveAvatar(r.Context(), adminID(r), accountID))
}

func (h *handlers) deleteAccount(w http.ResponseWriter, r *http.Request) {
	accountID, ok := targetID(r)
	if !ok {
		http.Error(w, "Invalid request.", http.StatusBadRequest)
		return
	}
	command(w, r, "delete account", h.users.DeleteAccount(r.Context(), adminID(r), accountID))
}

// command answers 204 on success; the live list shows the change. Refusals patch _adminError.
func command(w http.ResponseWriter, r *http.Request, operation string, err error) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, identity.ErrDeleteOwnAccount):
		patchError(w, r, "You can't delete your own account.")
	case errors.Is(err, identity.ErrAccountNotFound):
		patchError(w, r, "That account no longer exists.")
	case errors.Is(err, identity.ErrAdminRequired):
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
	default:
		errorlog.Request(r, operation, err)
		patchError(w, r, "Something went wrong. Try again.")
	}
}

func patchError(w http.ResponseWriter, r *http.Request, message string) {
	sse := datastar.NewSSE(w, r, func(_ *datastar.ServerSentEventGenerator) { w.Header().Set("Cache-Control", "no-store") })
	if err := sse.MarshalAndPatchSignals(map[string]string{"_adminError": message}); err != nil {
		errorlog.Request(r, "send admin error", err)
	}
}
