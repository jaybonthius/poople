//go:build !dev

package static

import (
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestReferenceHashedPathsAndServing(t *testing.T) {
	assets := fstest.MapFS{"static/styles/app.css": {Data: []byte("body { color: red; }")}}
	handler := Handler("/fixture/static", assets, "fixture")
	path := Path("fixture", "styles/app.css")
	if !strings.HasPrefix(path, "/fixture/static/styles/app-") || !strings.HasSuffix(path, ".css") {
		t.Fatalf("production path is not module-local and hashed: %s", path)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != 200 || w.Body.String() != "body { color: red; }" || w.Header().Get("Cache-Control") != "public, max-age=31536000" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("hashed asset unavailable or unsafe: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/fixture/static/", "/fixture/static/styles", "/fixture/static/missing", "/fixture/static/../../secret"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 403 && w.Code != 404 {
			t.Fatalf("directory/missing/traversal path exposed: %s status=%d", path, w.Code)
		}
	}
	Handler("/fixture/static", fstest.MapFS{"static/styles/app.css": {Data: []byte("body { color: blue; }")}}, "fixture")
	if Path("fixture", "styles/app.css") == path {
		t.Fatal("changed content retained its production URL")
	}
	if Path("unregistered", "app.css") != "/unregistered/static/app.css" {
		t.Fatal("unregistered module path changed")
	}
}
