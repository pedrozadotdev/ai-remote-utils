package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
)

// StartRedirect starts an HTTP server on port 80 that redirects all requests
// to the same host and path on HTTPS (port 443). Returns nil (non-fatal) if
// binding port 80 fails.
func StartRedirect(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusMovedPermanently)
	})

	server := &http.Server{Addr: ":80", Handler: mux}

	listener, err := net.Listen("tcp", ":80")
	if err != nil {
		slog.Warn("HTTP redirect server disabled: cannot bind port 80", "error", err)
		slog.Warn("  Access services directly via https://<host>:443")
		return nil // non-fatal
	}

	slog.Info("HTTP redirect server listening", "addr", listener.Addr().String())

	go func() {
		<-ctx.Done()
		server.Close()
		slog.Debug("HTTP redirect server stopped")
	}()

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Warn("HTTP redirect server error", "error", err)
		}
	}()

	return nil
}
