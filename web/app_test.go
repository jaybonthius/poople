package web_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"poople/app"
	"poople/web"
)

func newHandler(t *testing.T) http.Handler {
	t.Helper()
	backend, err := app.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	handler, _, err := web.NewApp(t.Context(), backend, false, 30)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func request(h http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func assertPrivateHeaders(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	for name, want := range map[string]string{"Cache-Control": "no-store", "Referrer-Policy": "no-referrer", "X-Content-Type-Options": "nosniff"} {
		if got := w.Header().Get(name); got != want {
			t.Fatalf("%s=%q, want %q", name, got, want)
		}
	}
}

func TestNewAppRejectsInvalidFrameRate(t *testing.T) {
	backend, err := app.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	for _, fps := range []int{0, -1, int(time.Second) + 1} {
		if _, _, err := web.NewApp(t.Context(), backend, false, fps); err == nil {
			t.Fatalf("frame rate %d accepted", fps)
		}
	}
}

func TestPlayPageRendersShellAndOpensStream(t *testing.T) {
	h := newHandler(t)
	w := request(h, "GET", "/", nil)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("page status=%d type=%q", w.Code, w.Header().Get("Content-Type"))
	}
	assertPrivateHeaders(t, w)
	html := w.Body.String()
	for _, want := range []string{"<!doctype html>", `type="importmap"`, `data-init="@get(&#39;/play/state&#39;`, `<main id="main" class="play"`, `<header class="masthead">`, `<ol class="ladder"`, `<section class="keyboard"`, "Daily #"} {
		if !strings.Contains(html, want) {
			t.Fatalf("page missing %q", want)
		}
	}
}

func TestPlayStateStreamsMainFromStore(t *testing.T) {
	server := httptest.NewServer(newHandler(t))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/play/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Accept-Encoding", "identity")
	r.Header.Set("Datastar-Request", "true")
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("stream status=%d headers=%v", response.StatusCode, response.Header)
	}
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		if line := scanner.Text(); strings.HasPrefix(line, `data: elements <main id="main" class="play"`) {
			if !strings.Contains(line, `<ol class="ladder"`) || !strings.Contains(line, "Daily #") {
				t.Fatalf("streamed view=%q", line)
			}
			return
		}
	}
	t.Fatalf("stream ended without a full <main> view: %v", scanner.Err())
}

func TestCrossOriginCommandsAreRejected(t *testing.T) {
	h := newHandler(t)
	w := request(h, "POST", "/", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://untrusted.invalid"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST status=%d", w.Code)
	}
	w = request(h, "POST", "http://internal.test/", map[string]string{"Origin": "https://untrusted.test", "X-Forwarded-Host": "untrusted.test"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("proxy headers bypassed cross-origin protection: %d", w.Code)
	}
	w = request(h, "POST", "/", map[string]string{"Sec-Fetch-Site": "same-origin"})
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("same-origin POST to a page status=%d", w.Code)
	}
}

func TestNotFoundIsAPageOnlyForNavigation(t *testing.T) {
	h := newHandler(t)
	for _, headers := range []map[string]string{{"Sec-Fetch-Dest": "document"}, {"Accept": "text/html,application/xhtml+xml"}} {
		w := request(h, "GET", "/missing?token=private-query", headers)
		if w.Code != http.StatusNotFound || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
			t.Fatalf("navigation 404 status=%d type=%q", w.Code, w.Header().Get("Content-Type"))
		}
		assertPrivateHeaders(t, w)
		html := w.Body.String()
		if !strings.Contains(html, "<!doctype html>") || !strings.Contains(html, `<main id="main" class="page error-page">`) || !strings.Contains(html, "Not Found") || strings.Contains(html, "private-query") {
			t.Fatal("navigation 404 is not the shell error page")
		}
	}
	for _, tc := range []struct {
		path    string
		headers map[string]string
	}{
		{"/missing", map[string]string{"Accept": "text/html", "Datastar-Request": "true"}},
		{"/missing", map[string]string{"Sec-Fetch-Dest": "empty"}},
		{"/missing", map[string]string{"Accept": "text/html;q=0"}},
		{"/missing/state", map[string]string{"Accept": "text/html"}},
		{"/core/static/missing.js", map[string]string{"Accept": "text/html"}},
	} {
		w := request(h, "GET", tc.path, tc.headers)
		if w.Code != http.StatusNotFound || w.Body.String() != "404 page not found\n" {
			t.Fatalf("%s %v: status=%d body=%q", tc.path, tc.headers, w.Code, w.Body.String())
		}
	}
}
