// Package errorlog logs failures at the boundary that handles or drops them.
// Callers exclude expected domain refusals and pass only code-owned operations
// and safe identifiers. Never pass request data or private content as attributes.
package errorlog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/go-chi/chi/v5"
)

// Error leaves ordinary cancellation quiet, but retains server-side deadlines.
func Error(ctx context.Context, operation string, err error, attrs ...any) {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	if message := Safe(err); message != "" {
		slog.ErrorContext(ctx, operation, append([]any{"error", message}, attrs...)...)
	}
}

// Reported preserves a returned failure without logging it again at shutdown.
func Reported(err error) error {
	if err == nil {
		return nil
	}
	return &reported{err}
}

type reported struct{ error }

func (e *reported) Unwrap() error { return e.error }

// Request never falls back to the raw path: even path segments can hold secrets.
// Middleware before route matching has an empty pattern; the operation identifies it.
func Request(r *http.Request, operation string, err error) {
	// A disconnected socket can fail before net/http cancels the request.
	if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, net.ErrClosed) {
		return
	}
	route := ""
	if ctx := chi.RouteContext(r.Context()); ctx != nil {
		route = ctx.RoutePattern()
	}
	Error(r.Context(), operation, err, "route", route)
}

var (
	urls    = regexp.MustCompile(`(?i)(?:https?|wss?)://[^\s"'<>]+`)
	secrets = regexp.MustCompile(`(?i)(?:sk-[a-z0-9_*\-]+|bearer\s+[^\s,;]+|(?:token|password|secret|api[_-]?key|code)=[^\s&]+)`)
	quoted  = regexp.MustCompile(`"(?:\\.|[^"\\])*"|'[^']*'`)
)

// Safe removes data-bearing wrappers (paths, URLs, input values) while keeping
// causes such as SQLite errors and deadlines. Remote errors can supply a bounded
// LogValue instead of their private diagnostic text. This is not a body scrubber:
// callers must never log arbitrary provider, tool or document output.
func Safe(err error) string {
	if err == nil {
		return ""
	}
	switch e := err.(type) {
	case *reported:
		return ""
	case *os.PathError:
		return e.Op + ": " + Safe(e.Err)
	case *os.LinkError:
		return e.Op + ": " + Safe(e.Err)
	case *url.Error:
		return Safe(e.Err)
	case *net.OpError:
		return e.Op + ": " + Safe(e.Err)
	case *net.DNSError:
		return "DNS: " + e.Err
	case *strconv.NumError:
		return e.Func + ": " + Safe(e.Err)
	case *json.UnmarshalTypeError:
		return "JSON type mismatch"
	case *json.SyntaxError:
		return "invalid JSON"
	case slog.LogValuer:
		return e.LogValue().String()
	case interface{ Unwrap() []error }:
		var parts []string
		for _, cause := range e.Unwrap() {
			if cause != nil {
				if text := Safe(cause); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "; ")
	case interface{ Unwrap() error }:
		if cause := e.Unwrap(); cause != nil {
			return Safe(cause)
		}
	}
	text := urls.ReplaceAllString(err.Error(), "[URL]")
	text = secrets.ReplaceAllString(text, "[redacted]")
	text = quoted.ReplaceAllString(text, "[redacted]")
	return fmt.Sprintf("%.2000s", text)
}
