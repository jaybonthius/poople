package web

import (
	"mime"
	"net/http"
	"strconv"
	"strings"

	"poople/web/core"
	"poople/web/services/identity"
)

// identityErrorPage renders the shell error page for browser navigations only.
func identityErrorPage(w http.ResponseWriter, r *http.Request, status int) bool {
	if !navigationNotFound(r) {
		return false
	}
	core.WriteErrorPage(w, r, status)
	return true
}

// notFound answers unknown paths: the shell error page, with the viewer's
// masthead, for browser navigations; plain text for everything else.
func notFound(w http.ResponseWriter, r *http.Request) {
	if !navigationNotFound(r) {
		http.NotFound(w, r)
		return
	}
	var viewer *core.Viewer
	if v := identity.ViewerFrom(r.Context()); v != nil && v.Account != nil {
		viewer = &core.Viewer{Username: v.Account.Username, AvatarURL: v.Account.AvatarPath(), IsAdmin: v.Account.IsAdmin}
	}
	core.WriteErrorPage(w, r.WithContext(core.WithViewer(r.Context(), viewer)), http.StatusNotFound)
}

// navigationNotFound reports whether a missing path was a browser page
// navigation, which gets the HTML error page; streams, assets and commands get plain text.
func navigationNotFound(r *http.Request) bool {
	if r.Method != http.MethodGet || r.Header.Get("Datastar-Request") != "" {
		return false
	}
	p := r.URL.Path
	if strings.HasPrefix(p, "/core/") || strings.Contains(p, "/static/") || strings.HasSuffix(p, "/static") || strings.HasSuffix(p, "/state") {
		return false
	}
	if dest := r.Header.Get("Sec-Fetch-Dest"); dest != "" {
		return dest == "document"
	}
	for _, item := range strings.Split(r.Header.Get("Accept"), ",") {
		mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(item))
		if err != nil || mediaType != "text/html" {
			continue
		}
		if q, ok := params["q"]; ok {
			quality, err := strconv.ParseFloat(q, 64)
			if err != nil || !(quality > 0 && quality <= 1) {
				continue
			}
		}
		return true
	}
	return false
}
