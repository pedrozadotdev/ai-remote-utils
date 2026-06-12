package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// StartCleanup begins the cleanup loop. It immediately runs a purge, then runs
// cleanup at the given interval. The loop stops when ctx is cancelled.
func StartCleanup(ctx context.Context, dir string, maxAge, interval, minAge time.Duration) {
	// Immediate startup purge
	if count, err := cleanupOnce(ctx, dir, maxAge, minAge); err != nil {
		slog.Warn("startup cleanup failed", "error", err)
	} else if count > 0 {
		slog.Info("startup cleanup removed old files", "count", count)
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				slog.Debug("cleanup manager stopped")
				return
			case <-ticker.C:
				count, err := cleanupOnce(ctx, dir, maxAge, minAge)
				if err != nil {
					slog.Warn("cleanup failed", "error", err)
				} else if count > 0 {
					slog.Info("cleanup removed old files", "count", count)
				}
			}
		}
	}()
}

// cleanupOnce removes files in dir that are older than maxAge.
// Files with mtime less than minAge ago are skipped to avoid races
// with in-flight uploads.
func cleanupOnce(ctx context.Context, dir string, maxAge, minAge time.Duration) (int, error) {
	// Check context first
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // Directory doesn't exist yet, nothing to clean
		}
		return 0, fmt.Errorf("failed to read dir %s: %w", dir, err)
	}

	now := time.Now()
	var removed int

	for _, entry := range entries {
		// Check context periodically
		select {
		case <-ctx.Done():
			return removed, ctx.Err()
		default:
		}

		if entry.IsDir() {
			continue // skip subdirectories
		}

		info, err := entry.Info()
		if err != nil {
			slog.Debug("failed to get file info, skipping", "file", entry.Name(), "error", err)
			continue
		}

		age := now.Sub(info.ModTime())

		// Skip files within the safety window (minAge) to avoid races with uploads
		if age < minAge {
			continue
		}

		// Remove files older than maxAge
		if age > maxAge {
			path := filepath.Join(dir, entry.Name())
			if err := os.Remove(path); err != nil {
				slog.Warn("failed to remove old file", "path", path, "error", err)
				continue
			}
			slog.Debug("removed old file", "path", path, "age", age.Round(time.Second))
			removed++
		}
	}

	return removed, nil
}
