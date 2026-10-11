package core_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"poople/lib/static"
	"poople/web/core"

	"github.com/go-chi/chi/v5"
)

func TestEmbeddedAssetsAndShell(t *testing.T) {
	remoteCSS := regexp.MustCompile(`(?i)(?:url\(\s*["']?|@import\s*["'])(?:https?:)?//`)
	router := chi.NewRouter()
	core.Routes(t.Context(), router)
	for _, asset := range []struct{ path, media, contains string }{
		{"styles/app.css", "text/css", "@layer"},
		{"datastar-rocket-1.0.4.js", "javascript", "Datastar v1.0.4"},
		{"datastar-LICENSE.txt", "text/plain", "Copyright © Star Federation"},
	} {
		path := static.Path("core", asset.path)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), asset.media) || !strings.Contains(w.Body.String(), asset.contains) || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("asset %s unavailable or unsafe: status=%d", path, w.Code)
		}
		if strings.HasSuffix(asset.path, ".css") && remoteCSS.Match(w.Body.Bytes()) {
			t.Fatalf("stylesheet %s requests remote assets", path)
		}
		if strings.HasSuffix(asset.path, ".js") {
			if got := fmt.Sprintf("%x", sha256.Sum256(w.Body.Bytes())); got != "c723fb309157a555609df273e4693dcc4e193e68e6d7477f3285b955288e1e7b" {
				t.Fatal("free upstream pinned Rocket client changed")
			}
		}
	}
	for _, path := range []string{"/core/static/", "/core/static/styles", "/core/static/missing.js", "/core/static/../shell.templ", "/core/static/styles/../../shell.templ", "/core/static/build/core/app.css", "/static/app.css", "/reload.js"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusNotFound && w.Code != http.StatusForbidden {
			t.Fatalf("asset directory/traversal %s exposed: status=%d", path, w.Code)
		}
	}
	var body bytes.Buffer
	featurePath := "/fixture/static/styles/fixture.css"
	if err := core.Shell("<script>unsafe</script>", featurePath).Render(context.Background(), &body); err != nil {
		t.Fatal(err)
	}
	html := body.String()
	runtimePath := static.Path("core", "datastar-rocket-1.0.4.js")
	for _, want := range []string{`lang="en"`, `name="viewport"`, `name="color-scheme" content="light dark"`, `type="importmap"`, `"datastar":"` + runtimePath + `"`, `src="` + runtimePath + `"`, "&lt;script&gt;"} {
		if !strings.Contains(html, want) {
			t.Fatalf("shell missing %q", want)
		}
	}
	if strings.Count(html, `src="`+runtimePath+`"`) != 1 || strings.Contains(html, "<script>unsafe") {
		t.Fatal("runtime duplicated or title not escaped")
	}
	shared, feature := strings.Index(html, `href="`+static.Path("core", "styles/app.css")+`"`), strings.Index(html, `href="`+featurePath+`"`)
	if shared < 0 || feature < shared {
		t.Fatal("feature styles must load after the shared stylesheet")
	}
	if strings.Contains(html, "https://") || strings.Contains(html, "http://") {
		t.Fatal("shell requests remote assets")
	}
}
