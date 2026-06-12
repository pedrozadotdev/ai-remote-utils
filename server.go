package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
)

// Server wraps the HTTP server and its dependencies.
type Server struct {
	httpServer *http.Server
	tlsConfig  *tls.Config
	uploadDir  string
	maxSize    int64
	certPEM    []byte
	keyPEM     []byte
}

// NewServer creates a new Server with the given configuration.
func NewServer(port int, maxSize int64, uploadDir string, certPEM, keyPEM []byte) *Server {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		slog.Error("failed to parse TLS certificate", "error", err)
		return nil
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	mux := http.NewServeMux()

	// Upload handler
	uploadHandler := &UploadHandler{
		BaseDir: uploadDir,
		MaxSize: maxSize,
		NameGen: &NameGenerator{},
	}
	mux.Handle("/upload", uploadHandler)

	// Static file handler (serves embedded index.html)
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(staticFS, "index.html")
		if err != nil {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	}))

	srv := &Server{
		httpServer: &http.Server{
			Addr:    fmt.Sprintf(":%d", port),
			Handler: securityHeaders(mux),
		},
		tlsConfig: tlsConfig,
		uploadDir: uploadDir,
		maxSize:   maxSize,
		certPEM:   certPEM,
		keyPEM:    keyPEM,
	}

	return srv
}

// securityHeaders wraps an http.Handler with security-related HTTP headers.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// Close immediately closes the server.
func (s *Server) Close() error {
	return s.httpServer.Close()
}
