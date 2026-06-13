# aru

A single Go binary that provides local development utilities for AI agent workflows:

- **Reverse proxy** — access any `localhost:<port>` via named `https://<name>.test` URLs, managed via CLI
- **File upload** — drag-drop/paste file upload with `@`-prefixed path for AI agents
- **Built-in DNS** — automatically resolves `*.test` domains to your machine's IP (zero-config)
- **Worktree manager** — create/remove/open/list git worktrees with tmux sessions and RAM-backed data
- **Auto-cleanup** — uploaded files clean up after 1 hour
- **No dependencies** — uses only Go standard library

## Quick Start

```bash
# Build (requires root for ports <1024)
go build -o aru .
sudo ./aru

# Or install as a systemd service
sudo ./aru --install-service
sudo systemctl daemon-reload
sudo systemctl enable --now aru
```

## Features

### 🌐 Reverse proxy for `*.test` domains

Access any local dev server via a named `https://<name>.test` URL — no more `localhost:3000`:

```bash
# Add a proxy entry
aru proxy add --name=myapp --port=3000

# Now access it
https://myapp.test/  →  http://localhost:3000/

# List all proxies
aru proxy list

# Remove a proxy
aru proxy del --name=myapp
```

- Proxy entries are persisted in `~/.aru/proxies.json` (survives restarts)
- Edits to `proxies.json` are picked up at runtime (hot-reload via mtime check)
- WebSocket support works automatically (Vite, Next.js hot-reload)
- Host header is preserved as `*.test` (upstream sees the original hostname)
- Blocked ports: 53 (DNS), 80 (HTTP), 443 (HTTPS) — prevents loops
- Unknown proxy names → 404 Not Found

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

### 🔧 Worktree manager

Create isolated git worktrees with tmux sessions and RAM-backed data directories:

```bash
# Add a worktree for a feature branch (pulls latest, creates worktree, launches tmux)
aru worktree add my-feature

# Re-attach to an existing worktree's tmux session
aru worktree open my-feature

# Remove a worktree (cleans up RAM, kills tmux, deletes branch)
aru worktree del my-feature

# List all worktrees with current directory marker
aru worktree list
```

- Worktrees stored at `~/.aru/wt/<project>/<branch>`
- RAM-backed data at `~/.aru/ram/<project>/<branch>` (tmpfs via `syscall.Mount`)
- Data directory symlinked to `<worktree>/data` → RAM directory
- Tmux sessions managed via custom sockets at `~/.aru/sockets/<project>-<branch>.sock`
- Lifecycle hooks: `wt-setup.sh` (runs in tmux setup window with `PORT` env var), `wt-destroy.sh` (runs on deletion)
- RAM directory and symlink auto-recreated on `open` if missing (handle reboots)

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
| `-cert-dir` | `~/.aru/` | `CERT_DIR` | Directory for TLS certificates and proxy DB |
| `-upload-dir` | `/tmp/u` | `UPLOAD_DIR` | Upload directory |
| `--install-service` | `false` | — | Install systemd service and exit (no other flags needed) |

### Subcommands

Manage reverse proxy entries without restarting the server:

```bash
# Add a named proxy (persisted to ~/.aru/proxies.json)
aru proxy add --name=myapp --port=3000

# Delete a proxy
aru proxy del --name=myapp

# List all proxies
aru proxy list

# Worktree management
aru worktree add my-feature
aru worktree open my-feature
aru worktree del my-feature
aru worktree list
```

Flags override environment variables. Environment variables override defaults.

### Installing as a systemd service

```bash
# Move binary to final location first
sudo cp aru /usr/local/bin/

# Install the service (detects binary path and working directory automatically)
sudo /usr/local/bin/aru --install-service

# Then enable and start
sudo systemctl daemon-reload
sudo systemctl enable --now aru
```

The `--install-service` flag generates the service file at `/etc/systemd/system/aru.service` with the correct `ExecStart` and `WorkingDirectory` paths based on the binary's location.

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
main.go         — entry point, subcommand routing (proxy, worktree), three listeners
server.go       — virtual host routing (tmp.test → upload, <name>.test → proxy via ProxyDB)
proxy.go        — reverse proxy handler with WebSocket support, LookupProxy
proxydb.go      — persistent proxy database (JSON-backed, thread-safe, hot-reload)
worktree.go     — git worktree manager (add/del/open/list), RAM dir, tmux sessions
cert.go         — self-signed TLS certificate with wildcard SANs (*.test, tmp.test)
dns.go          — built-in DNS server for *.test domains
redirect.go     — HTTP → HTTPS redirect server
upload.go       — file upload handler, name generation
cleanup.go      — background file cleanup goroutine
static.go       — embedded static files (frontend)
static/         — frontend HTML/CSS/JS assets
```

### Data directory

All persistent data lives under `~/.aru/`:

```
~/.aru/
├── cert.pem          — TLS certificate
├── key.pem           — TLS private key (0600)
├── proxies.json      — Reverse proxy configuration
├── wt/               — Git worktrees (<project>/<branch>)
├── ram/              — RAM-backed data (tmpfs, <project>/<branch>)
└── sockets/          — Tmux control sockets (<project>-<branch>.sock)
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
| `<name>.test` | Reverse proxy to `http://localhost:<port>` via ProxyDB lookup (no security headers) |
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
