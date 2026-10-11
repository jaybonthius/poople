//go:build dev

package core_test

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"poople/app"
	"poople/web"
	"poople/web/core"

	"github.com/go-chi/chi/v5"
)

func readRevision(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data: elements ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "data: elements "))
		}
	}
}

func TestReloadBroadcastReconnectAndRequestCancellation(t *testing.T) {
	router := chi.NewRouter()
	core.Routes(t.Context(), router)
	server := httptest.NewServer(router)
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	var bodies []io.ReadCloser
	var readers []*bufio.Reader
	for range 2 {
		resp, err := client.Get(server.URL + "/reload")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("reload stream headers missing")
		}
		bodies = append(bodies, resp.Body)
		readers = append(readers, bufio.NewReader(resp.Body))
	}
	first := readRevision(t, readers[0])
	if first != readRevision(t, readers[1]) {
		t.Fatal("tabs received different initial revisions")
	}
	resp, err := client.Post(server.URL+"/force-reload", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatal("loopback notification failed")
	}
	next := readRevision(t, readers[0])
	if next == first || readRevision(t, readers[1]) != next {
		t.Fatal("reload was not broadcast to all tabs")
	}
	bodies[0].Close() // request cancellation must not hold the subscription or consume other tabs' updates
	reconnected, err := client.Get(server.URL + "/reload")
	if err != nil {
		t.Fatal(err)
	}
	defer reconnected.Body.Close()
	if readRevision(t, bufio.NewReader(reconnected.Body)) != next {
		t.Fatal("reconnect lost current revision")
	}
	fresh := chi.NewRouter()
	core.Routes(t.Context(), fresh)
	server2 := httptest.NewServer(fresh)
	defer server2.Close()
	restarted, err := client.Get(server2.URL + "/reload")
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Body.Close()
	if readRevision(t, bufio.NewReader(restarted.Body)) == next {
		t.Fatal("server instance change did not change revision")
	}
}

func TestReloadLoopbackPeerAndCrossOriginProtection(t *testing.T) {
	router := chi.NewRouter()
	router.Use(http.NewCrossOriginProtection().Handler)
	core.Routes(t.Context(), router)
	for _, test := range []struct {
		method, peer, origin string
		want                 int
	}{
		{"POST", "127.0.0.1:1234", "", 204},
		{"POST", "[::1]:1234", "", 204},
		{"POST", "192.0.2.1:1234", "", 403},
		{"POST", "invalid", "", 403},
		{"POST", "127.0.0.1:1234", "https://untrusted.invalid", 403},
		{"GET", "127.0.0.1:1234", "", 405},
	} {
		r := httptest.NewRequest(test.method, "http://fixture.test/force-reload", nil)
		r.RemoteAddr = test.peer
		r.Header.Set("Origin", test.origin)
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != test.want {
			t.Fatalf("%s peer=%s origin=%s status=%d want=%d", test.method, test.peer, test.origin, w.Code, test.want)
		}
	}
	var body bytes.Buffer
	if err := core.Shell("fixture", nil).Render(t.Context(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.String(), `data-init="@get('/reload'`) || strings.Contains(body.String(), "/reload.js") {
		t.Fatal("dev HTML missing Datastar subscription or included the obsolete client")
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/reload.js", nil))
	if w.Code != 404 {
		t.Fatal("obsolete reload client was served")
	}
}

func TestOpenReloadDrainsBeforeServeReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	dir := t.TempDir()
	backend, err := app.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	handler, err := web.NewApp(ctx, backend, false, 30)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- app.Serve(ctx, backend, listener, handler) }()
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	resp, err := client.Get("http://" + listener.Addr().String() + "/reload")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	readRevision(t, bufio.NewReader(resp.Body))
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("open reload stream prevented clean Shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reload did not drain before shutdown deadline")
	}
	reopened, err := app.Open(t.Context(), dir)
	if err != nil {
		t.Fatalf("reopen after drained shutdown: %v", err)
	}
	reopened.Close()
}
