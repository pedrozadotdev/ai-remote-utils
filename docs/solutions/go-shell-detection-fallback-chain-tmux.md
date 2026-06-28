---
title: Detecting the user's default shell for tmux sessions in Go CLI tools
category: architecture
severity: high
tags:
  - go
  - shell-detection
  - tmux
  - fallback-chain
  - environment-detection
  - os-exec
  - test-isolation
  - exec-lookpath
applies_when:
  - Building Go CLI tools that spawn shell sessions or tmux windows
  - Need to detect and use the user's preferred shell ($SHELL) instead of hardcoding "bash"
  - Designing cached environment detection that must remain testable
  - Using tmux programmatically with `default-shell` option
  - Testing code paths that call `os.Exit()` via subprocess isolation
---

# Detecting the user's default shell for tmux sessions in Go CLI tools

## Problem

CLI tools that spawn tmux sessions often hardcode `"bash"` as the shell for command execution and interactive fallback. This breaks for users whose default shell is not bash (zsh, fish, nushell, etc.) — they get bash inside their tmux sessions instead of their configured `$SHELL`.

In the `aru` project, 6 call sites across `worktree.go` used hardcoded `"bash"`:

```go
// tmux session args (global scope)
"set-option", "-g", "default-command", "bash"

// tmux command templates
"exec bash"

// runSetupCommands / runTeardownCommands
exec.Command("bash", "-c", cmdStr)

// fallback interactive shell in handleWorktreeAdd / handleWorktreeOpen
exec.Command("bash")
```

## Solution

### Architecture overview

Separate the problem into two functions:

1. **`resolveShell()`** — pure detection logic with a fallback chain. Tested directly via table-driven tests with mock binaries in PATH.
2. **`detectShell()`** — cached wrapper with test-isolation support. Returns the cached result after first resolution.

```go
var (
    shellDetected bool
    cachedShell   string
)

func resolveShell() string {
    // Step 1: Try $SHELL
    if shell := os.Getenv("SHELL"); shell != "" {
        // Try the absolute path first
        if _, err := exec.LookPath(shell); err == nil {
            return filepath.Base(shell)
        }
        // Try the basename in PATH
        if base := filepath.Base(shell); base != "" {
            if _, err := exec.LookPath(base); err == nil {
                return base
            }
        }
    }

    // Step 2: Try bash
    if _, err := exec.LookPath("bash"); err == nil {
        return "bash"
    }

    // Step 3: Try sh
    if _, err := exec.LookPath("sh"); err == nil {
        return "sh"
    }

    // Step 4: Nothing found — hard error
    fmt.Fprintln(os.Stderr, "ERROR: no shell available (tried $SHELL, bash, sh)")
    os.Exit(1)
    return "" // unreachable
}

// detectShell returns the detected shell, cached after first resolution.
// Not safe for concurrent initialization.
//
// Uses bool+string instead of sync.Once so that resetDetectShell()
// (used in tests) can clear the cached value.
func detectShell() string {
    if !shellDetected {
        cachedShell = resolveShell()
        shellDetected = true
    }
    return cachedShell
}

// resetDetectShell resets the detectShell cache for test isolation.
// Defined as a package-level function (unexported) in the test file.
func resetDetectShell() {
    shellDetected = false
    cachedShell = ""
}
```

### Fallback chain rationale

| Step | Target | Why |
|------|--------|-----|
| 1a | `$SHELL` absolute path | User's explicit preference; works if $SHELL is a full path to an installed binary |
| 1b | `$SHELL` basename | Handles cases where $SHELL points to a path not in PATH (e.g., `/opt/homebrew/bin/fish`) but the basename is findable |
| 2 | `bash` | Universally installed on Linux/macOS; most common default |
| 3 | `sh` | POSIX minimum; always present on Unix systems |
| 4 | `os.Exit(1)` | Fatal error with clear message; no silent fallback to wrong shell |

### Tmux integration: default-shell over default-command

Use tmux's `default-shell` option (session-scoped, no `-g`) instead of `default-command`:

```go
// Before (wrong):
";", "set-option", "-g", "default-command", "bash",

// After (correct):
";", "set-option", "default-shell", detectShell(),
```

**Why `default-shell`?** `default-command` runs as the initial command in each new window/pane, while `default-shell` is the shell that interprets subsequent commands. Using `default-shell` is semantically correct — it tells tmux "use this shell for new windows" rather than "run this specific command first."

**Why no `-g`?** Without `-g`, the option is session-scoped rather than global. This avoids leaking the shell setting to other tmux sessions the user may have running independently.

### Fallback interactive shell: login shell `-l`

For fallback error paths where the tool spawns an interactive shell directly:

```go
// Before:
exec.Command("bash")

// After:
exec.Command(detectShell(), "-l")
```

The `-l` flag starts a login shell, which sources the user's profile files (`.profile`, `.zprofile`, `.bash_profile`, etc.). This ensures the fallback shell has the user's PATH and environment correctly configured.

## Why this works

1. **`exec.LookPath` validates existence** — it checks both executable permissions and PATH resolution, so we never pass an invalid shell to tmux or `exec.Command`.

2. **Basename-only resolution** — `filepath.Base(shell)` strips directory prefixes (e.g., `/usr/bin/zsh` → `zsh`). Tmux's `default-shell` accepts basenames (e.g., `set-option default-shell zsh`), making the output compatible.

3. **Login shell for fallback** — Starting an interactive shell without a command delegates to the shell's RC files, ensuring PATH and environment are set up correctly for the user.

4. **Separated pure logic from caching** — `resolveShell()` is a pure function (no side effects beyond `os.Exit` on hard error) that can be tested directly. `detectShell()` adds caching on top, but the detection logic is independently testable.

## Testing patterns

### 1. Mock shells for resolveShell table tests

Create lightweight shell scripts that just exit 0 (exec.LookPath only needs executability):

```go
func createMockShell(t *testing.T, dir, name string) {
    t.Helper()
    path := filepath.Join(dir, name)
    content := `#!/bin/sh
exit 0
`
    if err := os.WriteFile(path, []byte(content), 0755); err != nil {
        t.Fatal(err)
    }
}

func TestResolveShell(t *testing.T) {
    tests := []struct {
        name     string
        shellEnv string
        setup    func(t *testing.T, dir string)
        want     string
        wantExit bool
    }{
        {
            name:     "fromEnv",
            shellEnv: "/usr/bin/zsh",
            setup:    func(t *testing.T, dir string) { createMockShell(t, dir, "zsh") },
            want:     "zsh",
        },
        {
            name:     "fallbackToBash",
            shellEnv: "",
            setup:    func(t *testing.T, dir string) { createMockShell(t, dir, "bash") },
            want:     "bash",
        },
        // ...
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            if tt.wantExit { /* subprocess isolation — see below */ return }
            mockDir := t.TempDir()
            tt.setup(t, mockDir)
            t.Setenv("PATH", mockDir)
            t.Setenv("SHELL", tt.shellEnv)

            got := resolveShell()
            if got != tt.want {
                t.Errorf("resolveShell() = %q, want %q", got, tt.want)
            }
        })
    }
}
```

### 2. Subprocess isolation for os.Exit testing

Testing code paths that call `os.Exit(1)` cannot be done in-process — `os.Exit` terminates the entire test process. The pattern is to re-execute the test binary as a child process with a sentinel environment variable:

```go
if tt.wantExit {
    // First call: detect the sentinel and execute as child
    if os.Getenv("RESOLVE_SHELL_HARD_ERROR") == "1" {
        t.Setenv("SHELL", "")
        t.Setenv("PATH", t.TempDir()) // no shell binaries
        got := resolveShell()
        t.Fatalf("resolveShell() should have exited, got %q", got)
    }

    // Parent: run the test as a subprocess
    cmd := exec.Command(os.Args[0], "-test.run="+t.Name()+"$", "-test.v")
    cmd.Env = append(os.Environ(), "RESOLVE_SHELL_HARD_ERROR=1")
    cmd.Env = append(cmd.Env, "PATH="+t.TempDir())
    out, err := cmd.CombinedOutput()

    // Verify exit code 1
    if exitErr, ok := err.(*exec.ExitError); ok {
        if exitErr.ExitCode() != 1 {
            t.Errorf("expected exit code 1, got %d", exitErr.ExitCode())
        }
    }

    // Verify error message
    if !strings.Contains(string(out), "no shell available") {
        t.Errorf("expected error message about no shell, got: %s", string(out))
    }
    return
}
```

### 3. Recording mock shell for verifying which shell is used

To verify that `runSetupCommands` actually uses the detected shell, use a recording mock that writes to a log file:

```go
func TestRunSetupCommands_UsesDetectedShell(t *testing.T) {
    resetDetectShell()
    tmp := t.TempDir()
    mockDir := filepath.Join(tmp, "mockbin")
    os.MkdirAll(mockDir, 0755)

    recordingShell := filepath.Join(mockDir, "myshell")
    logFile := filepath.Join(tmp, "shell.log")
    shellContent := `#!/bin/sh
printf '%s' myshell >> ` + logFile + `
exec /bin/sh -c "$2"
`
    os.WriteFile(recordingShell, []byte(shellContent), 0755)

    t.Setenv("PATH", mockDir+":/bin:/usr/bin")
    t.Setenv("SHELL", filepath.Join(mockDir, "myshell"))

    runSetupCommands(tmp, []string{"touch marker"})

    data, _ := os.ReadFile(logFile)
    if string(data) != "myshell" {
        t.Errorf("expected 'myshell' to be invoked, got %q", string(data))
    }
}
```

### 4. Cache reset for test isolation

Every test that calls `detectShell()` or functions that use it must reset the package-level cache:

```go
func TestSomething(t *testing.T) {
    resetDetectShell()          // <-- required before each test
    t.Setenv("SHELL", "/bin/bash")
    // ... test body
}
```

`resetDetectShell()` is an unexported function defined in the test file:

```go
// resetDetectShell resets the detectShell cache for test isolation.
func resetDetectShell() {
    shellDetected = false
    cachedShell = ""
}
```

## Caching design: bool+string vs sync.Once

**The problem with `sync.Once`:** It makes cached state permanently immutable within a process. There is no `sync.Once.Reset()` method. In testing, each test needs a fresh resolution (different `$SHELL` values), but `sync.Once` would return the cached value from the first test forever.

**The bool+string solution:** A simple pair of package-level variables with a manual guard. The trade-off is that it's NOT safe for concurrent initialization (unlike `sync.Once`), but in practice:

- This initialization happens once, synchronously, at the first call site
- The codebase is single-threaded at this point in the lifecycle
- The comment warning "Not safe for concurrent initialization" prevents future misuse

**Important:** Document clearly *why* the non-standard approach was chosen, so future maintainers don't "fix" it back to `sync.Once` without considering test isolation:

```go
// Note: Uses bool+string instead of sync.Once so that resetDetectShell()
// (used in tests) can clear the cached value. If sync.Once were used, there
// would be no way to reset the cache for test isolation. Do not "fix" this
// back to sync.Once without providing a test-reset mechanism.
```

## What to watch out for

### All call sites must be updated

When replacing a hardcoded shell reference, search exhaustively. In the `aru` project, there were 6 call sites:

1. `buildTmuxCommand` — `"exec bash"` → `"exec " + detectShell()`
2. `buildSessionArgs` — `default-command` with bash → `default-shell` with detectShell()
3. `runSetupCommands` — `exec.Command("bash", "-c", ...)` → `exec.Command(detectShell(), "-c", ...)`
4. `runTeardownCommands` — same pattern
5. `handleWorktreeAdd` fallback — `exec.Command("bash")` → `exec.Command(detectShell(), "-l")`
6. `handleWorktreeOpen` fallback — same

### doc comments must be updated

After changing hardcoded references, update doc comments that mention `"bash"` explicitly:

```
// runSetupCommands runs each setup command in order with bash -c
→ // runSetupCommands runs each setup command in order with <detected shell> -c
```

### All existing tests need SHELL determinism

Every existing test that transitively calls `detectShell()` must set `SHELL` explicitly:

```go
t.Setenv("SHELL", "/bin/bash")
resetDetectShell()
```

Without this, the test result depends on the developer's own `$SHELL` environment variable — a non-reproducible test.

### PATH manipulation for exec.LookPath mocking

When testing `exec.LookPath`, create mock binary scripts in `t.TempDir()` and prepend to PATH:

```go
mockDir := t.TempDir()
os.WriteFile(filepath.Join(mockDir, "zsh"), []byte("#!/bin/sh\nexit 0"), 0755)
t.Setenv("PATH", mockDir)  // no fallback to real shells
t.Setenv("SHELL", "/usr/bin/zsh")
```

### Prefer t.Setenv over os.Setenv

`t.Setenv()` (Go 1.17+) automatically restores the original environment variable after the test. It also panics if called in parallel subtests, preventing data races.

## Prevention

1. **Never hardcode shell names** in tools that spawn interactive sessions or run user commands. Always detect via `$SHELL` with a fallback chain.

2. **Design for testability from the start** — separate pure detection logic from caching. Use `resolveShell()` (pure, directly testable) + `detectShell()` (caching wrapper).

3. **Choose caching mechanism based on testability needs** — `sync.Once` is great for production but impossible to reset in tests. Bool+string with a `resetXxx()` function is testable. Document the trade-off explicitly.

4. **Add `SHELL` determinism to all existing tests** when introducing shell detection to a codebase — every test that touches the affected code must set `$SHELL` explicitly.

5. **Search exhaustively** — use `rg '"bash"'` or `grep -rn '"bash"'` to find all hardcoded references before starting.

6. **For tmux specifically**, prefer `default-shell` (session-scoped, no `-g`) over `default-command`. Use `-l` (login shell) for directly spawned fallback shells.
