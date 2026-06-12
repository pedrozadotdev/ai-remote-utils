# AGENTS.md — AI Agent Rules for tmp-file

This file documents conventions, constraints, and skill mappings for AI agents working on this project.

## Intent → Skill Mapping

| Intent | Skill / Action |
|--------|---------------|
| Add or modify a Go feature | Edit `.go` files in package `main`. Follow table-driven test pattern. |
| Change upload behavior | `upload.go` — `UploadHandler.ServeHTTP` + `upload_test.go` |
| Change cleanup behavior | `cleanup.go` — `cleanupOnce` + `StartCleanup` ; `cleanup_test.go` |
| Change TLS/cert behavior | `cert.go` — cert generation, expiry, persistence; `cert_test.go` |
| Add/change routes or middleware | `server.go` — `NewServer` mux setup + `server_test.go` |
| Update frontend UI | `static/index.html` — single embedded HTML file |
| Add embedded static asset | `static.go` — embed via `//go:embed static` directive |
| Document a solved problem | `~/.pi/agent/docs/solutions/<category>/` (global solutions) |
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

### Testing conventions
- All test files: `*_test.go` in same package
- Coverage target: 80%+ of non-`main()` functions (main() excluded by convention)
- Use `t.TempDir()` for filesystem tests — automatic cleanup
- Context cancellation tests required for goroutine lifecycle
- Race-free tests: `go test -race` must pass

### Security rules
- **Private key permissions**: `os.WriteFile(keyPath, keyPEM, 0600)` — never 0644
- **TLS MinVersion**: `tls.VersionTLS12` minimum
- **Security headers**: `securityHeaders()` middleware on all routes
- **Upload size limit**: `http.MaxBytesReader` per request, configurable
- **Multi-file upload**: iterate `r.MultipartForm.File["files"]`, not `r.FormFile()`
- **Startup checks**: writability check once at startup, not per-request (avoids TOCTOU)

### Clean naming conventions
- 4-char random alphanumeric filenames (a-z, 0-9)
- Extension preserved from original filename, fallback `.bin`
- Mutex-protected name generation with collision retry (max 10)

### Review triggers
When these code patterns appear in a diff, flag for review:
- `os.WriteFile` with `0644` or `0666` on cert/key paths
- `r.FormFile("files")` when multi-file support is expected
- Startup checks repeated inside per-request handlers
- Nested guard conditions where outer condition implies inner

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

Global solutions at `~/.pi/agent/docs/solutions/`:
- `architecture/go-tls-key-permissions.md` — private key 0600 rule
- `architecture/go-http-security-headers.md` — security headers middleware
- `architecture/go-toctou-startup-check.md` — avoid per-request startup checks
- `integration/go-multipart-multiple-files.md` — multi-file upload pattern
- `testing/go-coverage-exclude-main.md` — main() coverage exclusion
- `workflow/nested-guard-dead-code.md` — detecting nested guard dead code

## Build

```bash
go build -o tmp-file .  # Produces ~9.4MB static binary
```
