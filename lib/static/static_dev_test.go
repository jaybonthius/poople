//go:build dev

package static

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDiskAssetsUpdateWithoutIndexesOrRootEscape(t *testing.T) {
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "styles"), 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(directory, "styles/app.css")
	handler := diskHandler("/fixture/static", directory)
	for _, content := range []string{"first", "second"} {
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/fixture/static/styles/app.css", nil))
		if w.Code != 200 || w.Body.String() != content || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("disk-backed asset did not update safely")
		}
	}
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("not public"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(directory, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/fixture/static/", "/fixture/static/styles", "/fixture/static/missing", "/fixture/static/../secret", "/fixture/static/escape", "/fixture/static/back\\slash"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatalf("directory/root escape exposed: %s status=%d", path, w.Code)
		}
	}
	if Path("fixture", "styles/app.css") != "/fixture/static/styles/app.css" {
		t.Fatal("dev module path changed")
	}
}
