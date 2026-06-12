package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// Default values
const (
	defaultPort    = 8443
	defaultMaxSize = 50 * 1024 * 1024 // 50 MB
)

func main() {
	// --- Flag parsing ---
	port := flag.Int("port", lookupEnvInt("PORT", defaultPort), "HTTPS server port")
	maxSize := flag.Int("max-size", lookupEnvInt("MAX_UPLOAD_SIZE", defaultMaxSize), "Maximum upload file size in bytes")
	certDir := flag.String("cert-dir", lookupEnvStr("CERT_DIR", defaultCertDir()), "Directory for TLS certificates")
	uploadDir := flag.String("upload-dir", lookupEnvStr("UPLOAD_DIR", defaultUploadDir()), "Upload directory")
	flag.Parse()

	// --- Structured logging ---
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	slog.Info("starting tmp-file server",
		"port", *port,
		"max_size", *maxSize,
		"cert_dir", *certDir,
		"upload_dir", *uploadDir,
	)

	// --- Create upload directory and check writability ---
	if err := os.MkdirAll(*uploadDir, 0755); err != nil {
		slog.Error("failed to create upload directory", "dir", *uploadDir, "error", err)
		os.Exit(1)
	}
	if err := checkWritable(*uploadDir); err != nil {
		slog.Error("upload directory not writable", "dir", *uploadDir, "error", err)
		os.Exit(1)
	}

	// --- Ensure certificate ---
	if err := EnsureCert(*certDir); err != nil {
		slog.Error("failed to ensure certificate", "error", err)
		os.Exit(1)
	}

	// Read cert files
	certPEM, err := os.ReadFile(filepath.Join(*certDir, "cert.pem"))
	if err != nil {
		slog.Error("failed to read cert.pem", "error", err)
		os.Exit(1)
	}
	keyPEM, err := os.ReadFile(filepath.Join(*certDir, "key.pem"))
	if err != nil {
		slog.Error("failed to read key.pem", "error", err)
		os.Exit(1)
	}

	// Check if cert expires soon
	if CertExpiresSoon(certPEM, 30*24*time.Hour) {
		slog.Warn("certificate will expire within 30 days, consider regenerating")
	}

	// --- Create server ---
	srv := NewServer(*port, int64(*maxSize), *uploadDir, certPEM, keyPEM)
	if srv == nil {
		slog.Error("failed to create server")
		os.Exit(1)
	}

	// --- Listen ---
	plainListener, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		slog.Error("failed to listen", "error", err)
		os.Exit(1)
	}

	// Parse TLS certificate for the listener
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		slog.Error("failed to parse certificate", "error", err)
		os.Exit(1)
	}
	tlsListener := tls.NewListener(plainListener, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})

	// --- Start cleanup ---
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	StartCleanup(ctx, *uploadDir, 1*time.Hour, 5*time.Minute, 5*time.Minute)

	// --- Serve ---
	slog.Info("server listening",
		"addr", tlsListener.Addr().String(),
		"upload_dir", *uploadDir,
	)

	fmt.Printf("Open https://localhost:%d in your browser\n", *port)

	go func() {
		<-ctx.Done()
		slog.Info("shutting down server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	if err := srv.httpServer.Serve(tlsListener); err != nil && err != http.ErrServerClosed {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}

	slog.Info("server stopped")
}

// lookupEnvInt returns the integer value of the environment variable, or fallback.
func lookupEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

// lookupEnvStr returns the value of the environment variable, or fallback.
func lookupEnvStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// defaultCertDir returns the default certificate directory (~/.tmp-file/).
func defaultCertDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".tmp-file"
	}
	return filepath.Join(home, ".tmp-file")
}

// defaultUploadDir returns the default upload directory.
func defaultUploadDir() string {
	return "/tmp/u"
}

// checkWritable verifies that a directory is writable by creating and removing a test file.
func checkWritable(dir string) error {
	testFile := filepath.Join(dir, ".writetest")
	if err := os.WriteFile(testFile, []byte("writability-check"), 0644); err != nil {
		return fmt.Errorf("directory not writable: %w", err)
	}
	os.Remove(testFile)
	return nil
}
