package core

import (
	"net"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
)

// ClientKey is the rate-limit key for the request's client: the address that
// web/app.go's chi ClientIPFrom* middleware resolved (the connection, or the
// trusted proxy's X-Forwarded-For entry), with IPv6 grouped by /64. Without a
// resolved address it falls back to the connection's.
func ClientKey(r *http.Request) string {
	ip := middleware.GetClientIP(r.Context())
	if ip == "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip = host
	}
	return httprate.CanonicalizeIP(ip)
}
