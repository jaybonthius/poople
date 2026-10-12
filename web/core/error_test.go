package core_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"poople/web/core"
)

func TestNotFoundPageNamesTheProblemAndKeepsTheViewersMasthead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		viewer *core.Viewer
		want   string
	}{
		{"anonymous", nil, `href="/login" aria-label="Log in"`},
		{"signed in", &core.Viewer{Username: "mira"}, `href="/account" aria-label="Your account, mira"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/missing", nil)
			r = r.WithContext(core.WithViewer(r.Context(), tc.viewer))
			w := httptest.NewRecorder()
			core.WriteErrorPage(w, r, http.StatusNotFound)
			html := w.Body.String()
			if w.Code != http.StatusNotFound {
				t.Fatalf("status=%d", w.Code)
			}
			for _, want := range []string{tc.want, `<h1 class="page-title">Page not found</h1>`, `class="form-panel"`, `<use href="#mascot">`} {
				if !strings.Contains(html, want) {
					t.Fatalf("404 page missing %q", want)
				}
			}
		})
	}
}
