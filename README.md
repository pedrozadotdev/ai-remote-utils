# ai-remote-utils

A single Go binary that provides local development utilities for AI agent workflows:

- **Reverse proxy** — access any `localhost:<port>` via named `https://<name>.<domain>` URLs, managed via CLI
- **Custom domain** — drop a wildcard certificate into the cert dir and aru routes every service under its suffix
- **File upload** — drag-drop/paste file upload with `@`-prefixed path for AI agents
- **Built-in DNS** — automatically resolves `*.<domain>` domains to your machine's IP (zero-config)
- **Worktree manager** — create/remove/open/list git worktrees with tmux sessions and RAM-backed data
- **Auto-cleanup** — uploaded files clean up after 1 hour
- **Minimal dependencies** — only `golang.org/x/net/ipv4` (Go team-maintained sub-repo); everything else is Go standard library

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

### 🏷️ Custom domain from a wildcard certificate

By default aru generates a self-signed certificate for `*.test` and routes every service under `.test`. To use your own domain, put a wildcard certificate in the cert directory and restart aru:

```bash
cp cert.pem key.pem ~/.aru/
sudo systemctl restart aru
```

aru reads the **first wildcard DNS SAN** from `cert.pem` and adopts its suffix everywhere — reverse proxy, upload host, and built-in DNS:

| Certificate SANs | Routing domain | Upload host | Proxy hosts |
|------------------|----------------|-------------|-------------|
| `*.example.com` | `example.com` | `tmp.example.com` | `myapp.example.com` |
| `*.example.com`, `*.lab.internal` | `example.com` (first wildcard) | `tmp.example.com` | `myapp.example.com` |
| none — aru generates `*.test` | `test` | `tmp.test` | `myapp.test` |

The certificate is authoritative, so aru protects it:

- aru **never rewrites** a certificate pair it did not generate — your files are used as-is
- a certificate without any wildcard SAN fails with an explanatory error (add a `*.yourdomain` SAN, or remove the pair so aru can generate `*.test`)
- an expired certificate you supplied is reported rather than replaced; only aru's own expired certificate is regenerated
- a `cert.pem` without `key.pem` (or a mismatched pair) is an error instead of silently overwriting your files
- the adopted suffix must resolve to this machine — use your own DNS, or let the built-in DNS answer A-record queries for it

Only the **first** wildcard is used, so `*.example.com` + `*.lab.internal` routes `<name>.example.com` and leaves `*.lab.internal` alone.

### 🌐 Reverse proxy for `<name>.<domain>`

Access any local dev server via a named `https://<name>.<domain>` URL — no more `localhost:3000`. `<domain>` is `test` for the generated certificate, or the suffix adopted from your wildcard certificate. Names can contain dots for multi-level subdomains (e.g., `https://api.myapp.test/` → `http://localhost:3001/`).

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
- Host header is preserved as the original proxy hostname (upstream sees `<name>.<domain>`)
- Blocked ports: 53 (DNS), 80 (HTTP), 443 (HTTPS) — prevents loops
- Unknown proxy names → 404 Not Found
- **Multi-proxy support**: `aru.json` can register multiple proxies for multiple services in one worktree (see [aru.json schema](#🔐-trust-model-for-arujson-commands))
- **Dots in names**: Proxy names may contain dots for multi-level subdomains (e.g., `api.myapp.test`)

### 📁 File upload at `tmp.<domain>`

Open `https://tmp.test/` (or `https://tmp.<domain>/` with a custom certificate) for a dark-themed upload UI:

- **Click** to select files (stages them — press Upload to confirm)
- **Drag and drop** files onto the drop zone (auto-uploads immediately)
- **Paste** images from clipboard (auto-uploads immediately)
- Each uploaded file gets a card with preview, path, and copy button

### 🧠 Built-in DNS (port 53)

The built-in DNS server resolves the `*.<domain>` names that aru actually serves (`*.test` by default) to your machine's IP:

- It answers **only for names that exist**: the upload host (`tmp.<domain>`) and every name registered in the proxy DB (`aru proxy add`, `aru worktree add`). An unregistered name under the routing domain gets **NXDOMAIN** instead of silently pointing at this machine
- New proxy entries are picked up on the next query for that name — no server restart needed
- It uses **IP_PKTINFO** socket control messages (via `golang.org/x/net/ipv4`) to extract the **exact destination IP** each DNS query arrived on — no subnet matching, no heuristics
- This works correctly with **any interface type**: Tailscale (`/32`), LAN (`/24`), Docker bridges, VPNs — the kernel tells us the IP directly
- Queries from a remote Tailscale client → the server's **Tailscale IP** (e.g., `100.x.x.x`)
- Queries from local machine → `127.0.0.1`
- Queries from LAN devices → your machine's LAN IP
- Non-A queries and queries outside the routing domain → REFUSED (allows client fallback to secondary DNS)

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
- RAM-backed data at `~/.aru/wt/<project>/<branch>/<path>` (tmpfs via `syscall.Mount`), configured via `aru.json` `ramdir` — each entry gets its own tmpfs mount directly in the worktree, no symlinks
- Tmux sessions managed via custom sockets at `~/.aru/sockets/<project>-<branch>.sock`
- **Config-driven lifecycle via `aru.json`**: declaratively specify setup commands, teardown commands, tmux windows, and reverse proxy registration — see [aru.json schema](#🔐-trust-model-for-arujson-commands) below
- Port persistence: allocated ports survive reboots via `~/.aru/state/<project>/<branch>/ports.json`
- Setup idempotency: `setup_oneshot: true` runs setup only once per worktree session (marker at `~/.aru/state/<project>/<branch>/setup-complete`)
- RAM directory entries auto-recreated on `open` using `syscall.Statfs` to detect reboot (distinguishes tmpfs from leftover ext4 mount-point directories; skips non-empty fallback dirs to preserve user data)
- Setup and teardown commands run verbatim via the detected shell (`$SHELL` → `bash` → `sh`) (see trust model below)

#### 🔐 Trust model for `aru.json` commands

The `aru.json` config file (in your worktree) lets you declaratively specify setup commands, teardown commands, and tmux window commands. **All commands in `aru.json` run verbatim in your shell as the user invoking `aru`**. This is the same trust model as:

- `Makefile` recipes (`make` runs `bash -c` on each target)
- `package.json` scripts (`npm run` executes arbitrary shell)
- `Dockerfile` `RUN` instructions (executed at build time)

Because you already have full shell access, the commands in `aru.json` provide no additional privilege escalation — they are equivalent to typing them in a terminal yourself.

**Security implications:**

- Anyone who can write to `aru.json` in a worktree can run commands as the user who invokes `aru` on that worktree.
- Review changes to `aru.json` in pull requests with the same care as shell scripts.
- If you copy `aru.json` from an untrusted source, treat it as code execution and review every command.
- There is no in-process sandbox; commands run with full user permissions.

**Defense in depth (what aru does):**

- Env values are shell-escaped (`strconv.Quote`) before substitution, so a malicious env value cannot inject shell metacharacters.
- Project/branch placeholders (`<PROJECT>`, `<BRANCH>`) are substituted as opaque text into the resolved config.
- `<PORTn>` placeholders are substituted with allocated port numbers (no user input).
- Commands themselves are run as-is — by design, per the trust model above.

#### `aru.json` schema

```json
{
  "version": 1,
  "worktree": {
    "setup": ["npm install", "npm run build"],
    "setup_oneshot": true,
    "teardown": ["rm -rf .cache"]
  },
  "tmux": [
    { "name": "dev",  "command": "npm run dev",   "env": { "PORT": "<PORT1>" } },
    { "name": "misc", "command": "bash" }
  ],
  "proxy": [
    { "name": "<BRANCH>.<PROJECT>", "port": "<PORT1>" },
    { "name": "api",                "port": "<PORT2>" }
  ],
  "ramdir": [
    { "path": "data",       "size": "200M" },
    { "path": "cache/build", "size": "500M" }
  ]
}
```

- `version` — schema version (currently optional, reserved for future migrations)
- `worktree.setup` — list of shell commands to run on `aru worktree add` and `aru worktree open`. Commands run verbatim via the detected shell (see trust model below for security implications)
- `worktree.setup_oneshot` — if `true`, setup runs only once per worktree session. A marker file at `~/.aru/state/<project>/<branch>/setup-complete` records that setup has run; subsequent opens skip setup. The marker is removed when the worktree is deleted. To force re-run, delete the marker file manually.
- `worktree.teardown` — list of shell commands to run on `aru worktree del`
- `tmux` — **ordered array** of tmux window definitions. The first entry is created via `new-session`, subsequent entries via `new-window`. Each entry has:
  - `name` — window name (placeholders supported)
  - `command` — shell command to run (runs verbatim via the detected shell)
  - `env` — optional map of environment variables (values are shell-escaped)
- `proxy` — **array** of reverse proxy registrations (supports multiple proxies). Each entry has:
  - `name` — proxy name with `<PROJECT>`/`<BRANCH>` placeholders for dynamic naming. Can contain dots for multi-level subdomains (e.g., `api.myapp.test`).
  - `port` — port with `<PORT1>`/`<PORT2>`/... placeholders (allocated from 1024-9999)
- `ramdir` — **array** of RAM-backed tmpfs directory entries. Each entry has:
  - `path` — worktree-relative path for the tmpfs mount point (e.g., `"data"`, `"cache/build"`). Parent directories are created automatically.
  - `size` — optional tmpfs size specifier (e.g., `"200M"`, `"1G"`). Defaults to `"200M"` when omitted.

**SIGINT behavior:** Tmux commands get a `trap ':' INT` handler so the outer shell survives Ctrl+C and drops into a fallback shell. Child processes remain interruptible (default SIG_DFL disposition). This prevents accidentally closing the entire tmux window when pressing Ctrl+C on a dev server.

Placeholders:

- `<PROJECT>` — directory name of the main worktree
- `<BRANCH>` — branch name being checked out
- `<PORT1>`, `<PORT2>`, ... — allocated open ports (numbers map to specific ports, reused across `add` and `open` for consistency)

### 🔄 Auto-redirect (port 80 → 443)

HTTP requests on port 80 are automatically redirected to HTTPS on port 443.

## Usage

### Setup DNS (one-time)

For the `*.<domain>` domains to work, ensure the built-in DNS server can bind port 53:

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

**POST /upload** (at `https://tmp.<domain>/upload`)

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
server.go       — virtual host routing (tmp.<domain> → upload, <name>.<domain> → proxy via ProxyDB)
domain.go       — routing domain resolution (wildcard SAN → suffix), host matching, upload host
proxy.go        — reverse proxy handler with WebSocket support, LookupProxy
proxydb.go      — persistent proxy database (JSON-backed, thread-safe, hot-reload)
worktree.go     — git worktree manager (add/del/open/list), aru.json config, RAM dir, tmux sessions
aruconfig.go    — aru.json config types, parsing, struct-walking placeholder resolution
cert.go         — certificate adoption (wildcard SAN → routing domain) and self-signed generation
dns.go          — built-in DNS server for *.<domain> domains (IP_PKTINFO destination-IP extraction)
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
├── cert.pem          — TLS certificate (yours, or generated with *.test SANs)
├── key.pem           — TLS private key (0600)
├── proxies.json      — Reverse proxy configuration
├── state/            — Per-worktree state (ports.json, setup-complete)
├── wt/               — Git worktrees (<project>/<branch>)
└── sockets/          — Tmux control sockets (<project>-<branch>.sock)
```

### Listeners

| Port | Protocol | Service | Failure mode |
|------|----------|---------|-------------|
| 53 | UDP | DNS (`*.<domain>` → interface IP) | Non-fatal — logs warning, continues |
| 80 | TCP | HTTP → HTTPS redirect | Non-fatal — logs warning, continues |
| 443 | TCP | HTTPS (file upload + reverse proxy) | Fatal — core service |

### Virtual host routing

| Host | Routes to |
|------|-----------|
| `tmp.<domain>` | File upload UI + upload API (with security headers) |
| `<name>.<domain>` | Reverse proxy to `http://localhost:<port>` via ProxyDB lookup (no security headers) |
| anything else | 404 Not Found |

`<domain>` is `test` unless a wildcard certificate in the cert dir sets a different suffix — see [Custom domain](#🏷️-custom-domain-from-a-wildcard-certificate).

### Packages

Uses Go standard library packages plus **one Go team-maintained sub-repo**:

- `golang.org/x/net/ipv4` — IP_PKTINFO control messages for DNS destination-IP discovery

Standard library packages: `net/http`, `net/http/httputil`, `crypto/tls`, `crypto/x509`, `crypto/rand`, `embed`, `log/slog`, `net`, `os`, `sync`, `time`, `io`, `mime/multipart`, `flag`.

## Development

### Prerequisites

- Go 1.26.3 or later
- `golang.org/x/net/ipv4` (fetched automatically by Go modules)

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
- **Self-signed cert** — generated with 10-year validity and wildcard SANs for `*.test`, `tmp.test`, `localhost`; a certificate you provide replaces it and is never rewritten by aru
- **Key permissions** — private key stored at `0600` (owner-only)
- **Security headers** — applied only to file upload routes (`tmp.<domain>`)
- **Reverse proxy** — no security headers on proxied responses (upstream controls its own headers)
- **No file-type validation** — all file types accepted by design

## Cleaning up

Files in the upload directory older than 1 hour are automatically removed. The cleanup runs every 5 minutes and also runs on startup.
