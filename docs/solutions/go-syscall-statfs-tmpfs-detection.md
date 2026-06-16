---
title: Go syscall.Statfs — detect tmpfs filesystem type to survive reboots
category: architecture
severity: medium
tags:
  - go
  - syscall
  - tmpfs
  - statfs
  - filesystem
  - reboot
  - ram-disk
  - linux
  - os
applies_when:
  - Managing tmpfs-backed directories that must survive a system reboot
  - Need to distinguish a tmpfs mount from a regular directory at the same path
  - Building a CLI tool that creates RAM-backed data directories via tmpfs
  - Detecting whether a filesystem type at a path is tmpfs, ext4, or other
  - Implementing a "reboot recovery" check for tmpfs state
---

# Problem

When using `syscall.Mount("tmpfs", path, "tmpfs", 0, "size=200M")` to create RAM-backed directories, the tmpfs data is volatile: after a system reboot, the tmpfs is gone. However, **the mount point directory persists** on disk as an empty regular directory (on ext4 or whatever root filesystem you're using).

This creates a detection problem. Consider:

```
Before reboot:
  ~/.aru/ram/myproject/feature/data/  ← tmpfs mount (volatile RAM)

After reboot:
  ~/.aru/ram/myproject/feature/data/  ← still EXISTS but is a regular ext4 dir (empty)
```

If your code checks `os.Stat(path)` — the path exists! If it checks `os.ReadDir(path)` — it's empty, so you might assume it needs to be recreated. But what if the original `syscall.Mount` call failed (non-root user) and the fallback directory has real user data? An emptiness check would incorrectly wipe that fallback data.

You need a way to ask the kernel: **"Is what's at this path a tmpfs or not?"**

# Context

In the `aru` project (`ai-remote-utils`), the `aru worktree open` command re-attaches to an existing worktree. During initial creation (`aru worktree add`), RAM directories are mounted as tmpfs. After a reboot:

1. The tmpfs mount is gone, but the mount point directory persists as an empty ext4 directory
2. `os.Stat` says the path exists — we can't use existence to decide
3. `os.ReadDir` says it's empty — but a non-root fallback from a previous run might have real content
4. `os.IsNotExist(err)` is never true — the directory survived the reboot

We tried three approaches before converging on the final solution:

- **Approach A (existence check — failed):** `os.Stat` always succeeds because the mount point directory persists
- **Approach B (emptiness check — dangerous):** `os.ReadDir` works when empty, but a non-root fallback with user data would be wiped if we re-mount
- **Approach C (filesystem type check — correct):** `syscall.Statfs` returns the filesystem type magic number, distinguishing tmpfs from ext4

# Solution

Use `syscall.Statfs` to read the filesystem type magic number and compare it against the known tmpfs magic (`0x01021994`).

## Core function

```go
import "syscall"

// TMPFS_MAGIC is the Linux kernel's magic number for tmpfs filesystems.
// Defined in <linux/magic.h> as 0x01021994.
const TMPFS_MAGIC int64 = 0x01021994

// isTmpfs checks if the given path resides on a tmpfs filesystem.
// Returns false if the path doesn't exist or if Statfs fails.
func isTmpfs(path string) bool {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return false
	}
	return stat.Type == TMPFS_MAGIC
}
```

## Reboot recovery pattern

The full reboot-recovery logic combines `isTmpfs` with a safety check:

```go
func ensureRamDirAfterReboot(project, branch string, entry RamDirConfig, target string) {
	subPath := ramDirSubPath(project, branch, entry.Path)

	if isTmpfs(subPath) {
		// Already a proper tmpfs mount — nothing to do
		return
	}

	// Not a tmpfs. Safety check: if the path is a non-empty regular dir,
	// it may be a fallback from a previous non-root run with user data.
	if fi, err := os.Stat(subPath); err == nil && fi.IsDir() {
		entries, _ := os.ReadDir(subPath)
		if len(entries) > 0 {
			slog.Warn("path has data but is not tmpfs; skipping to preserve contents",
				"path", entry.Path)
			return
		}
	}

	// Missing or empty — recreate everything
	slog.Info("path not tmpfs, re-creating", "path", entry.Path)
	os.Remove(symlinkPath) // clean up dangling symlink from old state
	mountRamDirEntry(project, branch, entry, target)
}
```

### Why the kernel magic approach works

The Linux kernel exposes filesystem type information through the `statfs` system call. Each filesystem type has a unique 64-bit magic number registered in `<linux/magic.h>`:

| Filesystem | Magic (hex) | Magic (decimal) | Use case |
|------------|-------------|-----------------|----------|
| tmpfs      | `0x01021994` | 16914836 | RAM-based volatile storage |
| ext4       | `0xEF53`     | 61267 | Standard on-disk filesystem |
| btrfs      | `0x9123683E` | 2433357886 | Copy-on-write filesystem |
| xfs        | `0x58465342` | 1481003842 | High-performance journaling |
| proc       | `0x9FA0`     | 40864 | Process filesystem |

After a reboot, the mount point directory sits on the root filesystem (typically ext4, magic `0xEF53`). `syscall.Statfs` returns `0xEF53`, which doesn't match `0x01021994`, so `isTmpfs` correctly returns `false`. This triggers re-creation without ambiguity.

The safety check (non-empty regular dir) prevents data loss: if the previous run fell back to a regular directory (e.g., the user wasn't root), user content in that directory is preserved.

## Full pattern with mount

```go
// mountRamDir creates a directory and mounts tmpfs at the given path.
// If tmpfs mount fails (e.g. not root), it falls back to a regular directory.
// size accepts a tmpfs size specifier (e.g. "200M", "1G"). If empty, defaults to "200M".
func mountRamDir(path, size string) error {
	if size == "" {
		size = "200M"
	}

	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("failed to create RAM directory %s: %w", path, err)
	}

	mountOpts := "size=" + size
	if err := syscall.Mount("tmpfs", path, "tmpfs", 0, mountOpts); err != nil {
		slog.Warn("failed to mount tmpfs, using regular directory",
			"path", path, "error", err, "size", size)
		return nil // fallback to regular dir
	}

	slog.Info("tmpfs mounted", "path", path, "size", size)
	return nil
}

// unmountRamDir unmounts tmpfs and removes the directory.
// Best-effort: continues on partial failure (e.g., not a mount point).
func unmountRamDir(path string) error {
	if err := syscall.Unmount(path, 0); err != nil {
		// May not be a mount point (e.g., fallback regular directory)
		slog.Debug("unmount failed (may not be a mount point)", "path", path, "error", err)
	}

	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("failed to remove RAM directory %s: %w", path, err)
	}
	return nil
}
```

## Performance and portability characteristics

`syscall.Statfs` is a thin wrapper around the kernel's `statfs` system call. It is:

- **Fast:** No I/O, no forking — just a single kernel call
- **Reliable:** The filesystem type is an intrinsic property of the mounted filesystem, not a heuristic
- **Portable (Linux):** Works on any Linux kernel. On other Unix systems, `statfs` exists but magic numbers differ (BSD uses `MNT_*` flags instead). The Go `syscall` package abstracts the platform differences.
- **No external dependencies:** Uses only the Go standard library's `syscall` package

# Prevention

When designing features that use tmpfs:

1. **Never rely on `os.Stat` existence alone** to determine if a tmpfs mount needs to be remounted after reboot. The mount point directory persists.

2. **Never rely on directory emptiness alone** to determine if a remount is safe. A non-root fallback directory may have content.

3. **Use `syscall.Statfs` for filesystem type detection** whenever you need to distinguish tmpfs from regular directories. This is the authoritative kernel-level check.

4. **Always add a safety check** before remounting: if the path is a non-empty regular directory, skip to preserve user data from a previous fallback run.

5. **Document the magic numbers** you're comparing against. `0x01021994` is not self-documenting — include a comment referencing `<linux/magic.h>`.

See also:
- [Linux kernel magic.h](https://github.com/torvalds/linux/blob/master/include/uapi/linux/magic.h) — all registered filesystem magic numbers
- `go-port-state-persistence-lifecycle.md` — Persisting port state across process restarts (complementary pattern for reboot survival)
