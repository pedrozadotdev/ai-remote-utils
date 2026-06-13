---
title: Mocking external commands in Go tests using PATH manipulation
category: testing
severity: high
tags:
  - go
  - testing
  - os-exec
  - mock
  - external-commands
  - integration-testing
applies_when:
  - Testing Go code that uses os/exec to call external binaries
  - Need to control behavior of external commands in tests without installing them
  - Want to test error paths for missing or failing external commands
  - Testing code that calls tmux, git, or other CLI tools
---

# Mocking external commands in Go tests using PATH manipulation

## Problem

Go code that uses `os/exec` to call external binaries (git, tmux, docker, etc.) is hard to unit test. The commands may not be installed in CI, may have side effects, or may require specific environments that are impractical to set up in a test.

Simply skipping tests when the command isn't available leaves coverage gaps.

## Solution: mock binary in PATH

Create a lightweight shell script that simulates the external command, write it to a temp directory, and prepend that directory to `PATH`.

### Pattern

```go
// createMockTmux writes a mock tmux script that simulates enough behavior
// for testing: has-session, new-session, kill-session, attach-session.
func createMockTmux(t *testing.T, dir string) {
    t.Helper()
    tmuxPath := filepath.Join(dir, "tmux")
    content := `#!/bin/sh
# Mock tmux for testing
sock=""
has_session=0
new_session=0
kill_session=0

for arg in "$@"; do
    case "$arg" in
        -S) mode="sock" ;;
        has-session) has_session=1 ;;
        new-session) new_session=1 ;;
        kill-session) kill_session=1 ;;
        *)
            if [ "$mode" = "sock" ] && [ -n "$arg" ]; then
                sock="$arg"
                mode=""
            fi
            ;;
    esac
done

if [ -z "$sock" ]; then
    exit 0
fi

if [ "$has_session" = "1" ]; then
    [ -f "$sock" ] && exit 0 || exit 1
fi

if [ "$new_session" = "1" ]; then
    mkdir -p "$(dirname "$sock")"
    touch "$sock"
    exit 0
fi

if [ "$kill_session" = "1" ]; then
    rm -f "$sock"
    exit 0
fi

exit 0
`
    if err := os.WriteFile(tmuxPath, []byte(content), 0755); err != nil {
        t.Fatal(err)
    }
}

// Usage in test:
func TestSomethingWithTmux(t *testing.T) {
    mockDir := t.TempDir()
    createMockTmux(t, mockDir)

    // Prepend mock directory to PATH
    origPath := os.Getenv("PATH")
    os.Setenv("PATH", mockDir+":"+origPath)
    defer os.Setenv("PATH", origPath)

    // Now code that calls exec.Command("tmux", ...) will use the mock
    // instead of the real tmux binary
}
```

### Key principles

1. **Parse the `-S` flag** to extract the socket path — tmux uses `-S <socket>` to specify a custom control socket
2. **Simulate `has-session`** — return 0 if the socket file exists, 1 otherwise
3. **Simulate `new-session`** — create the socket directory and touch the socket file
4. **Simulate `kill-session`** — remove the socket file
5. **Default to exit 0** — commands like `set-option`, `new-window`, `select-window`, and `attach-session` don't need real behavior for test purposes

## What to watch out for

### `os.Exit` in goroutines kills the test process

If the production code calls `os.Exit()` inside a goroutine (e.g., in error handlers called from `exec.Command`), the entire test process terminates immediately. `recover()` does NOT catch `os.Exit`.

**Fix:** Refactor functions to return errors instead of calling `os.Exit`. Have the top-level CLI entry point call `os.Exit` after checking the error.

### Waiting for socket readiness

Don't use `os.Stat` to check if a socket is ready — a stale socket file from a dead process returns success. Instead, poll with the actual command:

```go
// BAD: checks file existence
func waitForSocketBad(path string) {
    for {
        if _, err := os.Stat(path); err == nil { return }
        time.Sleep(100 * time.Millisecond)
    }
}

// GOOD: checks actual readiness via the command
func waitForSocketGood(sock, sessionName string) {
    for {
        cmd := exec.Command("tmux", "-S", sock, "has-session", "-t", sessionName)
        if cmd.Run() == nil { return }
        time.Sleep(100 * time.Millisecond)
    }
}
```

### t.Setenv vs os.Setenv

Prefer `t.Setenv()` (Go 1.17+) over `os.Setenv()` + `defer os.Setenv()` — it automatically restores the original value after the test.

### Testing git commands

For testing git operations, create a temporary repository:

```go
func setupTestRepo(t *testing.T, dir string) string {
    t.Helper()

    run := func(name string, args ...string) {
        cmd := exec.Command(name, args...)
        cmd.Dir = dir
        if out, err := cmd.CombinedOutput(); err != nil {
            t.Fatalf("%s %v failed: %v\n%s", name, args, err, string(out))
        }
    }

    run("git", "init")
    run("git", "config", "user.email", "test@test.com")
    run("git", "config", "user.name", "Test")
    os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test"), 0644)
    run("git", "add", "README.md")
    run("git", "commit", "-m", "initial")

    // Return the current branch name
    out, _ := exec.Command("git", "branch", "--show-current").CombinedOutput()
    return strings.TrimSpace(string(out))
}
```

### runInDir helper

For testing functions that operate on the current working directory:

```go
func runInDir(t *testing.T, dir string, fn func()) {
    t.Helper()
    origDir, err := os.Getwd()
    if err != nil { t.Fatal(err) }
    if err := os.Chdir(dir); err != nil { t.Fatal(err) }
    defer os.Chdir(origDir)
    fn()
}
```

## Real-world example

From the `ai-remote-utils` project (`worktree_test.go`):

```go
func TestHandleWorktreeAdd(t *testing.T) {
    dir := t.TempDir()
    setupTestRepo(t, dir)
    createTestBranch(t, dir, "add-test-branch")

    // Create mock tmux
    mockDir := t.TempDir()
    createMockTmux(t, mockDir)
    t.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))

    runInDir(t, dir, func() {
        // Call the function that internally uses exec.Command("tmux", ...)
        errCh := make(chan error, 1)
        go func() {
            defer func() {
                if r := recover(); r != nil {
                    errCh <- fmt.Errorf("panic: %v", r)
                }
            }()
            handleSomeTmuxOperation()
            errCh <- nil
        }()

        select {
        case err := <-errCh:
            if err != nil { t.Fatal(err) }
        case <-time.After(10 * time.Second):
            t.Fatal("timed out")
        }
    })
}
```

## See also

- `go-json-persistent-store-proxydb-pattern.md` — Thread-safe JSON persistence (uses similar test patterns)
- `go-recording-mock-binary-testing.md` — Recording mock binary that logs invocations to a JSONL file for argument verification
- `go-struct-walking-placeholder-resolution.md` — Struct-walking placeholder resolution (alternative to marshal-replace-unmarshal)
