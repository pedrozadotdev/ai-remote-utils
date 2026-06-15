package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// ---- LookupProxy ----

func TestLookupProxy_Found(t *testing.T) {
	db := testProxyDB(t, map[string]int{"myapp": 3000, "api": 8080})

	port, ok := LookupProxy("myapp.test", db)
	if !ok || port != 3000 {
		t.Errorf("LookupProxy('myapp.test') = (%d, %v), want (3000, true)", port, ok)
	}

	port, ok = LookupProxy("api.test", db)
	if !ok || port != 8080 {
		t.Errorf("LookupProxy('api.test') = (%d, %v), want (8080, true)", port, ok)
	}
}

func TestLookupProxy_NotFound(t *testing.T) {
	db := testProxyDB(t, map[string]int{"myapp": 3000})

	port, ok := LookupProxy("unknown.test", db)
	if ok || port != 0 {
		t.Errorf("LookupProxy('unknown.test') = (%d, %v), want (0, false)", port, ok)
	}
}

func TestLookupProxy_ReservedTmp(t *testing.T) {
	db := testProxyDB(t, map[string]int{"myapp": 3000})

	port, ok := LookupProxy("tmp.test", db)
	if ok || port != 0 {
		t.Errorf("LookupProxy('tmp.test') = (%d, %v), want (0, false)", port, ok)
	}
}

func TestLookupProxy_StripsPortSuffix(t *testing.T) {
	db := testProxyDB(t, map[string]int{"myapp": 3000})

	port, ok := LookupProxy("myapp.test:443", db)
	if !ok || port != 3000 {
		t.Errorf("LookupProxy('myapp.test:443') = (%d, %v), want (3000, true)", port, ok)
	}
}

func TestLookupProxy_NoSubdomain(t *testing.T) {
	db := testProxyDB(t, map[string]int{"myapp": 3000})

	tests := []struct {
		host string
		desc string
	}{
		{"test", "no subdomain (no dot)"},
		{".test", "empty subdomain"},
		{"", "empty string"},
		{"myapp", "no .test suffix"},
		{"example.com", "non-.test domain"},
	}
	for _, tc := range tests {
		port, ok := LookupProxy(tc.host, db)
		if ok || port != 0 {
			t.Errorf("LookupProxy(%q) = (%d, %v), want (0, false) [%s]",
				tc.host, port, ok, tc.desc)
		}
	}
}

func TestLookupProxy_MultipleLabels(t *testing.T) {
	db := testProxyDB(t, map[string]int{"snake.aru-test": 3000, "foo.bar": 8080})

	// Multi-label subdomains (e.g., "snake.aru-test.test") are supported
	port, ok := LookupProxy("snake.aru-test.test", db)
	if !ok || port != 3000 {
		t.Errorf("LookupProxy('snake.aru-test.test') = (%d, %v), want (3000, true)", port, ok)
	}

	port, ok = LookupProxy("FOO.BAR.TEST", db)
	if !ok || port != 8080 {
		t.Errorf("LookupProxy('FOO.BAR.TEST') = (%d, %v), want (8080, true)", port, ok)
	}
}

func TestLookupProxy_CaseInsensitive(t *testing.T) {
	db := testProxyDB(t, map[string]int{"myapp": 3000})

	// Lookup is case-insensitive on the hostname
	port, ok := LookupProxy("MyApp.test", db)
	if !ok || port != 3000 {
		t.Errorf("LookupProxy('MyApp.test') = (%d, %v), want (3000, true)", port, ok)
	}

	port, ok = LookupProxy("MYAPP.TEST", db)
	if !ok || port != 3000 {
		t.Errorf("LookupProxy('MYAPP.TEST') = (%d, %v), want (3000, true)", port, ok)
	}
}

func TestLookupProxy_NilDB(t *testing.T) {
	port, ok := LookupProxy("myapp.test", nil)
	if ok || port != 0 {
		t.Errorf("LookupProxy with nil db = (%d, %v), want (0, false)", port, ok)
	}
}

func TestLookupProxy_EmptyDB(t *testing.T) {
	db := testProxyDB(t, nil)

	port, ok := LookupProxy("anything.test", db)
	if ok || port != 0 {
		t.Errorf("LookupProxy with empty db = (%d, %v), want (0, false)", port, ok)
	}
}

// ---- Reverse proxy (unchanged) ----

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
