---
title: Go JSON-backed persistent store with thread safety (ProxyDB pattern)
category: architecture
severity: medium
tags:
  - go
  - json
  - persistence
  - thread-safety
  - sync-rwmutex
  - write-through
  - mtime-hot-reload
  - stdlib
applies_when:
  - Building a persistent key-value store using only Go standard library
  - Need thread-safe read/write access to a JSON file from multiple goroutines
  - Want hot-reload without restarting the process
  - Want to prevent partial state on write failures
---

# Problem

When building CLI tools or lightweight servers in Go, you often need to persist simple configuration or state without pulling in a database. A naive approach using `os.ReadFile`/`json.Unmarshal` for each read leads to excessive I/O, while `os.WriteFile` without coordination can produce partial or corrupted data on failure.

# Context

In ai-remote-utils, we replaced an ephemeral port-based reverse proxy with named proxy entries stored in a JSON file (`~/.ai-remote-utils/proxies.json`). The proxy DB needed to be:

- Read concurrently by the HTTP server (many goroutines)
- Written by CLI subcommands (`proxy add/del`)
- Hot-reloadable if the file is edited externally (or by the CLI while the server runs)
- Safe against partial writes (disk full, permission errors)

# Solution

Use an in-memory `map[string]int` protected by `sync.RWMutex`, backed by a JSON file. Load at startup, write-through on mutation, and check file mtime on each read for hot-reload.

## Key patterns

### 1. Thread-safe in-memory map

```go
type ProxyDB struct {
    mu      sync.RWMutex
    path    string
    proxies map[string]int
    mtime   time.Time
}
```

- `sync.RWMutex` allows concurrent reads (many HTTP handlers) but exclusive writes
- `Get()` and `List()` use `RLock`; mutations use `Lock`

### 2. Write-through save (prevent partial state)

Save to disk BEFORE updating the in-memory map. If the save fails, the map is never updated.

```go
func (db *ProxyDB) Add(name string, port int) error {
    // ... validation ...

    db.mu.Lock()
    defer db.mu.Unlock()

    // Build new map state
    newProxies := make(map[string]int, len(db.proxies)+1)
    for k, v := range db.proxies {
        newProxies[k] = v
    }
    newProxies[name] = port

    // Write-through: save to disk FIRST
    if err := db.saveLocked(newProxies); err != nil {
        return err  // map NOT updated
    }

    // Only update memory after disk write succeeds
    db.proxies = newProxies
    return nil
}
```

### 3. mtime-based hot-reload

Check file modification time before each read. If the file changed externally, reload from disk.

```go
func (db *ProxyDB) Refresh() bool {
    db.mu.Lock()
    defer db.mu.Unlock()

    fi, err := os.Stat(db.path)
    if err != nil {
        return false
    }
    if !fi.ModTime().After(db.mtime) {
        return false  // no change
    }

    data, err := os.ReadFile(db.path)
    if err != nil {
        slog.Warn("proxy DB: failed to read file on refresh", "error", err)
        return false
    }

    var pf proxyFile
    if err := json.Unmarshal(data, &pf); err != nil {
        slog.Warn("proxy DB: failed to parse JSON on refresh", "error", err)
        return false
    }

    db.proxies = pf.Proxies
    db.mtime = fi.ModTime()
    return true
}
```

The server calls `Refresh()` on each proxy request before looking up an entry.

### 4. Ensure parent directory on save

CLI subcommands may run when the parent directory doesn't exist yet.

```go
func (db *ProxyDB) saveLocked(proxies map[string]int) error {
    // ... marshal to JSON ...

    if err := os.MkdirAll(filepath.Dir(db.path), 0755); err != nil {
        return fmt.Errorf("failed to create proxy DB directory: %w", err)
    }
    if err := os.WriteFile(db.path, data, 0644); err != nil {
        return fmt.Errorf("failed to write proxy DB: %w", err)
    }

    // Update mtime after successful write
    fi, err := os.Stat(db.path)
    if err == nil {
        db.mtime = fi.ModTime()
    }
    return nil
}
```

### 5. Validate entries on load, not just on add

External edits to the JSON file can bypass `Add()` validation. Always validate when loading:

```go
func LoadProxyDB(path string) (*ProxyDB, error) {
    // ... read and unmarshal ...

    validProxies := make(map[string]int, len(pf.Proxies))
    for name, port := range pf.Proxies {
        if err := validatePort(port); err != nil {
            slog.Warn("skipping invalid proxy entry on load", "name", name, "port", port, "error", err)
            continue
        }
        validProxies[name] = port
    }
    // ... init with validProxies ...
}
```

### 6. JSON file format

```json
{
  "version": 1,
  "proxies": {
    "myapp": 3000,
    "api-gateway": 8080
  }
}
```

A `version` field allows future schema migrations.

# Why this works

- **Thread safety** via `sync.RWMutex` is proven and lightweight for this scale
- **Write-through** prevents the classic partial-state bug where the map is updated but the file write fails
- **mtime check** avoids polling or signal-based reload — works with any external file editor or CLI invocation
- **MkdirAll** makes the store self-contained — no separate init step needed
- **Validate on load** ensures external edits don't bypass business rules (e.g., blocked ports)

# Prevention

Future projects needing simple JSON-backed persistence should follow this ProxyDB pattern:

1. Load from JSON at startup into a `sync.RWMutex`-protected map
2. Mutations use write-through: disk first, memory second
3. Optional hot-reload via mtime comparison on each read
4. Validate entries on load (not just on add)
5. Call `os.MkdirAll` before `os.WriteFile` for self-contained operation
