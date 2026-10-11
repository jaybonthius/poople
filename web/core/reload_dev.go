//go:build dev

package core

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/starfederation/datastar-go/datastar"

	"poople/lib/errorlog"
)

// Development is true only in `task live` builds (-tags dev).
const Development = true

// Northstar's Datastar reload route, with per-tab delivery and application cancellation.
func reloadRoutes(ctx context.Context, router chi.Router) {
	instance := rand.Text()
	var mu sync.Mutex
	var revision uint64
	changed := make(chan struct{})

	router.Post("/force-reload", func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || !ip.IsLoopback() {
			http.Error(w, "Loopback peers only.", http.StatusForbidden)
			return
		}
		mu.Lock()
		revision++
		close(changed)
		changed = make(chan struct{})
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	router.Get("/reload", func(w http.ResponseWriter, r *http.Request) {
		sse := datastar.NewSSE(w, r, func(_ *datastar.ServerSentEventGenerator) {
			w.Header().Set("Cache-Control", "no-store")
		})
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()
		for {
			mu.Lock()
			id, next := fmt.Sprintf("%s:%d", instance, revision), changed
			mu.Unlock()
			// Remember the initial connection without refreshing; reconnects compare versions.
			if err := sse.ExecuteScript(fmt.Sprintf(`if (window.__poopleReload !== undefined && window.__poopleReload !== %q) window.location.reload(); window.__poopleReload = %q;`, id, id)); err != nil {
				if ctx.Err() == nil {
					errorlog.Request(r, "write reload event", err)
				}
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-r.Context().Done():
				return
			case <-next:
			case <-heartbeat.C:
			}
		}
	})
}
