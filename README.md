# ai-remote-utils

A single Go binary that provides local development utilities for AI agent workflows:

- **Reverse proxy** — access any `localhost:<port>` via `https://<port>.test`
- **File upload** — drag-drop/paste file upload with `@`-prefixed path for AI agents
- **Built-in DNS** — automatically resolves `*.test` domains to your machine's IP (zero-config)
- **Auto-cleanup** — uploaded files clean up after 1 hour
- **No dependencies** — uses only Go standard library

## Quick Start

```bash
# Build (requires root for ports <1024)
go build -o ai-remote-utils .
sudo ./ai-remote-utils

# Or install as a systemd service
sudo cp ai-remote-utils /usr/local/bin/
sudo cp ai-remote-utils.service /etc/systemd/system/
sudo systemctl enable --now ai-remote-utils
```

## Features

### 🌐 Reverse proxy for `*.test` domains

Access any local dev server via `https://<port>.test` — no more `localhost:3000`:

```
https://3000.test/      →  http://localhost:3000/
https://8080.test/      →  http://localhost:8080/
```

- WebSocket support works automatically (Vite, Next.js hot-reload)
- Host header is preserved as `*.test` (upstream sees the original hostname)
- Blocked ports: 53 (DNS), 80 (HTTP), 443 (HTTPS) — prevents loops
- Invalid ports → 400 Bad Request; unreachable → 502 Bad Gateway

### 📁 File upload at `tmp.test`

Open `https://tmp.test/` for a dark-themed upload UI:
- **Click** to select files (stages them — press Upload to confirm)
- **Drag and drop** files onto the drop zone (auto-uploads immediately)
- **Paste** images from clipboard (auto-uploads immediately)
- Each uploaded file gets a card with preview, path, and copy button

### 🧠 Built-in DNS (port 53)

The built-in DNS server automatically resolves `*.test` domains to your machine's IP:

- Queries from local machine → `127.0.0.1` (works locally)
- Queries from LAN devices → your machine's LAN IP (works on network)
- Non-`.test` queries → NXDOMAIN (not an open resolver)

If port 53 is already in use (e.g., `systemd-resolved`), the server logs a warning and continues without DNS — use `/etc/hosts` as fallback.

### 🔄 Auto-redirect (port 80 → 443)

HTTP requests on port 80 are automatically redirected to HTTPS on port 443.

## Usage

### Setup DNS (one-time)

For the `*.test` domains to work, ensure the built-in DNS server can bind port 53:

```bash
# Check if systemd-resolved is using port 53
sudo systemctl stop systemd-resolved
sudo systemctl disable systemd-resolved

# Or use /etc/hosts as fallback:
echo "127.0.0.1 tmp.test 3000.test 8080.test" | sudo tee -a /etc/hosts
```

### Command-line flags

| Flag | Default | Env var | Description |
|------|---------|---------|-------------|
| `-port` | `443` | `PORT` | HTTPS server port |
| `-max-size` | `52428800` (50 MB) | `MAX_UPLOAD_SIZE` | Maximum upload file size in bytes |
| `-cert-dir` | `~/.ai-remote-utils/` | `CERT_DIR` | Directory for TLS certificates |
| `-upload-dir` | `/tmp/u` | `UPLOAD_DIR` | Upload directory |
| `--install-service` | `false` | — | Install systemd service and exit (no other flags needed) |

Flags override environment variables. Environment variables override defaults.

### Installing as a systemd service

```bash
# Move binary to final location first
sudo cp ai-remote-utils /usr/local/bin/

# Install the service (detects binary path and working directory automatically)
sudo /usr/local/bin/ai-remote-utils --install-service

# Then enable and start
sudo systemctl daemon-reload
sudo systemctl enable --now ai-remote-utils
```

The `--install-service` flag generates the service file at `/etc/systemd/system/ai-remote-utils.service` with the correct `ExecStart` and `WorkingDirectory` paths based on the binary's location.

### API

**POST /upload** (at `https://tmp.test/upload`)

Accepts multipart form data with field `file` or `files`.

Response (single file):
```json
{"path": "@/tmp/u/ab7x.jpg"}
```

Response (multiple files):
```json
{"paths": ["@/tmp/u/ab7x.jpg", "@/tmp/u/cd8y.png"]}
```

## Architecture

```
main.go         — entry point, three listeners (DNS :53, HTTP redirect :80, HTTPS :443)
server.go       — virtual host routing (tmp.test → upload, <port>.test → proxy)
proxy.go        — reverse proxy handler with WebSocket support
dns.go          — built-in DNS server for *.test domains
redirect.go     — HTTP → HTTPS redirect server
upload.go       — file upload handler, name generation
cleanup.go      — background file cleanup goroutine
cert.go         — self-signed TLS certificate with wildcard SANs (*.test, tmp.test)
static.go       — embedded static files (frontend)
static/         — frontend HTML/CSS/JS assets
```

### Listeners

| Port | Protocol | Service | Failure mode |
|------|----------|---------|-------------|
| 53 | UDP | DNS (`.test` → interface IP) | Non-fatal — logs warning, continues |
| 80 | TCP | HTTP → HTTPS redirect | Non-fatal — logs warning, continues |
| 443 | TCP | HTTPS (file upload + reverse proxy) | Fatal — core service |

### Virtual host routing

| Host | Routes to |
|------|-----------|
| `tmp.test` | File upload UI + upload API (with security headers) |
| `<port>.test` | Reverse proxy to `http://localhost:<port>` (no security headers) |
| anything else | 404 Not Found |

### Packages

Uses only Go standard library packages: `net/http`, `net/http/httputil`, `crypto/tls`, `crypto/x509`, `crypto/rand`, `embed`, `log/slog`, `net`, `os`, `sync`, `time`, `io`, `mime/multipart`, `flag`.

## Development

### Prerequisites

- Go 1.26.3 or later
- No external dependencies required

### Running tests

```bash
# Run all tests with race detection
go test -race -count=1 ./...

# Run with coverage
go test -race -count=1 -cover ./...

# View per-function coverage
go tool cover -func=coverage.out
```

## Security

- **TLS 1.2 minimum** — connections below TLS 1.2 are rejected
- **Self-signed cert** — auto-generated with 10-year validity, wildcard SAN for `*.test`, `tmp.test`, `localhost`
- **Key permissions** — private key stored at `0600` (owner-only)
- **Security headers** — applied only to file upload routes (`tmp.test`)
- **Reverse proxy** — no security headers on proxied responses (upstream controls its own headers)
- **No file-type validation** — all file types accepted by design

## Cleaning up

Files in the upload directory older than 1 hour are automatically removed. The cleanup runs every 5 minutes and also runs on startup.
