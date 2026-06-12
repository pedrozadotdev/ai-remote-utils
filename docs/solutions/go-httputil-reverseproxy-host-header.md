---
title: httputil.ReverseProxy — preserving upstream Host header
category: architecture
severity: medium
tags:
  - go
  - reverse-proxy
  - httputil
  - host-header
  - websocket
applies_when:
  - Implementing a reverse proxy with Go's httputil.ReverseProxy
  - Needing to preserve the original Host header for the upstream server
  - Adding WebSocket support to a Go reverse proxy
  - Debugging "upstream received wrong Host header" in proxy setups
---

# Problem

When using Go's `net/http/httputil.ReverseProxy`, the `Director` function modifies the outgoing request — but it's easy to accidentally overwrite `r.Host` (the `Host` header the upstream sees) when you only meant to change the TCP routing target.

## Wrong behavior (common mistake)

```go
proxy := &httputil.ReverseProxy{
    Director: func(r *http.Request) {
        r.URL.Scheme = "http"
        r.URL.Host = "localhost:8080"
        r.Host = "localhost:8080"  // ⚠️ BUG: overwrites upstream Host header
    },
}
```

The upstream server receives `Host: localhost:8080` instead of the original hostname (e.g., `Host: myapp.test`). This breaks virtual hosting in upstream servers like Vite, Next.js, or any framework that routes based on the Host header.

## Solution

Only modify `r.URL.Scheme` and `r.URL.Host` for routing. Leave `r.Host` untouched:

```go
func NewReverseProxy(port int) *httputil.ReverseProxy {
    target := &url.URL{
        Scheme: "http",
        Host:   fmt.Sprintf("localhost:%d", port),
    }

    proxy := &httputil.ReverseProxy{
        Director: func(r *http.Request) {
            // Rewrite routing target (TCP connection destination)
            r.URL.Scheme = target.Scheme
            r.URL.Host = target.Host
            // r.Host is intentionally NOT modified
            // Upstream sees the original hostname (e.g., "myapp.test")
        },
        ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
            slog.Warn("proxy error", "host", r.Host, "error", err)
            http.Error(w, "Bad Gateway: upstream unreachable", http.StatusBadGateway)
        },
    }
    return proxy
}
```

## WebSocket support

Go 1.20+ `httputil.ReverseProxy` handles WebSocket upgrades automatically when using the default `Transport`. Do NOT set a custom `RoundTripper` that might strip upgrade headers:

```go
// ✅ This works — WebSocket upgrades pass through
proxy := &httputil.ReverseProxy{
    Director: myDirector,
    Transport: &http.Transport{
        IdleConnTimeout: 90 * time.Second,
    },
}
```

## Verification

Start a local echo server and test:

```bash
# Terminal 1: start a test server
python3 -m http.server 3000

# Terminal 2: test the proxy
curl -k -H "Host: 3000.test" https://localhost/proxy-path
# The upstream server should see Host: 3000.test, NOT Host: localhost:3000
```

Or in Go tests:

```go
func TestReverseProxy_PreservesHostHeader(t *testing.T) {
    var upstreamHost string
    upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        upstreamHost = r.Host
    }))
    defer upstream.Close()

    _, port, _ := net.SplitHostPort(upstream.Listener.Addr().String())
    proxyPort, _ := strconv.Atoi(port)
    proxy := NewReverseProxy(proxyPort)

    // ... send request with Host: "3000.test" ...
    // upstreamHost should be "3000.test", not "localhost:<port>"
}
```

## Why this works

- `r.URL.Host` controls where the TCP connection is made (the routing target).
- `r.Host` sets the value of the `Host` HTTP header sent to the upstream.
- They are independent fields in Go's `http.Request` — modifying one does NOT affect the other.
