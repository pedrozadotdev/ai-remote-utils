package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestParseTestSubdomain_Valid(t *testing.T) {
	tests := []struct {
		host     string
		wantPort int
		wantOK   bool
	}{
		{"3000.test", 3000, true},
		{"8080.test", 8080, true},
		{"1.test", 1, true},
		{"65535.test", 65535, true},
		{"3000.TEST", 3000, true},
		{"3000.Test", 3000, true},
	}
	for _, tc := range tests {
		port, ok := ParseTestSubdomain(tc.host)
		if ok != tc.wantOK || port != tc.wantPort {
			t.Errorf("ParseTestSubdomain(%q) = (%d, %v), want (%d, %v)",
				tc.host, port, ok, tc.wantPort, tc.wantOK)
		}
	}
}

func TestParseTestSubdomain_Reserved(t *testing.T) {
	tests := []struct {
		host   string
		reason string
	}{
		{"tmp.test", "reserved for upload handler"},
	}
	for _, tc := range tests {
		port, ok := ParseTestSubdomain(tc.host)
		if ok || port != 0 {
			t.Errorf("ParseTestSubdomain(%q) = (%d, %v), want (0, false) [%s]",
				tc.host, port, ok, tc.reason)
		}
	}
}

func TestParseTestSubdomain_Invalid(t *testing.T) {
	tests := []struct {
		host   string
		reason string
	}{
		{"abc.test", "non-numeric subdomain"},
		{"test", "no subdomain (no dot)"},
		{".test", "empty subdomain"},
		{"", "empty string"},
		{"abc.def.test", "too many labels"},
	}
	for _, tc := range tests {
		port, ok := ParseTestSubdomain(tc.host)
		if ok || port != 0 {
			t.Errorf("ParseTestSubdomain(%q) = (%d, %v), want (0, false) [%s]",
				tc.host, port, ok, tc.reason)
		}
	}
}

func TestParseTestSubdomain_BlockedPorts(t *testing.T) {
	blocked := []int{53, 80, 443}
	for _, p := range blocked {
		host := fmt.Sprintf("%d.test", p)
		port, ok := ParseTestSubdomain(host)
		if ok || port != 0 {
			t.Errorf("ParseTestSubdomain(%q) = (%d, %v), want (0, false) [blocked port]",
				host, port, ok)
		}
	}
}

func TestParseTestSubdomain_OutOfRange(t *testing.T) {
	tests := []struct {
		host   string
		reason string
	}{
		{"0.test", "zero"},
		{"-1.test", "negative"},
		{"99999.test", "overflow"},
		{"70000.test", "overflow"},
	}
	for _, tc := range tests {
		port, ok := ParseTestSubdomain(tc.host)
		if ok || port != 0 {
			t.Errorf("ParseTestSubdomain(%q) = (%d, %v), want (0, false) [%s]",
				tc.host, port, ok, tc.reason)
		}
	}
}

func TestParseTestSubdomain_StripsPortSuffix(t *testing.T) {
	port, ok := ParseTestSubdomain("3000.test:443")
	if !ok || port != 3000 {
		t.Errorf("ParseTestSubdomain(%q) = (%d, %v), want (3000, true)", "3000.test:443", port, ok)
	}
}

func TestReverseProxy_ProxiesHTTP(t *testing.T) {
	// Upstream echo server that records the Host header it receives
	var upstreamHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHost = r.Host
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello from upstream"))
	}))
	defer upstream.Close()

	// Extract the port from the upstream listener
	_, upstreamPort, err := net.SplitHostPort(upstream.Listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort error: %v", err)
	}
	proxyPort, _ := strconv.Atoi(upstreamPort)
	proxy := NewReverseProxy(proxyPort)

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = "3000.test"
		r.URL.Host = "localhost:" + upstreamPort
		r.URL.Scheme = "http"
		proxy.ServeHTTP(w, r)
	}))
	defer proxyServer.Close()

	resp, err := http.Get(proxyServer.URL)
	if err != nil {
		t.Fatalf("GET proxy error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "hello from upstream") {
		t.Errorf("body = %q, want to contain 'hello from upstream'", string(body))
	}
	// Verify Host header was preserved (not rewritten to localhost:port)
	if upstreamHost != "3000.test" {
		t.Errorf("upstream received Host = %q, want %q", upstreamHost, "3000.test")
	}
}

func TestReverseProxy_502OnUnreachable(t *testing.T) {
	proxy := NewReverseProxy(39999)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = "39999.test"
		r.URL.Host = "127.0.0.1:1"
		r.URL.Scheme = "http"
		proxy.ServeHTTP(w, r)
	}))
	defer proxyServer.Close()

	resp, err := http.Get(proxyServer.URL)
	if err != nil {
		t.Fatalf("GET proxy error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
}

func TestNewReverseProxy_NonNil(t *testing.T) {
	proxy := NewReverseProxy(8080)
	if proxy == nil {
		t.Fatal("NewReverseProxy returned nil")
	}
}
