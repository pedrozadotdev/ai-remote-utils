# AGENTS.md — AI Agent Rules for ai-remote-utils

This file documents conventions, constraints, and skill mappings for AI agents working on this project.

## Intent → Skill Mapping

| Intent | Skill / Action |
|--------|---------------|
| Add or modify a Go feature | Edit `.go` files in package `main`. Follow table-driven test pattern. |
| Change upload behavior | `upload.go` — `UploadHandler.ServeHTTP` + `upload_test.go` |
| Change cleanup behavior | `cleanup.go` — `cleanupOnce` + `StartCleanup` ; `cleanup_test.go` |
| Change TLS/cert behavior | `cert.go` — cert generation, SAN detection, expiry, persistence; `cert_test.go` |
| Change DNS behavior | `dns.go` — `StartDNS`, wire protocol, interface matching; `dns_test.go` |
| Change reverse proxy | `proxy.go` — `LookupProxy`, `NewReverseProxy`; `proxy_test.go` |
| Change proxy persistence | `proxydb.go` — ProxyDB (Load/Save/Add/Delete/Get/List/Refresh); `proxydb_test.go` |
| Change proxy management CLI | `main.go` — `handleProxySubcommand`, `handleProxyAdd`, `handleProxyDel`, `handleProxyList` |
| Change worktree behavior | `worktree.go` — `handleWorktreeAdd`, `handleWorktreeDel`, `handleWorktreeOpen`, `handleWorktreeList`; `worktree_test.go` |
| Add worktree subcommand | `main.go` — add `"worktree"` case to subcommand switch alongside `"proxy"` |
| Change HTTP redirect | `redirect.go` — `StartRedirect`; `redirect_test.go` |
| Add/change routes or middleware | `server.go` — `NewServer` virtual host mux; `server_test.go` |
| Change main.go wiring | `main.go` — flag parsing, listener orchestration, signal handling |
| Update frontend UI | `static/index.html` — single embedded HTML file |
| Add embedded static asset | `static.go` — embed via `//go:embed static` directive |
| Change systemd service | `ai-remote-utils.service` — unit file |
| Document a solved problem | `~/.pi/agent/docs/solutions/<category>/` (global solutions) |
| Mock external commands in tests | `docs/solutions/go-mock-external-commands-testing.md` — PATH manipulation with mock binaries, temp git repos |
| Review code changes | `docs/reviews/<date>-<topic>.md` using `04-review` skill |

## Core Rules & Conventions

### Go code style
- **No external dependencies** — use only Go standard library packages
- **`log/slog`** for all structured logging with contextual fields
- **Error wrapping** — `fmt.Errorf("context: %w", err)` consistently
- **Table-driven tests** — all test files use table-driven patterns with `t.Run`
- **Race detection** — `go test -race ./...` must pass on every change

### Package structure
- Single `package main` — all source files are in one package
- No init() functions unless absolutely necessary
- Embedded static files via `//go:embed static/index.html`
- Worktree operations use `os/exec` for git and tmux, `syscall.Mount` for tmpfs

### Testing conventions
- All test files: `*_test.go` in same package
- Coverage target: 80%+ of non-`main()` functions (main() excluded by convention)
- Use `t.TempDir()` for filesystem tests — automatic cleanup
- Context cancellation tests required for goroutine lifecycle
- Race-free tests: `go test -race` must pass

### Security rules
- **Private key permissions**: `os.WriteFile(keyPath, keyPEM, 0600)` — never 0644
- **TLS MinVersion**: `tls.VersionTLS12` minimum
- **Security headers**: `securityHeaders()` middleware on upload routes only, not proxy routes
- **Upload size limit**: `http.MaxBytesReader` per request, configurable
- **Multi-file upload**: iterate `r.MultipartForm.File["files"]`, not `r.FormFile()`
- **Startup checks**: writability check once at startup, not per-request (avoids TOCTOU)
- **DNS**: only respond to `.test` queries; non-.test gets REFUSED (allows client fallback to secondary DNS; still prevents open resolver since no data is returned for arbitrary domains)

### Clean naming conventions
- 4-char random alphanumeric filenames (a-z, 0-9)
- Extension preserved from original filename, fallback `.bin`
- Mutex-protected name generation with collision retry (max 10)

### Worktree conventions
- Worktree operations require a git repository (checked via `git rev-parse --git-dir`)
- Must be run from the main (non-linked) worktree root — checked via `git rev-parse --git-common-dir` vs `--git-dir`
- Worktrees stored at `~/.aru/wt/<project>/<branch>`
- RAM-backed data at `~/.aru/ram/<project>/<branch>` with tmpfs via `syscall.Mount` (falls back to regular dir if mount fails)
- Tmux sessions use custom sockets at `~/.aru/sockets/<project>-<branch>.sock`
- Lifecycle scripts: `wt-setup.sh` (runs in tmux setup window with PORT env) and `wt-destroy.sh` (runs on worktree deletion)
- `setupTmuxSession` returns errors instead of calling `os.Exit` — callers handle fallback
- Port discovery scans 1024-9999 via `net.Listen` (TOCTOU accepted — port only used for env var)
- Missing tmux = hard error; missing git = hard error

### Reverse proxy conventions
- `LookupProxy` extracts subdomain from `<name>.test` hostnames, looks up name in ProxyDB
- Case-insensitive matching
- Strips `:443` port suffix before matching
- Blocked ports: 53, 80, 443 (prevents loops)
- Host header preserved as `*.test` (not rewritten to localhost)
- `URL.Scheme` → `http`, `URL.Host` → `localhost:<port>` for TCP routing
- Default transport enables WebSocket upgrades
- Unknown names return 404 (not a proxy entry), not 502 (upstream unreachable)

### Review triggers
When these code patterns appear in a diff, flag for review:
- `os.WriteFile` with `0644` or `0666` on cert/key paths
- `r.FormFile("files")` when multi-file support is expected
- Startup checks repeated inside per-request handlers
- Nested guard conditions where outer condition implies inner
- Proxy Director modifying `r.Host` (should preserve original)
- DNS responses for non-`.test` domains that return data (ensure REFUSED is not accidentally changed to success or NXDOMAIN)
- `blockedProxyPorts` bypass via JSON file edit (LoadProxyDB and Refresh must validate ports)
- ProxyDB name validation bypass — `LookupProxy` must block reserved names (`tmp`, `test`) alongside `validateName`
- `os.Exit()` inside functions called from goroutines (cannot be recovered)
- `waitForSocket` using `os.Stat` instead of actual readiness check (`tmux has-session`)
- `runSetupScript` called from both foreground and tmux window (double execution)
- `setupTmuxSession` calling `os.Exit` instead of returning errors

## Pipeline Workflow

When developing this project, sequence through:
1. `01-brainstorm` — clarify requirements
2. `02-plan` — plan implementation units
3. `03-work` — implement with TDD (RED → GREEN → REFACTOR)
4. `04-review` — code review against plan
5. `04-5-debug` — debug any issues found
6. `05-learn` — document learnings as solution artifacts
7. `06-docsync` — sync README.md and AGENTS.md with current state

## Relevant Solution Artifacts

Project-specific solutions at `docs/solutions/`:
- `go-json-persistent-store-proxydb-pattern.md` — Go JSON-backed persistent store with write-through, mtime hot-reload, thread safety
- `go-mock-external-commands-testing.md` — Mocking external commands in Go tests using PATH manipulation

Global solutions at `~/.pi/agent/docs/solutions/`:
- `architecture/go-tls-key-permissions.md` — private key 0600 rule
- `architecture/go-http-security-headers.md` — security headers middleware
- `architecture/go-toctou-startup-check.md` — avoid per-request startup checks
- `integration/go-multipart-multiple-files.md` — multi-file upload pattern
- `testing/go-coverage-exclude-main.md` — main() coverage exclusion
- `workflow/nested-guard-dead-code.md` — detecting nested guard dead code

## Build

```bash
go build -o ai-remote-utils .  # Produces ~10MB static binary
```
