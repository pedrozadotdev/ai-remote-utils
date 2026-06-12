package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupOnce_RemovesOldFiles(t *testing.T) {
	dir := t.TempDir()
	oldFile := filepath.Join(dir, "old.txt")
	if err := os.WriteFile(oldFile, []byte("old"), 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	// Set mtime to 2 hours ago
	twoHoursAgo := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldFile, twoHoursAgo, twoHoursAgo); err != nil {
		t.Fatalf("Chtimes error = %v", err)
	}

	count, err := cleanupOnce(context.Background(), dir, 1*time.Hour, 5*time.Minute)
	if err != nil {
		t.Fatalf("cleanupOnce() error = %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 file removed, got %d", count)
	}
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Errorf("old file should be removed")
	}
}

func TestCleanupOnce_PreservesRecentFiles(t *testing.T) {
	dir := t.TempDir()
	newFile := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(newFile, []byte("new"), 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	// File was just created, mtime is now

	count, err := cleanupOnce(context.Background(), dir, 1*time.Hour, 5*time.Minute)
	if err != nil {
		t.Fatalf("cleanupOnce() error = %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 files removed, got %d", count)
	}
	if _, err := os.Stat(newFile); os.IsNotExist(err) {
		t.Errorf("new file should not be removed")
	}
}

func TestCleanupOnce_OnlyOldFilesRemoved(t *testing.T) {
	dir := t.TempDir()

	// Old file (2 hours ago)
	oldFile := filepath.Join(dir, "old.txt")
	if err := os.WriteFile(oldFile, []byte("old"), 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	os.Chtimes(oldFile, time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour))

	// Recent file (just created)
	newFile := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(newFile, []byte("new"), 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	count, err := cleanupOnce(context.Background(), dir, 1*time.Hour, 5*time.Minute)
	if err != nil {
		t.Fatalf("cleanupOnce() error = %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 file removed, got %d", count)
	}
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Errorf("old file should be removed")
	}
	if _, err := os.Stat(newFile); os.IsNotExist(err) {
		t.Errorf("new file should not be removed")
	}
}

func TestCleanupOnce_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	count, err := cleanupOnce(context.Background(), dir, 1*time.Hour, 5*time.Minute)
	if err != nil {
		t.Fatalf("cleanupOnce() error = %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 files removed, got %d", count)
	}
}

func TestCleanupOnce_RespectsMinAge(t *testing.T) {
	dir := t.TempDir()

	// File older than maxAge but newer than minAge
	// maxAge = 5min, minAge = 2min
	// File = 10 min old → old enough (10 > 5), and not too recent (10 > 2) → remove
	tenMinAgo := time.Now().Add(-10 * time.Minute)
	midFile := filepath.Join(dir, "mid.txt")
	if err := os.WriteFile(midFile, []byte("mid"), 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	os.Chtimes(midFile, tenMinAgo, tenMinAgo)

	count, err := cleanupOnce(context.Background(), dir, 5*time.Minute, 2*time.Minute)
	if err != nil {
		t.Fatalf("cleanupOnce() error = %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 file removed (10min old > 5min maxAge), got %d", count)
	}
}

func TestCleanupOnce_SkipsTooRecentFiles(t *testing.T) {
	dir := t.TempDir()

	// File is old (> maxAge) but within the safety window (< minAge)
	// maxAge = 3min, minAge = 5min. File = 4 min old.
	// 4 > 3 (older than maxAge) but 4 < 5 (within safety window) → SKIP
	fourMinAgo := time.Now().Add(-4 * time.Minute)
	recentFile := filepath.Join(dir, "recent.txt")
	if err := os.WriteFile(recentFile, []byte("recent"), 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	os.Chtimes(recentFile, fourMinAgo, fourMinAgo)

	count, err := cleanupOnce(context.Background(), dir, 3*time.Minute, 5*time.Minute)
	if err != nil {
		t.Fatalf("cleanupOnce() error = %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 files removed (file within safety window), got %d", count)
	}
}

func TestCleanupOnce_NonExistentDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nonexistent")
	count, err := cleanupOnce(context.Background(), dir, 1*time.Hour, 5*time.Minute)
	if err != nil {
		t.Fatalf("cleanupOnce() error = %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 files removed for non-existent dir, got %d", count)
	}
}

func TestCleanupOnce_ContextCancellation(t *testing.T) {
	dir := t.TempDir()

	// Create a file
	oldFile := filepath.Join(dir, "old.txt")
	if err := os.WriteFile(oldFile, []byte("old"), 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	os.Chtimes(oldFile, time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Immediate cancellation

	_, err := cleanupOnce(ctx, dir, 1*time.Hour, 5*time.Minute)
	if err == nil {
		t.Error("expected error for cancelled context, got nil")
	}
}

func TestCleanupOnce_StartupPurge(t *testing.T) {
	dir := t.TempDir()

	// Create an old file
	oldFile := filepath.Join(dir, "old.txt")
	if err := os.WriteFile(oldFile, []byte("old"), 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	os.Chtimes(oldFile, time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartCleanup(ctx, dir, 1*time.Hour, 100*time.Millisecond, 5*time.Minute)

	// Give it a moment to run the startup purge
	time.Sleep(200 * time.Millisecond)

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Errorf("old file should have been removed by startup purge")
	}

	cancel()
	time.Sleep(50 * time.Millisecond)
}

func TestCleanupOnce_IntervalCleanup(t *testing.T) {
	dir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartCleanup(ctx, dir, 1*time.Hour, 50*time.Millisecond, 5*time.Minute)

	// Create an old file after starting
	time.Sleep(10 * time.Millisecond)
	oldFile := filepath.Join(dir, "old2.txt")
	if err := os.WriteFile(oldFile, []byte("old"), 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	os.Chtimes(oldFile, time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour))

	// Wait for next cleanup tick
	time.Sleep(100 * time.Millisecond)

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Errorf("old file should have been removed by interval cleanup")
	}

	cancel()
}
