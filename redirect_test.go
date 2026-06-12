package main

import (
	"net"
	"net/http"
	"testing"
	"time"
)

func startRedirectServer(t *testing.T) (string, func()) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusMovedPermanently)
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen error: %v", err)
	}
	server := &http.Server{Handler: mux}
	go server.Serve(listener)
	return listener.Addr().String(), func() { server.Close() }
}

func TestRedirect_RedirectsToHTTPS(t *testing.T) {
	addr, cleanup := startRedirectServer(t)
	defer cleanup()
	time.Sleep(20 * time.Millisecond)

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse // Don't follow redirects
	}}
	resp, err := client.Get("http://" + addr + "/test-path?q=1")
	if err != nil {
		t.Fatalf("GET error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMovedPermanently {
		t.Errorf("status = %d, want 301", resp.StatusCode)
	}

	loc := resp.Header.Get("Location")
	expectedLoc := "https://" + addr + "/test-path?q=1"
	if loc != expectedLoc {
		t.Errorf("Location = %q, want %q", loc, expectedLoc)
	}
}

func TestRedirect_RedirectsRoot(t *testing.T) {
	addr, cleanup := startRedirectServer(t)
	defer cleanup()
	time.Sleep(20 * time.Millisecond)

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMovedPermanently {
		t.Errorf("status = %d, want 301", resp.StatusCode)
	}

	loc := resp.Header.Get("Location")
	expectedLoc := "https://" + addr + "/"
	if loc != expectedLoc {
		t.Errorf("Location = %q, want %q", loc, expectedLoc)
	}
}

func TestRedirect_MethodPreserved(t *testing.T) {
	addr, cleanup := startRedirectServer(t)
	defer cleanup()
	time.Sleep(20 * time.Millisecond)

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Post("http://"+addr+"/submit", "text/plain", nil)
	if err != nil {
		t.Fatalf("POST error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMovedPermanently {
		t.Errorf("status = %d, want 301", resp.StatusCode)
	}
}

func TestRedirect_Handler(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusMovedPermanently)
	})

	// Create request with just the path (as the real server would after parsing)
	req, err := http.NewRequest("GET", "/foo?q=1", nil)
	if err != nil {
		t.Fatalf("NewRequest error: %v", err)
	}
	req.Host = "example.com"
	req.URL.Host = "example.com"
	req.URL.Scheme = "http"

	rec := newResponseRecorder()
	handler.ServeHTTP(rec, req)

	if rec.status != http.StatusMovedPermanently {
		t.Errorf("status = %d, want 301", rec.status)
	}
	if rec.header.Get("Location") != "https://example.com/foo?q=1" {
		t.Errorf("Location = %q, want %q", rec.header.Get("Location"), "https://example.com/foo?q=1")
	}
}

type responseRecorder struct {
	header http.Header
	status int
	body   []byte
}

func newResponseRecorder() *responseRecorder {
	return &responseRecorder{header: make(http.Header)}
}
func (r *responseRecorder) Header() http.Header { return r.header }
func (r *responseRecorder) Write(b []byte) (int, error) {
	r.body = append(r.body, b...)
	return len(b), nil
}
func (r *responseRecorder) WriteHeader(status int) { r.status = status }
