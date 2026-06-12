# tmp-file — ai-remote-utils

A single Go binary that serves an HTTPS file upload webapp on LAN devices. Users upload files (especially images) via a drag-drop/paste UI, get back a local filesystem path with `@` prefix to paste into AI agents. Files are cleaned up automatically after 1 hour.

## Features

- **Drag-and-drop upload** with image preview
- **Multi-file upload** — upload multiple files at once
- **Paste from clipboard** — paste images directly from clipboard
- **Copy-to-clipboard** — `@`-prefixed path ready for AI agents
- **Auto-cleanup** — files older than 1 hour removed automatically
- **Self-signed HTTPS** — auto-generated TLS certificate with 10-year validity
- **Configurable** — port, max file size, upload directory, cert directory via flags or env vars
- **No dependencies** — uses only Go standard library

## Quick Start

```bash
# Build
go build -o tmp-file .

# Run (defaults: port 8443, max 50MB, /tmp/u for uploads)
./tmp-file

# Open in browser
open https://localhost:8443
```

## Usage

### Command-line flags

| Flag | Default | Env var | Description |
|------|---------|---------|-------------|
| `-port` | `8443` | `PORT` | HTTPS server port |
| `-max-size` | `52428800` (50 MB) | `MAX_UPLOAD_SIZE` | Maximum upload file size in bytes |
| `-cert-dir` | `~/.tmp-file/` | `CERT_DIR` | Directory for TLS certificates |
| `-upload-dir` | `/tmp/u` | `UPLOAD_DIR` | Upload directory |

Flags override environment variables. Environment variables override defaults.

### API

**POST /upload**

Accepts multipart form data with field `file` or `files`.

Response (single file):
```json
{"path": "@/tmp/u/ab7x.jpg"}
```

Response (multiple files):
```json
{"paths": ["@/tmp/u/ab7x.jpg", "@/tmp/u/cd8y.png"]}
```

### Frontend

Open `https://localhost:8443` in a browser for a dark-themed upload UI:
- **Click** the upload button to select files (stages them — press Upload to confirm)
- **Drag and drop** files onto the drop zone (auto-uploads immediately)
- **Paste** images from clipboard (auto-uploads immediately)
- Each uploaded file gets a card with preview, path, and copy button

## Architecture

```
main.go         — entry point, flag parsing, signal handling
server.go       — HTTP server setup, routing, security headers middleware
upload.go       — file upload handler, name generation
cleanup.go      — background file cleanup goroutine
cert.go         — self-signed TLS certificate generation and management
static.go       — embedded static files (frontend)
static/         — frontend HTML/CSS/JS assets
```

### Packages

Uses only Go standard library packages: `net/http`, `crypto/tls`, `crypto/x509`, `crypto/rand`, `embed`, `log/slog`, `os`, `sync`, `time`, `io`, `mime/multipart`, `flag`.

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

### Test coverage

Current coverage: **61.0%** (non-`main()` functions only). Key areas:
- `RandomAlphanum` — 85.7%
- `EnsureCert`, cleanup logic — well-covered
- `main()` — excluded by convention (flag parsing extracted for testability)

## Security

- **TLS 1.2 minimum** — connections below TLS 1.2 are rejected
- **Self-signed cert** — auto-generated with 10-year validity, SAN for all IPs
- **Key permissions** — private key stored at `0600` (owner-only)
- **Security headers** — `X-Content-Type-Options`, `X-Frame-Options`, `Strict-Transport-Security`, `Referrer-Policy`, `Permissions-Policy`
- **No file-type validation** — all file types accepted by design

## Cleaning up

Files in the upload directory older than 1 hour are automatically removed. The cleanup runs every 5 minutes and also runs on startup.
