# AGENTS.md — AI Agent Rules for aru

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
| Change aru.json config parsing | `aruconfig.go` — `ParseAruConfig`, `collectPortPlaceholders`, `resolvePlaceholders`, `cloneConfig`, `flattenConfig`; `aruconfig_test.go` |
| Change worktree behavior | `worktree.go` — `handleWorktreeAdd`, `handleWorktreeDel`, `handleWorktreeOpen`, `handleWorktreeList`, `readConfig`, `runSetupIfNeeded`, `setupProxy`/`removeProxy`; `worktree_test.go` |
| Add worktree subcommand | `main.go` — add `"worktree"` case to subcommand switch alongside `"proxy"` |
| Change HTTP redirect | `redirect.go` — `StartRedirect`; `redirect_test.go` |
| Add/change routes or middleware | `server.go` — `NewServer` virtual host mux; `server_test.go` |
| Change main.go wiring | `main.go` — flag parsing, listener orchestration, signal handling |
| Update frontend UI | `static/index.html` — single embedded HTML file |
| Add embedded static asset | `static.go` — embed via `//go:embed static` directive |
| Change systemd service | `aru.service` — unit file |
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
- **aru.json trust model**: commands in `aru.json` (`worktree.setup`, `worktree.teardown`, `tmux.<window>.command`, `tmux.<window>.env`) run verbatim as the user — equivalent to `Makefile` recipes, `package.json` scripts, or `Dockerfile` `RUN` instructions. Env values ARE shell-escaped (`strconv.Quote`); the `command` field is intentionally NOT escaped. Review `aru.json` changes with the same care as shell scripts. There is no in-process sandbox.
- **aru.json `setup_oneshot`**: when `true`, setup commands run only once per worktree session. Marker at `~/.aru/state/<project>/<branch>/setup-complete` tracks completion. Use this for non-idempotent setup (DB seeding, schema migrations, one-time init). Marker is removed on `aru worktree del`. To force re-run, delete the marker.

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
- Config-driven lifecycle via `aru.json` in the worktree root (`worktree.setup`, `worktree.teardown`, `tmux.<name>`, `proxy` sections)
- Placeholder resolution uses **struct-walking** (not marshal-replace-unmarshal): `cloneConfig` → `applyPlaceholders` → `replaceInString`. See `docs/solutions/go-struct-walking-placeholder-resolution.md`.
- Port persistence at `~/.aru/state/<project>/<branch>/ports.json` (survives reboots, allocated at `add`, reloaded on `open`)
- Setup idempotency via `setup_oneshot` config field; marker at `~/.aru/state/<project>/<branch>/setup-complete`
- `setupTmuxSession` returns errors instead of calling `os.Exit` — callers handle fallback
- Port discovery scans 1024-9999 via `net.Listen` (TOCTOU accepted — port only used for env var)
- Missing tmux = hard error; missing git = hard error
- `tryCreatePiWindow` now returns `bool` (caller decides whether to select the pi window); in config-driven sessions, pi is NOT selected (user's last config window is preserved)

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
- aru.json commands silently shell-escaped — only `env` values are escaped; `command` is run verbatim per the trust model
- New string fields added to `WorktreeConfig` or `TmuxWindow` without updating the struct walker (`applyPlaceholders`, `collectPortNumbers`, `cloneConfig`) — all three must stay in sync
- `cloneConfig` modified without adding the new field to the copy — a missing field means `SetupOneshot` (or any future field) gets silently dropped during resolution
- Port state persistence skipped (`removeAllocatedPorts` not called in `handleWorktreeDel`) — would leak state files
- Setup-complete marker not removed on worktree deletion (`clearSetupComplete` not called alongside `removeAllocatedPorts`)

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
- `go-struct-walking-placeholder-resolution.md` — Go struct-walking placeholder resolution (replaces marshal-replace-unmarshal to avoid JSON injection)

Global solutions at `~/.pi/agent/docs/solutions/`:
- `architecture/go-tls-key-permissions.md` — private key 0600 rule
- `architecture/go-http-security-headers.md` — security headers middleware
- `architecture/go-toctou-startup-check.md` — avoid per-request startup checks
- `integration/go-multipart-multiple-files.md` — multi-file upload pattern
- `testing/go-coverage-exclude-main.md` — main() coverage exclusion
- `testing/go-recording-mock-binary-testing.md` — Recording mock binary for verifying CLI command arguments in Go tests
- `workflow/nested-guard-dead-code.md` — detecting nested guard dead code

## Build

```bash
go build -o aru .  # Produces ~10MB static binary
```
