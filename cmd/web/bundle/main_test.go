package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func writeFixture(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "web/core/app.css", `@import "./styles/colors.part.css"; body { color: var(--fixture); }`)
	writeFixture(t, root, "web/core/styles/colors.part.css", `:root { --fixture: red; }`)
	writeFixture(t, root, "web/core/scripts/core.ts", `console.log("core-script");`)
	writeFixture(t, root, "web/features/home/app.css", `@import "../../core/styles/colors.part.css"; button { color: var(--fixture); }`)
	writeFixture(t, root, "web/features/home/scripts/app.ts", `import { value } from '../value'; import { label } from '../label.js'; console.log(value, label);`)
	writeFixture(t, root, "web/features/home/label.js", `export const label = "javascript-before";`)
	writeFixture(t, root, "web/features/home/value.ts", `export const value: string = "fixture-before";`)
	writeFixture(t, root, "web/features/home/web-components/component.ts", `import { rocket } from 'datastar'; rocket('fixture-copy', { mode: 'light' });`)
	writeFixture(t, root, "web/core/static/vendor.js", `this must never be compiled !!!`)
	return root
}

func readOutput(t *testing.T, root, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, "web", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestReferencePathsImportsErrorsAndProductionMaps(t *testing.T) {
	root := fixture(t)
	if err := run(t.Context(), root, false, true, ""); err != nil {
		t.Fatal(err)
	}
	cssPath, jsPath := "core/static/styles/app.css", "features/home/static/app.js"
	css, js := readOutput(t, root, cssPath), readOutput(t, root, jsPath)
	if !strings.Contains(css, "--fixture: red") || !strings.Contains(js, "fixture-before") || strings.Contains(js, ": string") {
		t.Fatal("CSS imports/TypeScript transpilation missing")
	}
	readOutput(t, root, "features/home/static/styles/app.css")
	readOutput(t, root, "core/static/core.js")
	component := readOutput(t, root, "features/home/static/web-components/component.js")
	if !strings.Contains(component, `from"datastar"`) {
		t.Fatal("Rocket must import the page's runtime, not bundle another copy")
	}
	for _, path := range []string{"core/static/styles/styles/colors.part.css", "core/static/build", "features/home/static/value.js"} {
		if _, err := os.Stat(filepath.Join(root, "web", path)); !os.IsNotExist(err) {
			t.Fatalf("unexpected output %s: %v", path, err)
		}
	}
	readOutput(t, root, cssPath+".map")
	readOutput(t, root, jsPath+".map")
	writeFixture(t, root, "web/features/home/value.ts", `export const value: = ;`)
	if err := run(t.Context(), root, false, false, ""); err == nil {
		t.Fatal("invalid TypeScript succeeded")
	}
	if readOutput(t, root, cssPath) != css || readOutput(t, root, jsPath) != js {
		t.Fatal("failed build replaced usable assets")
	}
	writeFixture(t, root, "web/features/home/value.ts", `export const value: string = "fixture-after";`)
	writeFixture(t, root, "web/core/app.css", `@import "./missing.css";`)
	if err := run(t.Context(), root, false, false, ""); err == nil {
		t.Fatal("missing CSS import succeeded")
	}
	if readOutput(t, root, cssPath) != css || readOutput(t, root, jsPath) != js {
		t.Fatal("CSS import error replaced usable assets")
	}
	writeFixture(t, root, "web/core/app.css", `@import "./styles/colors.part.css"; body { color: var(--fixture); }`)
	writeFixture(t, root, "web/features/home/static/orphan.js.map", `retired entrypoint's source map`)
	if err := run(t.Context(), root, false, false, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readOutput(t, root, jsPath), "fixture-after") {
		t.Fatal("recovery did not write output")
	}
	if err := filepath.WalkDir(filepath.Join(root, "web"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".map") {
			t.Errorf("production retained source map %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readOutput(t, root, cssPath), "sourceMappingURL") || readOutput(t, root, "core/static/vendor.js") != `this must never be compiled !!!` {
		t.Fatal("production references maps or changed a vendored file")
	}
}

func waitFor(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("fixture watch condition timed out")
}

func TestWatchImportsNotifiesOnlySuccessAndStops(t *testing.T) {
	root := fixture(t)
	var notifications atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Error("notification is not POST")
		}
		notifications.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx, root, true, true, server.URL) }()
	joined := false
	t.Cleanup(func() {
		cancel()
		if !joined {
			<-done
		}
	})
	waitFor(t, func() bool { return notifications.Load() >= 1 })
	outputContains := func(name, want string) bool {
		data, _ := os.ReadFile(filepath.Join(root, "web", name))
		return strings.Contains(string(data), want)
	}
	writeFixture(t, root, "web/core/styles/colors.part.css", `:root { --fixture: blue; }`)
	waitFor(t, func() bool {
		return outputContains("core/static/styles/app.css", "--fixture: blue") && notifications.Load() >= 2
	})
	writeFixture(t, root, "web/features/home/value.ts", `export const value: string = "watch-after";`)
	waitFor(t, func() bool {
		return outputContains("features/home/static/app.js", "watch-after") && notifications.Load() >= 3
	})
	writeFixture(t, root, "web/features/home/label.js", `export const label = "javascript-after";`)
	waitFor(t, func() bool {
		return outputContains("features/home/static/app.js", "javascript-after") && notifications.Load() >= 4
	})
	before := readOutput(t, root, "features/home/static/app.js")
	count := notifications.Load()
	writeFixture(t, root, "web/features/home/value.ts", `export const value: = ;`)
	time.Sleep(750 * time.Millisecond) // allow esbuild's polling cycle to observe the compile error
	if notifications.Load() != count || readOutput(t, root, "features/home/static/app.js") != before {
		t.Fatal("watch error notified or destroyed previous output")
	}
	writeFixture(t, root, "web/features/home/value.ts", `export const value: string = "watch-recovered";`)
	waitFor(t, func() bool {
		return outputContains("features/home/static/app.js", "watch-recovered") && notifications.Load() > count
	})
	count = notifications.Load()
	time.Sleep(500 * time.Millisecond)
	if notifications.Load() != count {
		t.Fatal("generated output created a watch loop")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		joined = true
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not stop")
	}
}
