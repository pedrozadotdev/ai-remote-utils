package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// blockedProxyPorts are ports that should not be proxied to prevent loops.
var blockedProxyPorts = map[int]bool{
	53:  true, // DNS
	80:  true, // HTTP redirect
	443: true, // HTTPS
}

// LookupProxy looks up a named proxy by hostname. It extracts the subdomain
// from a hostname like "myapp.test" or "myapp.test:443", converts to lowercase,
// and looks up the name in the given ProxyDB. Returns the port and true if found.
// Returns false for reserved names like "tmp.test", non-.test domains, etc.
func LookupProxy(host string, db *ProxyDB) (int, bool) {
	if db == nil {
		return 0, false
	}

	// Strip port suffix if present (e.g., "myapp.test:443" → "myapp.test")
	if idx := strings.LastIndex(host, ":"); idx >= 0 {
		portStr := host[idx+1:]
		if _, err := strconv.Atoi(portStr); err == nil {
			host = host[:idx]
		}
	}

	// Convert to lowercase for case-insensitive matching
	host = strings.ToLower(host)

	// Must end with ".test"
	if !strings.HasSuffix(host, ".test") {
		return 0, false
	}

	// Remove ".test" suffix
	name := strings.TrimSuffix(host, ".test")

	// Must have exactly one label (no dots)
	if name == "" || strings.Contains(name, ".") {
		return 0, false
	}

	// "tmp" and "test" are reserved
	if name == "tmp" || name == "test" {
		return 0, false
	}

	// Look up in the proxy DB
	port, ok := db.Get(name)
	return port, ok
}

// NewReverseProxy creates an httputil.ReverseProxy that forwards requests to
// localhost:<port>. The Host header is preserved as the original *.test hostname
// (not rewritten to localhost). WebSocket upgrades work via the default transport.
func NewReverseProxy(port int) *httputil.ReverseProxy {
	target := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("localhost:%d", port),
	}

	proxy := &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			// Preserve original Host header (upstream sees *.test hostname)
			// Only rewrite the URL target to localhost:<port>
			r.URL.Scheme = target.Scheme
			r.URL.Host = target.Host
			// r.Host is intentionally NOT modified — upstream sees *.test hostname
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Warn("proxy error", "host", r.Host, "error", err)
			http.Error(w, "Bad Gateway: upstream unreachable", http.StatusBadGateway)
		},
		Transport: &http.Transport{
			IdleConnTimeout: 90 * time.Second,
		},
	}

	return proxy
}
