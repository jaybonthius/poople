package live_test

import (
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"poople/web/services/live"

	"github.com/a-h/templ"
	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func TestStreamCompressionFlushBroadcastAndReconnect(t *testing.T) {
	for _, encoding := range []string{"identity", "br", "zstd", "gzip", "gzip, br, zstd", "gzip, br", "deflate", "unknown"} {
		t.Run(encoding, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var mu sync.Mutex
			value := 0
			changed := make(chan struct{})
			snapshot := func() (templ.Component, <-chan struct{}, error) {
				mu.Lock()
				defer mu.Unlock()
				current := value
				return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
					_, err := fmt.Fprintf(w, `<main id="main">%d</main>`, current)
					return err
				}), changed, nil
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = live.Stream(ctx, w, r, 10, snapshot)
			}))
			defer server.Close()
			client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
			defer client.CloseIdleConnections()
			open := func() (*http.Response, *bufio.Scanner) {
				t.Helper()
				request, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
				request.Header.Set("Accept-Encoding", encoding)
				response, err := client.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { response.Body.Close() })
				want := ""
				for _, supported := range []string{"zstd", "br", "gzip"} {
					if strings.Contains(encoding, supported) {
						want = supported
						break
					}
				}
				if response.Header.Get("Content-Encoding") != want || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Vary") != "Accept-Encoding" {
					t.Fatalf("stream headers: %v", response.Header)
				}
				var reader io.Reader = response.Body
				switch want {
				case "br":
					reader = brotli.NewReader(reader)
				case "zstd":
					decoder, err := zstd.NewReader(reader, zstd.WithDecoderConcurrency(1))
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(decoder.Close)
					reader = decoder
				case "gzip":
					decoder, err := gzip.NewReader(reader)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { decoder.Close() })
					reader = decoder
				}
				return response, bufio.NewScanner(reader)
			}
			readView := func(scanner *bufio.Scanner, expected int) {
				t.Helper()
				want := fmt.Sprintf("<main id=\"main\">%d</main>", expected)
				for scanner.Scan() {
					if strings.Contains(scanner.Text(), "<main") {
						if !strings.Contains(scanner.Text(), want) {
							t.Fatalf("view=%q, want %q", scanner.Text(), want)
						}
						return
					}
				}
				t.Fatalf("view not flushed while stream remained open: %v", scanner.Err())
			}
			first, a := open()
			readView(a, 0)
			second, b := open()
			readView(b, 0)
			mu.Lock()
			// Multiple changes share one latest-state frame, rather than a queued history.
			for range 20 {
				value++
				close(changed)
				changed = make(chan struct{})
			}
			mu.Unlock()
			readView(a, 20)
			readView(b, 20)
			first.Body.Close()
			second.Body.Close()
			third, c := open()
			readView(c, 20)
			third.Body.Close()
		})
	}
}

// A view can follow several topics; an ended request never wakes it.
func TestEitherWakesOnAnyTopicAndLeavesOthersQueued(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var never <-chan struct{}
	second := make(chan struct{}, 1)
	first := make(chan struct{})
	second <- struct{}{}
	select {
	case <-live.Either(ctx, never, first, second):
	case <-time.After(2 * time.Second):
		t.Fatal("third topic did not wake the view")
	}
	close(first)
	select {
	case <-live.Either(ctx, never, first, second):
	case <-time.After(2 * time.Second):
		t.Fatal("second topic did not wake the view")
	}
	stopped, stop := context.WithCancel(ctx)
	stop()
	select {
	case <-live.Either(stopped, never, never):
		t.Fatal("ended context woke the view")
	case <-time.After(50 * time.Millisecond):
	}
}
