package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"poople/app"
	"poople/web"
	"poople/web/services/identity"
)

// A signed-in player who follows a dead link keeps their own masthead.
func TestNotFoundPageShowsSignedInMasthead(t *testing.T) {
	backend, err := app.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	h, token, err := web.NewApp(t.Context(), backend, false, 30)
	if err != nil {
		t.Fatal(err)
	}
	setup := httptest.NewRequest("POST", "/setup", strings.NewReader(`{"setupToken":"`+token+`","username":"jay","password":"correct horse battery"}`))
	setup.Header.Set("Content-Type", "application/json")
	setup.Header.Set("Datastar-Request", "true")
	setup.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, setup)
	var session *http.Cookie
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == identity.SessionCookie {
			session = cookie
		}
	}
	if session == nil {
		t.Fatalf("setup gave no session: %s", w.Body)
	}
	missing := httptest.NewRequest("GET", "/missing", nil)
	missing.Header.Set("Sec-Fetch-Dest", "document")
	missing.AddCookie(session)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, missing)
	html := w.Body.String()
	if w.Code != http.StatusNotFound || !strings.Contains(html, `aria-label="Your account, jay"`) || strings.Contains(html, `aria-label="Log in"`) || !strings.Contains(html, "Page not found") {
		t.Fatalf("signed-in 404 status=%d: %s", w.Code, html)
	}
}
