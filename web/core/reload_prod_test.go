//go:build !dev

package core_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"poople/web/core"

	"github.com/go-chi/chi/v5"
)

func TestProductionExcludesReload(t *testing.T) {
	router := chi.NewRouter()
	core.Routes(t.Context(), router)
	for _, request := range []struct{ method, path string }{
		{"GET", "/reload"}, {"GET", "/reload.js"}, {"POST", "/force-reload"},
		{"GET", "/core/static/reload_dev.js"}, {"GET", "/core/static/styles/app.css.map"},
		{"GET", "/home/static/web-components/copy-button.js.map"},
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(request.method, request.path, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("production exposed %s %s: %d", request.method, request.path, w.Code)
		}
	}
	var body bytes.Buffer
	if err := core.Shell("fixture").Render(t.Context(), &body); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body.String(), "/reload") {
		t.Fatal("production HTML included reload tooling")
	}
}
