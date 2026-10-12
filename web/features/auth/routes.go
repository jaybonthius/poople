// Package auth serves setup, sign up, log in, log out, reset-link use and avatar files.
package auth

import (
	"bytes"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"poople/lib/errorlog"
	"poople/web/core"
	"poople/web/services/identity"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httprate"
	"github.com/starfederation/datastar-go/datastar"
)

const maxAuthSignalBytes = 16 << 10

type handlers struct {
	users   *identity.Service
	setup   *identity.Setup
	limiter *httprate.RateLimiter
}

type credentials struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	SetupToken string `json:"setupToken"`
	ResetToken string `json:"resetToken"`
}

// Routes expects CrossOriginProtection and identity.WithPlayer upstream.
func Routes(router chi.Router, users *identity.Service, setup *identity.Setup) {
	h := &handlers{users: users, setup: setup, limiter: httprate.NewRateLimiter(5, time.Minute)}
	router.Get("/login", h.loginPage)
	router.Post("/login", h.login)
	router.Get("/signup", h.signUpPage)
	router.Post("/signup", h.signUp)
	router.Get("/setup", h.setupPage)
	router.Post("/setup", h.completeSetup)
	router.Get("/reset", h.resetPage)
	router.Post("/reset", h.reset)
	router.Post("/logout", h.logout)
	router.Get("/avatars/{accountID}/{hash}.png", h.avatar)
}

func privateHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func render(w http.ResponseWriter, r *http.Request, component templ.Component) {
	privateHeaders(w)
	var body bytes.Buffer
	if err := component.Render(r.Context(), &body); err != nil {
		core.WriteErrorPage(w, r, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := body.WriteTo(w); err != nil {
		errorlog.Request(r, "write page", err)
	}
}

func signedIn(r *http.Request) bool {
	viewer := identity.ViewerFrom(r.Context())
	return viewer != nil && viewer.Account != nil
}

func (h *handlers) loginPage(w http.ResponseWriter, r *http.Request) {
	if signedIn(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	render(w, r, LoginPage())
}

func (h *handlers) signUpPage(w http.ResponseWriter, r *http.Request) {
	if signedIn(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	render(w, r, SignUpPage())
}

func (h *handlers) setupPage(w http.ResponseWriter, r *http.Request) {
	privateHeaders(w)
	token := r.URL.Query().Get("token")
	valid, err := h.setup.Valid(r.Context(), token)
	if err != nil {
		core.WriteErrorPage(w, r, http.StatusInternalServerError, err)
		return
	}
	if !valid {
		core.WriteErrorPage(w, r, http.StatusNotFound)
		return
	}
	render(w, r, SetupPage(token))
}

func (h *handlers) resetPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	username, err := h.users.PasswordResetFor(r.Context(), token)
	if errors.Is(err, identity.ErrInvalidReset) {
		render(w, r, ResetPage("", ""))
		return
	}
	if err != nil {
		core.WriteErrorPage(w, r, http.StatusInternalServerError, err)
		return
	}
	render(w, r, ResetPage(token, username))
}

func (h *handlers) login(w http.ResponseWriter, r *http.Request) {
	if signedIn(r) {
		navigate(w, r, "/")
		return
	}
	var signals credentials
	if err := readSignals(w, r, &signals); err != nil {
		patchError(w, r, "Invalid request. Try again.")
		return
	}
	username := strings.TrimSpace(signals.Username)
	// Bound the input before Argon2 sees it; identity owns credential rules.
	if len(username) == 0 || len(username) > 64 || !utf8.ValidString(username) || len(signals.Password) > 512 || !utf8.ValidString(signals.Password) {
		patchError(w, r, "Invalid username or password.")
		return
	}
	// Limit by client, not username, so nobody can lock another player out.
	if h.peerLimited(w, r, "login") {
		return
	}
	_, session, err := h.users.Login(r.Context(), identity.SessionToken(r), username, signals.Password)
	if errors.Is(err, identity.ErrInvalidCredentials) {
		patchError(w, r, "Invalid username or password.")
		return
	}
	if errors.Is(err, identity.ErrPasswordBusy) {
		patchError(w, r, busyMessage)
		return
	}
	if err != nil {
		errorlog.Request(r, "log in", err)
		patchError(w, r, "Unable to log in. Try again.")
		return
	}
	h.users.Cookies().Set(w, session)
	navigate(w, r, "/")
}

func (h *handlers) signUp(w http.ResponseWriter, r *http.Request) {
	if signedIn(r) {
		navigate(w, r, "/")
		return
	}
	var signals credentials
	if err := readSignals(w, r, &signals); err != nil {
		patchError(w, r, "Invalid request. Try again.")
		return
	}
	if h.peerLimited(w, r, "signup") {
		return
	}
	_, session, err := h.users.SignUp(r.Context(), identity.SessionToken(r), signals.Username, signals.Password)
	if err != nil {
		h.accountError(w, r, "sign up", err)
		return
	}
	h.users.Cookies().Set(w, session)
	navigate(w, r, "/")
}

func (h *handlers) completeSetup(w http.ResponseWriter, r *http.Request) {
	var signals credentials
	if err := readSignals(w, r, &signals); err != nil {
		patchError(w, r, "Invalid request. Try again.")
		return
	}
	if h.peerLimited(w, r, "setup") {
		return
	}
	_, session, err := h.setup.CreateAdmin(r.Context(), signals.SetupToken, identity.SessionToken(r), signals.Username, signals.Password)
	if errors.Is(err, identity.ErrInvalidSetupToken) || errors.Is(err, identity.ErrSetupComplete) {
		patchError(w, r, "Setup link is unavailable. Restart the server for a new link.")
		return
	}
	if err != nil {
		h.accountError(w, r, "create admin account", err)
		return
	}
	h.users.Cookies().Set(w, session)
	navigate(w, r, "/")
}

func (h *handlers) reset(w http.ResponseWriter, r *http.Request) {
	var signals credentials
	if err := readSignals(w, r, &signals); err != nil {
		patchError(w, r, "Invalid request. Try again.")
		return
	}
	if h.peerLimited(w, r, "reset") {
		return
	}
	_, session, err := h.users.ResetPassword(r.Context(), signals.ResetToken, identity.SessionToken(r), signals.Password)
	if errors.Is(err, identity.ErrInvalidReset) {
		patchError(w, r, "This reset link has expired or was already used. Ask the admin for a new one.")
		return
	}
	if err != nil {
		h.accountError(w, r, "reset password", err)
		return
	}
	h.users.Cookies().Set(w, session)
	navigate(w, r, "/")
}

// accountError shows rule violations as written and hides everything else.
func (h *handlers) accountError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	switch {
	case errors.Is(err, identity.ErrUsernameTaken):
		patchError(w, r, "That username is taken. Try another.")
	case errors.Is(err, identity.ErrSetupRequired):
		patchError(w, r, "Sign-up opens once the admin has set up Poople.")
	case errors.Is(err, identity.ErrPasswordBusy):
		patchError(w, r, busyMessage)
	case errors.Is(err, identity.ErrAlreadySignedUp):
		patchError(w, r, "This device already has an account. Log in instead.")
	case errors.Is(err, identity.ErrUsernameInvalid), errors.Is(err, identity.ErrPasswordInvalid):
		message := err.Error()
		patchError(w, r, strings.ToUpper(message[:1])+message[1:]+".")
	default:
		errorlog.Request(r, operation, err)
		patchError(w, r, "Something went wrong. Try again.")
	}
}

// busyMessage answers a password check refused because every hashing slot is taken.
const busyMessage = "Poople is busy. Try again in a moment."

// peerLimited bounds log-in, account creation and reset attempts per client
// address, even when attackers vary usernames or tokens.
func (h *handlers) peerLimited(w http.ResponseWriter, r *http.Request, action string) bool {
	if h.limiter.OnLimit(w, r, action+":"+core.ClientKey(r)) {
		patchError(w, r, "Too many attempts. Wait one minute and try again.")
		return true
	}
	return false
}

func (h *handlers) logout(w http.ResponseWriter, r *http.Request) {
	token := identity.SessionToken(r)
	if token == "" {
		navigate(w, r, "/")
		return
	}
	var signals credentials
	if err := readSignals(w, r, &signals); err != nil {
		patchLogoutError(w, r, "Invalid request. Try again.")
		return
	}
	if err := h.users.Logout(r.Context(), token); err != nil {
		errorlog.Request(r, "log out", err)
		patchLogoutError(w, r, "Unable to log out. Try again.")
		return
	}
	h.users.Cookies().Clear(w)
	navigate(w, r, "/")
}

// avatar serves a stored picture. The hash in the path changes with the
// picture, so the response is immutable and publicly cacheable.
func (h *handlers) avatar(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "accountID"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	picture, err := h.users.Avatar(r.Context(), id, chi.URLParam(r, "hash"))
	if errors.Is(err, identity.ErrAvatarNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		errorlog.Request(r, "serve picture", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(picture)))
	if _, err := w.Write(picture); err != nil {
		errorlog.Request(r, "write picture", err)
	}
}

func readSignals(w http.ResponseWriter, r *http.Request, signals *credentials) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errors.New("account requests require JSON")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthSignalBytes)
	return datastar.ReadSignals(r, signals)
}

func privateSSE(w http.ResponseWriter, r *http.Request) *datastar.ServerSentEventGenerator {
	// NewSSE sets no-cache; restore no-store before its first header flush.
	return datastar.NewSSE(w, r, func(_ *datastar.ServerSentEventGenerator) { privateHeaders(w) })
}

// patchError clears the password and shows message; account commands render no HTML.
func patchError(w http.ResponseWriter, r *http.Request, message string) {
	if err := privateSSE(w, r).MarshalAndPatchSignals(map[string]string{"password": "", "_authError": message}); err != nil {
		errorlog.Request(r, "send account error", err)
	}
}

func patchLogoutError(w http.ResponseWriter, r *http.Request, message string) {
	if err := privateSSE(w, r).MarshalAndPatchSignals(map[string]string{"_logoutError": message}); err != nil {
		errorlog.Request(r, "send log-out error", err)
	}
}

func navigate(w http.ResponseWriter, r *http.Request, path string) {
	if err := privateSSE(w, r).Redirect(path); err != nil {
		errorlog.Request(r, "navigate after account action", err)
	}
}
