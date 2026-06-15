---
title: Go port state persistence — resource-scoped JSON state with lifecycle management
category: architecture
severity: medium
tags:
  - go
  - json
  - persistence
  - state
  - lifecycle
  - resource-management
  - ports
  - tmux
  - worktree
applies_when:
  - Building a CLI tool that allocates dynamic resources (ports, temp directories) that must persist across restarts
  - Need to reload resource state on a subsequent command (e.g., re-attach, open, resume)
  - Need to clean up state on resource deletion
  - Want a graceful fallback when persisted state is missing (state directory wipe, backup restore)
  - Managing per-resource state files (one per resource, not a shared store)
---

# Go port state persistence — resource-scoped JSON state with lifecycle management

## Problem

CLI tools often need to allocate dynamic resources (TCP ports, temp directories, Unix sockets) at resource-creation time. When the user later performs a "re-attach" or "re-open" operation on that same resource, the originally allocated values must be reused — re-allocating would break dependent services (proxies, listeners, config files).

State must survive process restarts. Simply keeping values in memory is insufficient — the CLI is invoked fresh on each command. The state must be persisted to disk.

Furthermore, when the resource is deleted, the persisted state must be cleaned up. And if the state file is missing (e.g., a user wiped the state directory), the system should gracefully fall back to a fresh allocation rather than erroring out.

## Context

In `ai-remote-utils`, the `aru worktree add` command allocates TCP ports from the 1024-9999 range. These ports are passed as environment variables to the worktree's setup commands and registered with the embedded reverse proxy. On `aru worktree open` — which re-attaches to an existing worktree after a machine restart — the same ports must be used. If ports were re-allocated on open:

1. The proxy would point to different ports than the worktree's processes
2. Environment variables in tmux windows would reference stale ports
3. Ports allocated by `add` would leak (never released back to the pool)

The existing `ProxyDB` pattern (`go-json-persistent-store-proxydb-pattern.md`) solves *shared, thread-safe* JSON persistence. This problem is different: state is *per-resource* (one file per worktree), only ever accessed by a single process, but needs *lifecycle management* (create on allocation, read on re-attach, delete on removal, fallback on missing).

## Solution

Persist resource-scoped state as individual JSON files in a structured state directory. Each resource instance gets its own file, written at allocation time, read at re-attach time, and removed at cleanup time.

### State directory structure

```
~/.aru/state/
  <project>/
    <branch>/
      ports.json      # Allocated port numbers
      setup-complete  # Marker file (not JSON — empty file = completed)
```

### JSON schema

Port allocations are stored using a wrapper struct with string-keyed maps for human-readable JSON that is stable across Go map iteration order:

```go
// portsStateFile is the JSON schema for the persisted port assignments.
// Keys are placeholder numbers as strings (e.g., "1", "2") so the file
// is human-readable and stable across Go map iteration order.
type portsStateFile struct {
    Ports map[string]int `json:"ports"`
}
```

This produces JSON like:

```json
{
  "ports": {
    "1": 12345,
    "2": 12346
  }
}
```

### Core functions

#### Path builders

```go
// baseDir returns the aru home directory (~/.aru/).
func baseDir() string {
    home, err := os.UserHomeDir()
    if err != nil {
        return ".aru"
    }
    return filepath.Join(home, ".aru")
}

// State directory path for a worktree resource
func stateDir(project, branch string) string {
    return filepath.Join(baseDir(), "state", project, branch)
}

// Path to the ports state file for a worktree
func portsStatePath(project, branch string) string {
    return filepath.Join(stateDir(project, branch), "ports.json")
}
```

#### Allocation + persist (resource creation)

```go
// Persist port allocations to disk.
// Returns nil (no-op) if the ports map is empty.
func persistAllocatedPorts(project, branch string, ports map[int]int) error {
    if len(ports) == 0 {
        return nil
    }
    path := portsStatePath(project, branch)
    if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
        return fmt.Errorf("worktree: failed to create state directory: %w", err)
    }

    // Convert to string-keyed map for human-readable JSON
    state := portsStateFile{Ports: make(map[string]int, len(ports))}
    for num, port := range ports {
        state.Ports[strconv.Itoa(num)] = port
    }

    data, err := json.MarshalIndent(state, "", "  ")
    if err != nil {
        return fmt.Errorf("worktree: failed to marshal ports state: %w", err)
    }
    // 0600: least privilege — port allocations are not secrets but
    // shouldn't be world-readable
    if err := os.WriteFile(path, data, 0600); err != nil {
        return fmt.Errorf("worktree: failed to write ports state file: %w", err)
    }
    return nil
}
```

#### Load + reload (resource re-attach)

```go
// Load persisted port allocations.
// Returns (nil, nil) if the file does not exist (graceful fallback).
// Returns (nil, error) if the file exists but cannot be parsed.
func loadAllocatedPorts(project, branch string) (map[int]int, error) {
    path := portsStatePath(project, branch)
    data, err := os.ReadFile(path)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, nil // graceful fallback: no state yet
        }
        return nil, fmt.Errorf("worktree: failed to read ports state: %w", err)
    }

    var state portsStateFile
    if err := json.Unmarshal(data, &state); err != nil {
        return nil, fmt.Errorf("worktree: failed to parse ports state: %w", err)
    }

    ports := make(map[int]int, len(state.Ports))
    for numStr, port := range state.Ports {
        num, err := strconv.Atoi(numStr)
        if err != nil {
            slog.Warn("skipping invalid port placeholder in state file", "key", numStr, "error", err)
            continue
        }
        ports[num] = port
    }
    return ports, nil
}
```

#### Remove (resource deletion)

```go
// Remove persisted port state when the resource is deleted.
// Best-effort: silently ignores "not found" errors.
func removeAllocatedPorts(project, branch string) {
    if err := os.Remove(portsStatePath(project, branch)); err != nil && !os.IsNotExist(err) {
        slog.Debug("failed to remove ports state file", "error", err)
    }
}
```

### Lifecycle integration

```go
// On resource creation: allocate → persist
func handleWorktreeAdd(branch string) {
    // ... setup worktree, resolve ports from placeholders ...
    if err := persistAllocatedPorts(project, branch, resolvedPorts); err != nil {
        slog.Warn("failed to persist port allocations", "error", err)
    }
}

// On resource re-attach: load → fallback → persist
func handleWorktreeOpen(branch string) {
    ports, err := loadAllocatedPorts(project, branch)
    if err != nil {
        // Parse error — warn and force re-allocation
        slog.Warn("failed to load ports, will re-allocate", "error", err)
    }
    if len(ports) == 0 {
        // Fallback: state file missing or empty (e.g., state dir wiped)
        ports = allocatePorts(...)
        persistAllocatedPorts(project, branch, ports) // best-effort
    }
}

// On resource deletion: remove state
func handleWorktreeDel(branch string) {
    // ... tear down worktree ...
    removeAllocatedPorts(project, branch) // best-effort, void
}
```

## Why this works

### Lifecycle symmetry

Each phase of the resource lifecycle has a corresponding state operation:

| Phase | State operation | Why |
|-------|----------------|-----|
| Create (add) | Allocate + persist | First use — ports must be chosen and stored |
| Re-attach (open) | Load from disk | Subsequent uses — must reuse same ports |
| Delete (del) | Remove from disk | Cleanup — no dangling state files |
| Missing state | Fallback + persist | Resilience — survive state dir wipe |

### Graceful degradation

When the state file is missing (fresh install, wiped state dir, upgrade), the system re-allocates and persists. The user doesn't see an error — they just get fresh ports, which is correct for a state-less worktree.

### Least privilege

State files are written with `0600` permissions. Port allocations aren't secrets, but they're internal state that shouldn't be world-readable.

## Prevention / Review triggers

When reviewing code that manages resource-scoped state:

1. **Missing cleanup on delete** — If state is written during creation but never removed during deletion, the state directory fills with orphan files. Flag `handleWorktreeDel` or equivalent for missing state cleanup.

2. **Re-allocation instead of reload** — If a "re-attach" or "open" command allocates fresh resources instead of loading from state, existing references (proxies, config files) become stale. This was finding F1 in the review.

3. **Fallback missing** — If the load function returns an error (instead of `nil, nil`) when the state file doesn't exist, a state directory wipe becomes a hard failure for the user. Load functions should return a sentinel or `nil` to distinguish "file missing" from "file corrupt."

4. **State file permissions** — State files are internal; use `0600` unless there's a specific reason to share. Never `0644` for state that affects process behavior.

5. **Lifecycle asymmetry** — For every write (persist, save, create), there should be a corresponding delete (remove, clean) at the opposite end of the lifecycle. Missing cleanup leaves stale state; missing write means state-dependent operations fail silently.

## See also

- `go-json-persistent-store-proxydb-pattern.md` — Thread-safe shared JSON persistence with write-through, hot-reload, and `sync.RWMutex`. Use this when multiple goroutines access the same state file. The port state pattern is for single-writer per-resource state.
- `go-struct-walking-placeholder-resolution.md` — Struct-walking placeholder resolution used alongside port state persistence (ports are injected via `<PORT1>` placeholders in aru.json config).
- `go-mock-external-commands-testing.md` — Mock external commands in Go tests using PATH manipulation; used to test the lifecycle handlers without real tmux.
- `~/.pi/agent/docs/solutions/testing/go-recording-mock-binary-testing.md` — Recording mock binary that logs invocations to a JSONL file for test assertions.
