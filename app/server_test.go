package app_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"poople/app"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func await(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for fixture lifecycle")
	}
}

func startHTTP(t *testing.T, backend *app.Backend, handler http.Handler) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Serve(ctx, backend, listener, handler) }()
	t.Cleanup(cancel)
	return "http://" + listener.Addr().String(), cancel, done
}

func TestServeJoinsHTTPBeforeClosingBackend(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	backend, err := app.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		// Shutdown must keep the borrowed store usable until this handler returns.
		if err := backend.Database().Write(r.Context(), func(conn *sqlite.Conn) error {
			return sqlitex.Execute(conn, "INSERT INTO app_meta(key, value) VALUES ('last-request', 'committed')", nil)
		}); err != nil {
			t.Error(err)
		}
		w.WriteHeader(204)
	})
	origin, cancel, served := startHTTP(t, backend, handler)
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		resp, err := client.Get(origin)
		if err != nil {
			t.Error(err)
			return
		}
		resp.Body.Close()
		if resp.StatusCode != 204 {
			t.Errorf("joined request status=%d", resp.StatusCode)
		}
	}()
	await(t, entered)
	cancel()
	select {
	case err := <-served:
		t.Fatalf("Serve returned before active HTTP finished: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	await(t, requestDone)
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	reopened, err := app.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var value string
	if err := reopened.Database().Read(t.Context(), func(conn *sqlite.Conn) error {
		return sqlitex.Execute(conn, "SELECT value FROM app_meta WHERE key = 'last-request'", &sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error { value = stmt.ColumnText(0); return nil },
		})
	}); err != nil || value != "committed" {
		t.Fatalf("shutdown lost joined request write: %q %v", value, err)
	}
}

func TestServeRequestCancellationAndHeaderBounds(t *testing.T) {
	t.Parallel()
	backend, err := app.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	entered, exited := make(chan struct{}), make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(exited)
	})
	origin, stop, served := startHTTP(t, backend, handler)
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	oversized, err := http.NewRequestWithContext(t.Context(), "GET", origin, nil)
	if err != nil {
		t.Fatal(err)
	}
	oversized.Header.Set("X-Fixture", strings.Repeat("a", 64<<10))
	resp, err := client.Do(oversized)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
		t.Fatalf("oversized headers status=%d", resp.StatusCode)
	}
	requestCtx, cancelRequest := context.WithCancel(t.Context())
	defer cancelRequest()
	req, err := http.NewRequestWithContext(requestCtx, "GET", origin, nil)
	if err != nil {
		t.Fatal(err)
	}
	requestDone := make(chan error, 1)
	go func() {
		resp, err := client.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		requestDone <- err
	}()
	await(t, entered)
	cancelRequest()
	if err := <-requestDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("request cancellation=%v", err)
	}
	await(t, exited)
	if err := backend.Database().Write(t.Context(), func(conn *sqlite.Conn) error {
		return sqlitex.Execute(conn, "INSERT INTO app_meta(key, value) VALUES ('still-usable', 'yes')", nil)
	}); err != nil {
		t.Fatalf("request cancellation closed storage: %v", err)
	}
	stop()
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}

func TestServeFailureClosesBackend(t *testing.T) {
	t.Parallel()
	backend, err := app.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener.Close()
	if err := app.Serve(t.Context(), backend, listener, http.NotFoundHandler()); err == nil {
		t.Fatal("failed listener served successfully")
	}
	if err := backend.Database().Read(t.Context(), func(*sqlite.Conn) error { return nil }); err == nil {
		t.Fatal("failed Serve left storage open")
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
