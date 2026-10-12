// Package account serves a signed-in player's profile picture and password.
package account

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"poople/lib/errorlog"
	"poople/lib/static"
	"poople/web/core"
	"poople/web/services/identity"
	"poople/web/services/live"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httprate"
	"github.com/starfederation/datastar-go/datastar"
)

// AvatarBodyLimit bounds POST /account/avatar: the picture plus multipart framing.
// web/app.go applies it to that route only.
const AvatarBodyLimit = identity.AvatarMaxBytes + 64<<10

const maxPasswordSignalBytes = 4 << 10

type handlers struct {
	users   *identity.Service
	limiter *httprate.RateLimiter
}

type passwordChange struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

// Routes expects CrossOriginProtection and identity.WithPlayer upstream.
func Routes(ctx context.Context, router chi.Router, users *identity.Service, maxFPS int) {
	router.Handle("/account/static/*", static.Handler("/account/static", StaticDirectory, "account"))
	h := &handlers{users: users, limiter: httprate.NewRateLimiter(5, time.Minute)}
	router.Group(func(router chi.Router) {
		router.Use(identity.RequireAccount())
		router.Get("/account", h.page)
		router.Get("/account/state", func(w http.ResponseWriter, r *http.Request) {
			token := identity.SessionToken(r)
			_ = live.Stream(ctx, w, r, maxFPS, func() (templ.Component, <-chan struct{}, error) {
				changed := users.AccountChanges()
				viewer, err := users.ViewerForSession(r.Context(), token)
				if err != nil {
					return nil, nil, err
				}
				// The account was deleted while this page was open.
				if viewer.Account == nil {
					return nil, nil, identity.ErrInvalidSession
				}
				return View(viewer.Account), changed, nil
			})
		})
		router.Post("/account/avatar", h.uploadAvatar)
		router.Post("/account/avatar/remove", h.removeAvatar)
		router.Post("/account/password", h.changePassword)
	})
}

// signedIn is the request's account; RequireAccount guarantees one.
func signedIn(r *http.Request) *identity.Account {
	return identity.ViewerFrom(r.Context()).Account
}

func (h *handlers) page(w http.ResponseWriter, r *http.Request) {
	account := signedIn(r)
	var body bytes.Buffer
	if err := Page(account).Render(r.Context(), &body); err != nil {
		core.WriteErrorPage(w, r, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := body.WriteTo(w); err != nil {
		errorlog.Request(r, "write page", err)
	}
}

// uploadAvatar reads one multipart "avatar" file part. Success returns 204 and
// the live view shows the new picture; refusals patch _avatarError.
func (h *handlers) uploadAvatar(w http.ResponseWriter, r *http.Request) {
	picture, err := readAvatar(r)
	if err == nil {
		_, err = h.users.SetAvatar(r.Context(), signedIn(r).ID, bytes.NewReader(picture))
	}
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, identity.ErrAvatarTooLarge):
		patchSignals(w, r, map[string]string{"_avatarError": "Picture must be 2 MB or smaller."})
	case errors.Is(err, identity.ErrAvatarDimensions):
		patchSignals(w, r, map[string]string{"_avatarError": "Picture is too big. Use one under 16 megapixels."})
	case errors.Is(err, identity.ErrAvatarInvalid):
		patchSignals(w, r, map[string]string{"_avatarError": "Picture must be a PNG, JPEG, GIF or WebP image."})
	case errors.Is(err, errNoAvatar):
		patchSignals(w, r, map[string]string{"_avatarError": "Choose a picture to upload."})
	case errors.Is(err, identity.ErrAccountNotFound):
		navigate(w, r, "/login")
	default:
		errorlog.Request(r, "upload picture", err)
		patchSignals(w, r, map[string]string{"_avatarError": "Unable to save your picture. Try again."})
	}
}

var errNoAvatar = errors.New("request has no avatar file")

func readAvatar(r *http.Request) ([]byte, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		return nil, errNoAvatar
	}
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, errNoAvatar
	}
	var picture []byte
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, uploadError(err)
		}
		if part.FormName() != "avatar" || picture != nil {
			part.Close()
			return nil, errNoAvatar
		}
		picture, err = io.ReadAll(io.LimitReader(part, identity.AvatarMaxBytes+1))
		part.Close()
		if err != nil {
			return nil, uploadError(err)
		}
		if len(picture) > identity.AvatarMaxBytes {
			return nil, identity.ErrAvatarTooLarge
		}
	}
	if len(picture) == 0 {
		return nil, errNoAvatar
	}
	return picture, nil
}

// uploadError reports a body cut off by AvatarBodyLimit as a picture too large.
func uploadError(err error) error {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return identity.ErrAvatarTooLarge
	}
	return errNoAvatar
}

func (h *handlers) removeAvatar(w http.ResponseWriter, r *http.Request) {
	err := h.users.RemoveAvatar(r.Context(), signedIn(r).ID)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, identity.ErrAccountNotFound):
		navigate(w, r, "/login")
	default:
		errorlog.Request(r, "remove picture", err)
		patchSignals(w, r, map[string]string{"_avatarError": "Unable to remove your picture. Try again."})
	}
}

// changePassword keeps this browser's session and revokes the account's others.
// Its result is transient form feedback, not a view: the fields clear and a
// notice or error appears.
func (h *handlers) changePassword(w http.ResponseWriter, r *http.Request) {
	var signals passwordChange
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err == nil && mediaType == "application/json" {
		r.Body = http.MaxBytesReader(w, r.Body, maxPasswordSignalBytes)
		err = datastar.ReadSignals(r, &signals)
	} else {
		err = errors.New("password change requires JSON")
	}
	if err != nil {
		passwordError(w, r, "Invalid request. Try again.")
		return
	}
	account := signedIn(r)
	if h.limiter.OnLimit(w, r, "password:"+strconv.FormatInt(account.ID, 10)) {
		passwordError(w, r, "Too many attempts. Wait one minute and try again.")
		return
	}
	err = h.users.ChangePassword(r.Context(), identity.SessionToken(r), signals.CurrentPassword, signals.NewPassword)
	switch {
	case err == nil:
		patchSignals(w, r, map[string]string{"_currentPassword": "", "_newPassword": "", "_passwordError": "",
			"_passwordNotice": "Password changed. Your other devices are logged out."})
	case errors.Is(err, identity.ErrInvalidCredentials):
		passwordError(w, r, "Current password is incorrect.")
	case errors.Is(err, identity.ErrPasswordInvalid):
		passwordError(w, r, "New password must be 10–128 characters.")
	case errors.Is(err, identity.ErrPasswordBusy):
		passwordError(w, r, "Poople is busy. Try again in a moment.")
	case errors.Is(err, identity.ErrInvalidSession):
		navigate(w, r, "/login")
	default:
		errorlog.Request(r, "change password", err)
		passwordError(w, r, "Unable to change your password. Try again.")
	}
}

func passwordError(w http.ResponseWriter, r *http.Request, message string) {
	patchSignals(w, r, map[string]string{"_currentPassword": "", "_passwordNotice": "", "_passwordError": message})
}

func privateSSE(w http.ResponseWriter, r *http.Request) *datastar.ServerSentEventGenerator {
	// NewSSE sets no-cache; restore no-store before its first header flush.
	return datastar.NewSSE(w, r, func(_ *datastar.ServerSentEventGenerator) { w.Header().Set("Cache-Control", "no-store") })
}

func patchSignals(w http.ResponseWriter, r *http.Request, signals map[string]string) {
	if err := privateSSE(w, r).MarshalAndPatchSignals(signals); err != nil {
		errorlog.Request(r, "send account feedback", err)
	}
}

func navigate(w http.ResponseWriter, r *http.Request, path string) {
	if err := privateSSE(w, r).Redirect(path); err != nil {
		errorlog.Request(r, "navigate after account action", err)
	}
}
