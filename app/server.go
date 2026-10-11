package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"poople/lib/errorlog"
)

// Serve owns listener and backend, joining HTTP users before closing storage.
// Backend work uses Backend.Run, never a request-owned resource lifetime.
func Serve(ctx context.Context, backend *Backend, listener net.Listener, handler http.Handler) (err error) {
	defer func() { err = errors.Join(err, backend.Close()) }()
	defer listener.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	requests, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var active sync.WaitGroup
	stopping := false
	tracked := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if stopping {
			mu.Unlock()
			http.Error(w, "Server is stopping.", http.StatusServiceUnavailable)
			return
		}
		active.Add(1)
		mu.Unlock()
		defer active.Done()
		// Bound stalled writes for this app's finite SSE patches. Each request
		// owns its write deadline; Server.WriteTimeout stays unset.
		if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
			errorlog.Request(r, "set response deadline", err)
			http.Error(w, "Unable to serve request.", http.StatusInternalServerError)
			return
		}
		handler.ServeHTTP(w, r)
	})
	server := &http.Server{
		Handler: tracked, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, IdleTimeout: time.Minute,
		MaxHeaderBytes: 32 << 10,
		BaseContext:    func(net.Listener) context.Context { return requests },
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	select {
	case err = <-served:
	case <-ctx.Done():
	}
	shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
	shutdownErr := server.Shutdown(shutdown)
	stop()
	// Shutdown may time out; Close interrupts I/O but does not join handlers.
	closeErr := server.Close()
	cancel()
	mu.Lock()
	stopping = true
	mu.Unlock()
	active.Wait()
	if err == nil {
		err = <-served
	}
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return errors.Join(err, shutdownErr, closeErr)
}
