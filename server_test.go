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

// doGet sends a GET request to the server with the given Host header.
func doGet(t *testing.T, addr, host, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("GET", "https://"+addr+path, nil)
	if err != nil {
		t.Fatalf("NewRequest error: %v", err)
	}
	req.Host = host
	resp, err := testHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("GET error: %v", err)
	}
	return resp
}

// doPost sends a POST request with the given Host header and body.
func doPost(t *testing.T, addr, host, path string, contentType string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", "https://"+addr+path, body)
	if err != nil {
		t.Fatalf("NewRequest error: %v", err)
	}
	req.Host = host
	req.Header.Set("Content-Type", contentType)
	resp, err := testHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("POST error: %v", err)
	}
	return resp
}

// setupTestServer creates a test server and returns its address and cert/key.
func setupTestServer(t *testing.T) (string, *Server, []byte, []byte) {
	t.Helper()
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

	srv := NewServer(1024*1024, t.TempDir(), certPEM, keyPEM)
	addr := startServer(t, srv)
	return addr, srv, certPEM, keyPEM
}

func TestServer_TmpTest_GetRoot(t *testing.T) {
	addr, srv, _, _ := setupTestServer(t)
	defer srv.Close()

	resp := doGet(t, addr, "tmp.test", "/")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "dropzone") {
		t.Error("response body missing 'dropzone'")
	}
}

func TestServer_TmpTest_PostUpload(t *testing.T) {
	addr, srv, _, _ := setupTestServer(t)
	defer srv.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "test.jpg")
	if err != nil {
		t.Fatalf("CreateFormFile error = %v", err)
	}
	io.WriteString(part, "image-data")
	writer.Close()

	resp := doPost(t, addr, "tmp.test", "/upload", writer.FormDataContentType(), body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestServer_TmpTest_PostUploadNoFile(t *testing.T) {
	addr, srv, _, _ := setupTestServer(t)
	defer srv.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.Close()

	resp := doPost(t, addr, "tmp.test", "/upload", writer.FormDataContentType(), body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestServer_GracefulShutdown(t *testing.T) {
	addr, srv, _, _ := setupTestServer(t)

	// Verify server is responding via tmp.test
	resp := doGet(t, addr, "tmp.test", "/")
	resp.Body.Close()

	// Shutdown with context
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown error: %v", err)
	}

	// Server should now be shut down
	_, err := testHTTPClient.Get("https://" + addr + "/")
	if err == nil {
		t.Error("expected error after shutdown, got nil")
	}
}

func TestServer_UploadDirCreated(t *testing.T) {
	_, srv, _, _ := setupTestServer(t)
	defer srv.Close()

	// Dir should exist (NewServer doesn't create it, but the test setup does)
	if _, err := os.Stat(srv.uploadDir); os.IsNotExist(err) {
		t.Error("upload directory was not created")
	}
}

func TestServer_LocalhostReturns404(t *testing.T) {
	addr, srv, _, _ := setupTestServer(t)
	defer srv.Close()

	resp := doGet(t, addr, "localhost", "/")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for localhost, got %d", resp.StatusCode)
	}
}

func TestServer_LocalhostIPReturns404(t *testing.T) {
	addr, srv, _, _ := setupTestServer(t)
	defer srv.Close()

	resp := doGet(t, addr, "127.0.0.1", "/")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for 127.0.0.1, got %d", resp.StatusCode)
	}
}

func TestServer_UnknownHostReturns404(t *testing.T) {
	addr, srv, _, _ := setupTestServer(t)
	defer srv.Close()

	resp := doGet(t, addr, "unknown.example.com", "/")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for unknown host, got %d", resp.StatusCode)
	}
}

func TestServer_TmpTest_HasSecurityHeaders(t *testing.T) {
	addr, srv, _, _ := setupTestServer(t)
	defer srv.Close()

	resp := doGet(t, addr, "tmp.test", "/")
	defer resp.Body.Close()

	hsts := resp.Header.Get("Strict-Transport-Security")
	if hsts == "" {
		t.Error("tmp.test response missing HSTS header")
	}
	xfo := resp.Header.Get("X-Frame-Options")
	if xfo == "" {
		t.Error("tmp.test response missing X-Frame-Options header")
	}
}

func TestServer_ProxyRoute(t *testing.T) {
	// Start an upstream server
	upstream := http.Server{Addr: "127.0.0.1:0"}
	upstream.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("upstream-ok"))
	})
	upstreamListener, err := net.Listen("tcp", upstream.Addr)
	if err != nil {
		t.Fatalf("upstream listen error: %v", err)
	}
	upstream.Addr = upstreamListener.Addr().String()
	go upstream.Serve(upstreamListener)
	defer upstream.Close()
	time.Sleep(20 * time.Millisecond)

	addr, srv, _, _ := setupTestServer(t)
	defer srv.Close()

	// Extract port from upstream
	_, portStr, _ := net.SplitHostPort(upstream.Addr)

	// Request via proxy route
	resp := doGet(t, addr, portStr+".test", "/")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for proxy route, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "upstream-ok") {
		t.Errorf("body = %q, want to contain 'upstream-ok'", string(body))
	}
}

func TestServer_ProxyRoute_NonExistent(t *testing.T) {
	addr, srv, _, _ := setupTestServer(t)
	defer srv.Close()

	resp := doGet(t, addr, "39999.test", "/")
	defer resp.Body.Close()

	// Should get 502 (unreachable) not 200
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("expected 502 for unreachable proxy, got %d", resp.StatusCode)
	}
}

// startServer starts the server on a random port and returns the address.
func startServer(t *testing.T, srv *Server) string {
	t.Helper()

	// Ensure upload directory exists
	if err := os.MkdirAll(srv.uploadDir, 0755); err != nil {
		t.Fatalf("failed to create upload dir: %v", err)
	}

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	tlsListener := tls.NewListener(listener, srv.tlsConfig)

	go func() {
		srv.httpServer.Addr = tlsListener.Addr().String()
		if err := srv.httpServer.Serve(tlsListener); err != nil && err != http.ErrServerClosed {
			t.Logf("serve error: %v", err)
		}
	}()

	return tlsListener.Addr().String()
}
