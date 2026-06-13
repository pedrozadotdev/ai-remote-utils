---
title: Go struct-walking placeholder resolution instead of marshal-replace-unmarshal
category: architecture
severity: medium
tags:
  - go
  - json
  - placeholder
  - struct-walking
  - encoding
  - marshal
  - templating
applies_when:
  - Replacing placeholders in config fields with dynamic values
  - Considering a marshal-replace-unmarshal approach for string substitution
  - Project/branch/parameter names may contain JSON metacharacters ("', \\, \\n, unicode)
  - Building a CLI tool or server that resolves template strings in user-provided config
---

# Problem

When you have a Go config struct with string fields that contain placeholders like `<PROJECT>`, `<BRANCH>`, or `<PORT1>`, the naive approach is to:
1. Marshal the struct to JSON
2. Do `strings.ReplaceAll` on the JSON bytes
3. Unmarshal back into a new struct

This works for simple cases, but breaks when the replacement values contain JSON metacharacters such as `"`, `\`, or control characters. A project name like `my"project` or a branch name like `feature\x` would produce invalid JSON, causing `json.Unmarshal` to fail with a cryptic error.

```go
// BAD — marshal-replace-unmarshal approach
data, _ := json.Marshal(cfg)
s := strings.ReplaceAll(string(data), "<PROJECT>", project) // breaks if project has " or \
var resolved Config
json.Unmarshal([]byte(s), &resolved) // may fail with "invalid character"
```

# Context

In the `aru` project (ai-remote-utils), we had a `aru.json` config file with placeholders like `<PROJECT>`, `<BRANCH>`, and `<PORT1>` in setup commands, tmux window names, env values, and proxy entries. The original implementation used the marshal-replace-unmarshal approach with a `SetEscapeHTML(false)` workaround to keep `<` and `>` literal in JSON output.

This had three problems:
1. Project/branch names with JSON metacharacters (`"`, `\`) would silently corrupt the JSON
2. The `SetEscapeHTML(false)` hack was non-obvious and fragile
3. Every resolution incurred a JSON round-trip (marshal → regex → unmarshal)

# Solution

Walk the Go struct fields directly, applying `strings.ReplaceAll` to each string value at the source. No JSON involvement at all.

## Key patterns

### 1. Deep-copy the config first

Don't mutate the caller's struct. Create a deep clone:

```go
func cloneConfig(cfg *AruConfig) *AruConfig {
    if cfg == nil {
        return nil
    }
    clone := &AruConfig{Version: cfg.Version}

    if cfg.Worktree != nil {
        wt := &WorktreeConfig{
            SetupOneshot: cfg.Worktree.SetupOneshot,
        }
        if cfg.Worktree.Setup != nil {
            wt.Setup = append([]string(nil), cfg.Worktree.Setup...)
        }
        if cfg.Worktree.Teardown != nil {
            wt.Teardown = append([]string(nil), cfg.Worktree.Teardown...)
        }
        clone.Worktree = wt
    }

    if cfg.Tmux != nil {
        clone.Tmux = make(TmuxConfig, len(cfg.Tmux))
        for name, win := range cfg.Tmux {
            newWin := TmuxWindow{Command: win.Command}
            if win.Env != nil {
                newWin.Env = make(map[string]string, len(win.Env))
                for k, v := range win.Env {
                    newWin.Env[k] = v
                }
            }
            clone.Tmux[name] = newWin
        }
    }

    if cfg.Proxy != nil {
        p := *cfg.Proxy
        clone.Proxy = &p
    }
    return clone
}
```

### 2. Walk all string-typed fields and apply replacements

```go
func applyPlaceholders(cfg *AruConfig, project, branch string, ports map[int]int, resolvePorts bool) {
    // Walk Worktree fields
    if cfg.Worktree != nil {
        for i, cmd := range cfg.Worktree.Setup {
            cfg.Worktree.Setup[i] = replaceInString(cmd, project, branch, ports, resolvePorts)
        }
        for i, cmd := range cfg.Worktree.Teardown {
            cfg.Worktree.Teardown[i] = replaceInString(cmd, project, branch, ports, resolvePorts)
        }
    }

    // Walk Tmux fields
    for name, win := range cfg.Tmux {
        win.Command = replaceInString(win.Command, project, branch, ports, resolvePorts)
        for k, v := range win.Env {
            win.Env[k] = replaceInString(v, project, branch, ports, resolvePorts)
        }
        cfg.Tmux[name] = win
    }

    // Walk Proxy fields
    if cfg.Proxy != nil {
        cfg.Proxy.Name = replaceInString(cfg.Proxy.Name, project, branch, ports, resolvePorts)
        cfg.Proxy.Port = replaceInString(cfg.Proxy.Port, project, branch, ports, resolvePorts)
    }
}
```

### 3. Single-string replacement

```go
func replaceInString(s, project, branch string, ports map[int]int, resolvePorts bool) string {
    if project != "" {
        s = strings.ReplaceAll(s, "<PROJECT>", project)
    }
    if branch != "" {
        s = strings.ReplaceAll(s, "<BRANCH>", branch)
    }
    if resolvePorts && len(ports) > 0 {
        nums := make([]int, 0, len(ports))
        for n := range ports {
            nums = append(nums, n)
        }
        sort.Ints(nums)
        for _, n := range nums {
            s = strings.ReplaceAll(s, fmt.Sprintf("<PORT%d>", n), strconv.Itoa(ports[n]))
        }
    }
    return s
}
```

### 4. Placeholder scanning without JSON

Find `<PORTn>` placeholders by walking the same struct fields:

```go
func collectPortPlaceholders(cfg *AruConfig) []string {
    if cfg == nil {
        return nil
    }
    seen := make(map[int]bool)

    if cfg.Worktree != nil {
        for _, cmd := range cfg.Worktree.Setup {
            collectPortNumbers(cmd, seen)
        }
        for _, cmd := range cfg.Worktree.Teardown {
            collectPortNumbers(cmd, seen)
        }
    }
    for _, win := range cfg.Tmux {
        collectPortNumbers(win.Command, seen)
        for _, v := range win.Env {
            collectPortNumbers(v, seen)
        }
    }
    if cfg.Proxy != nil {
        collectPortNumbers(cfg.Proxy.Name, seen)
        collectPortNumbers(cfg.Proxy.Port, seen)
    }

    if len(seen) == 0 {
        return nil
    }
    nums := make([]int, 0, len(seen))
    for n := range seen {
        nums = append(nums, n)
    }
    sort.Ints(nums)

    result := make([]string, len(nums))
    for i, n := range nums {
        result[i] = strconv.Itoa(n)
    }
    return result
}
```

## Why this works

- **No JSON escaping concerns**: project/branch names are substituted directly into Go strings, not into JSON byte streams. Names with `"`, `\`, newlines, control chars, or Unicode are handled transparently
- **No round-trip**: bypasses marshal → regex → unmarshal entirely
- **Deterministic output**: port numbers are sorted before substitution (the marshal approach relied on random map iteration order)
- **Smaller codebase**: the `marshalNoEscape`/`SetEscapeHTML(false)` dance is eliminated
- **Easier to debug**: the walker can be traced line by line; the marshal approach was opaque
- **No regex on generated JSON**: the old approach used regex on marshaled JSON output, which is fragile (JSON field order, struct tags, omitempty behavior all affect the regex target)

## What to watch out for

### Remember to update the walker when the struct changes

The struct walker must be manually maintained when new fields are added. If you add a new `AruConfig` field with placeholders, you must walk it. Unlike the marshal approach, there's no automatic serialization.

**Mitigation:** Keep the walker adjacent to the struct definition. Use a test that exercises all fields with placeholder values.

### Deep-copy is manual

Without generics (pre-1.18) or reflection, the deep-clone must be written by hand. For small stable structs this is manageable. For large structs with frequent changes, consider a reflection-based walker.

**Mitigation:** Write a `TestCloneConfig_PreservesAllFields` test that verifies every field survives the clone.

### Sorted field iteration matters

The marshal approach used a `map[string]anything` iteration. The struct walker must handle field ordering explicitly. Sort slices and map keys for deterministic output.

## See also

- `go-json-persistent-store-proxydb-pattern.md` — JSON-backed persistence (the original marshal approach)
- `go-mock-external-commands-testing.md` — Mocking external commands in Go tests (used alongside struct-walking)
- `go-recording-mock-binary-testing.md` — Recording mock binary for integration tests
- `go-coverage-exclude-main.md` — Test coverage (used in the same project)
