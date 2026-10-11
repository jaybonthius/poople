package web

import (
	"mime"
	"net/http"
	"strconv"
	"strings"
)

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
