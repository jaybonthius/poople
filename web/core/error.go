package core

import (
	"bytes"
	"context"
	"net/http"

	"poople/lib/errorlog"
)

type viewerContextKey struct{}

// WithViewer records the signed-in player for an error page's masthead.
func WithViewer(ctx context.Context, viewer *Viewer) context.Context {
	return context.WithValue(ctx, viewerContextKey{}, viewer)
}

// WriteErrorPage is for page handlers only, never commands, streams or file
// responses. Its masthead shows the viewer recorded by WithViewer, if any.
func WriteErrorPage(w http.ResponseWriter, r *http.Request, status int, causes ...error) {
	if status >= 500 {
		for _, err := range causes {
			errorlog.Request(r, "serve page", err)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	var body bytes.Buffer
	viewer, _ := r.Context().Value(viewerContextKey{}).(*Viewer)
	if err := ErrorPage(status, viewer).Render(r.Context(), &body); err != nil {
		errorlog.Request(r, "render error page", err)
		http.Error(w, http.StatusText(status), status)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := body.WriteTo(w); err != nil {
		errorlog.Request(r, "write error page", err)
	}
}
