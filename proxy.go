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

// ParseTestSubdomain extracts a port number from a hostname like "3000.test"
// or "3000.test:443". Returns the port and true if the subdomain is a valid
// numeric port. Returns false for reserved names like "tmp.test", non-numeric
// subdomains, blocked ports, and out-of-range values.
func ParseTestSubdomain(host string) (int, bool) {
	// Strip port suffix if present (e.g., "3000.test:443" → "3000.test")
	if idx := strings.LastIndex(host, ":"); idx >= 0 {
		// Only strip if it looks like a port number after the colon
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
	subdomain := strings.TrimSuffix(host, ".test")

	// Must have exactly one label (no dots)
	if subdomain == "" || strings.Contains(subdomain, ".") {
		return 0, false
	}

	// "tmp" is reserved for the upload handler
	if subdomain == "tmp" {
		return 0, false
	}

	// Parse as integer
	port, err := strconv.Atoi(subdomain)
	if err != nil {
		return 0, false
	}

	// Validate range
	if port < 1 || port > 65535 {
		return 0, false
	}

	// Block ports that would create loops
	if blockedProxyPorts[port] {
		return 0, false
	}

	return port, true
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
