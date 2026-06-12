package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testHTTPClient is an HTTP client that skips TLS verification.
var testHTTPClient = &http.Client{
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	},
}

func TestServer_GetRoot(t *testing.T) {
	certDir := t.TempDir()
	if err := EnsureCert(certDir); err != nil {
		t.Fatalf("EnsureCert error = %v", err)
	}
	certPEM, err := os.ReadFile(filepath.Join(certDir, "cert.pem"))
	if err != nil {
		t.Fatalf("failed to read cert.pem: %v", err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(certDir, "key.pem"))
	if err != nil {
		t.Fatalf("failed to read key.pem: %v", err)
	}

	srv := NewServer(0, 1024*1024, t.TempDir(), certPEM, keyPEM)
	defer srv.Close()

	addr := startServer(t, srv)

	resp, err := testHTTPClient.Get("https://" + addr + "/")
	if err != nil {
		t.Fatalf("GET / error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "dropzone") {
		t.Error("response body missing 'dropzone'")
	}
}

func TestServer_PostUpload(t *testing.T) {
	certDir := t.TempDir()
	if err := EnsureCert(certDir); err != nil {
		t.Fatalf("EnsureCert error = %v", err)
	}
	certPEM, err := os.ReadFile(filepath.Join(certDir, "cert.pem"))
	if err != nil {
		t.Fatalf("failed to read cert.pem: %v", err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(certDir, "key.pem"))
	if err != nil {
		t.Fatalf("failed to read key.pem: %v", err)
	}

	srv := NewServer(0, 1024*1024, t.TempDir(), certPEM, keyPEM)
	defer srv.Close()

	addr := startServer(t, srv)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "test.jpg")
	if err != nil {
		t.Fatalf("CreateFormFile error = %v", err)
	}
	io.WriteString(part, "image-data")
	writer.Close()

	resp, err := testHTTPClient.Post("https://"+addr+"/upload", writer.FormDataContentType(), body)
	if err != nil {
		t.Fatalf("POST /upload error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestServer_PostUploadNoFile(t *testing.T) {
	certDir := t.TempDir()
	if err := EnsureCert(certDir); err != nil {
		t.Fatalf("EnsureCert error = %v", err)
	}
	certPEM, err := os.ReadFile(filepath.Join(certDir, "cert.pem"))
	if err != nil {
		t.Fatalf("failed to read cert.pem: %v", err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(certDir, "key.pem"))
	if err != nil {
		t.Fatalf("failed to read key.pem: %v", err)
	}

	srv := NewServer(0, 1024*1024, t.TempDir(), certPEM, keyPEM)
	defer srv.Close()

	addr := startServer(t, srv)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.Close()

	resp, err := testHTTPClient.Post("https://"+addr+"/upload", writer.FormDataContentType(), body)
	if err != nil {
		t.Fatalf("POST /upload error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestServer_GracefulShutdown(t *testing.T) {
	certDir := t.TempDir()
	if err := EnsureCert(certDir); err != nil {
		t.Fatalf("EnsureCert error = %v", err)
	}
	certPEM, err := os.ReadFile(filepath.Join(certDir, "cert.pem"))
	if err != nil {
		t.Fatalf("failed to read cert.pem: %v", err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(certDir, "key.pem"))
	if err != nil {
		t.Fatalf("failed to read key.pem: %v", err)
	}

	srv := NewServer(0, 1024*1024, t.TempDir(), certPEM, keyPEM)

	addr := startServer(t, srv)

	// Verify server is responding
	resp, err := testHTTPClient.Get("https://" + addr + "/")
	if err != nil {
		t.Fatalf("GET / error: %v", err)
	}
	resp.Body.Close()

	// Shutdown with context
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown error: %v", err)
	}

	// Server should now be shut down
	_, err = testHTTPClient.Get("https://" + addr + "/")
	if err == nil {
		t.Error("expected error after shutdown, got nil")
	}
}

func TestServer_UploadDirCreated(t *testing.T) {
	certDir := t.TempDir()
	if err := EnsureCert(certDir); err != nil {
		t.Fatalf("EnsureCert error = %v", err)
	}
	certPEM, err := os.ReadFile(filepath.Join(certDir, "cert.pem"))
	if err != nil {
		t.Fatalf("failed to read cert.pem: %v", err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(certDir, "key.pem"))
	if err != nil {
		t.Fatalf("failed to read key.pem: %v", err)
	}

	srv := NewServer(0, 1024*1024, t.TempDir(), certPEM, keyPEM)

	addr := startServer(t, srv)
	defer srv.Close()

	// Trigger a request
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "test.jpg")
	io.WriteString(part, "data")
	writer.Close()

	resp, err := testHTTPClient.Post("https://"+addr+"/upload", writer.FormDataContentType(), body)
	if err != nil {
		t.Fatalf("POST /upload error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	// Dir should exist
	if _, err := os.Stat(srv.uploadDir); os.IsNotExist(err) {
		t.Error("upload directory was not created")
	}
}

// startServer starts the server on a random port and returns the address.
func startServer(t *testing.T, srv *Server) string {
	t.Helper()

	// Ensure upload directory exists
	if err := os.MkdirAll(srv.uploadDir, 0755); err != nil {
		t.Fatalf("failed to create upload dir: %v", err)
	}

	listener, err := net.Listen("tcp", srv.httpServer.Addr)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	tlsListener := tls.NewListener(listener, srv.tlsConfig)

	go func() {
		if err := srv.httpServer.Serve(tlsListener); err != nil && err != http.ErrServerClosed {
			t.Logf("serve error: %v", err)
		}
	}()

	return tlsListener.Addr().String()
}
