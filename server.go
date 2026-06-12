package main

import (
	"context"
	"crypto/tls"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
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

// NewServer creates a new Server with virtual host routing.
// - tmp.test → upload handler + static files (with security headers)
// - <port>.test → reverse proxy (without security headers)
// - anything else → 404
func NewServer(maxSize int64, uploadDir string, certPEM, keyPEM []byte) *Server {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		slog.Error("failed to parse TLS certificate", "error", err)
		return nil
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	// Create handlers
	uploadHandler := &UploadHandler{
		BaseDir: uploadDir,
		MaxSize: maxSize,
		NameGen: &NameGenerator{},
	}

	staticHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	})

	// Upload mux with security headers
	uploadMux := http.NewServeMux()
	uploadMux.Handle("/upload", uploadHandler)
	uploadMux.Handle("/", staticHandler)
	securedUpload := securityHeaders(uploadMux)

	// Reverse proxy handler (no security headers)
	proxyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		port, ok := ParseTestSubdomain(r.Host)
		if !ok {
			http.Error(w, "Bad Request: invalid test subdomain", http.StatusBadRequest)
			return
		}
		proxy := NewReverseProxy(port)
		proxy.ServeHTTP(w, r)
	})

	// Virtual host router
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		// Strip port suffix from host (e.g., "tmp.test:443" → "tmp.test")
		if idx := strings.LastIndex(host, ":"); idx >= 0 {
			portPart := host[idx+1:]
			// Only strip if it looks like a port number
			if len(portPart) > 0 && portPart[0] >= '0' && portPart[0] <= '9' {
				host = host[:idx]
			}
		}
		host = strings.ToLower(host)

		switch {
		case host == "tmp.test":
			securedUpload.ServeHTTP(w, r)
		case strings.HasSuffix(host, ".test"):
			proxyHandler.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	srv := &Server{
		httpServer: &http.Server{
			Handler: router,
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
