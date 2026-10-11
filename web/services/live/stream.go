// Package live streams complete state-derived views; commands never render through it.
package live

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"time"

	"poople/lib/errorlog"

	"github.com/a-h/templ"
	"github.com/starfederation/datastar-go/datastar"
)

// Stream renders on connect and after invalidation, capped at maxFPS.
// snapshot returns the view and its next-change channel from the same state snapshot.
func Stream(ctx context.Context, w http.ResponseWriter, r *http.Request, maxFPS int, snapshot func() (templ.Component, <-chan struct{}, error)) (err error) {
	defer func() {
		if ctx.Err() == nil {
			errorlog.Request(r, "stream live view", err)
		}
	}()
	if maxFPS <= 0 || time.Second/time.Duration(maxFPS) == 0 {
		return errors.New("UI frame rate must allow a positive frame interval")
	}
	rc := http.NewResponseController(w)
	renew := func() error {
		err := rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if errors.Is(err, http.ErrNotSupported) { // In-memory httptest writers have no socket.
			return nil
		}
		return err
	}
	if err := renew(); err != nil {
		return err
	}
	sse := datastar.NewSSE(w, r, datastar.WithCompression(datastar.WithServerPriority(), datastar.WithZstd(), datastar.WithBrotli(), datastar.WithGzip()), func(_ *datastar.ServerSentEventGenerator) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Add("Vary", "Accept-Encoding")
	})
	if err := sse.PatchSignals([]byte(`{"_streamConnected":true}`)); err != nil {
		return err
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	interval := time.Second / time.Duration(maxFPS)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.Context().Err(); err != nil {
			return err
		}
		view, changed, err := snapshot()
		if err != nil {
			return err
		}
		if err := renew(); err != nil {
			return err
		}
		if err := sse.PatchElementTempl(view); err != nil {
			return err
		}
		nextFrame := time.Now().Add(interval)
	wait:
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-r.Context().Done():
				return r.Context().Err()
			case <-changed:
				timer := time.NewTimer(time.Until(nextFrame))
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-r.Context().Done():
					timer.Stop()
					return r.Context().Err()
				case <-timer.C:
				}
				break wait
			case <-heartbeat.C:
				if err := renew(); err != nil {
					return err
				}
				// Keep idle connections alive without rerendering their view.
				if err := sse.PatchSignals([]byte(`{}`)); err != nil {
					return err
				}
			}
		}
	}
}

// Either closes when any channel fires, or never once ctx ends (a nil channel
// never fires). It lets one view follow several invalidation topics; it receives
// at most one value, so a buffered wake it does not take stays for the next frame.
func Either(ctx context.Context, channels ...<-chan struct{}) <-chan struct{} {
	out := make(chan struct{})
	cases := make([]reflect.SelectCase, 0, len(channels)+1)
	cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())})
	for _, c := range channels {
		cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(c)})
	}
	go func() {
		if chosen, _, _ := reflect.Select(cases); chosen > 0 {
			close(out)
		}
	}()
	return out
}
