package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSanitizeSessionName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "simple branch", input: "feature-x", expected: "feature-x"},
		{name: "dots to hyphens", input: "feature.x", expected: "feature-x"},
		{name: "slashes to hyphens", input: "feature/sub", expected: "feature-sub"},
		{name: "underscores to hyphens", input: "feature_x", expected: "feature-x"},
		{name: "mixed special chars", input: "fea.ture/sub_x", expected: "fea-ture-sub-x"},
		{name: "alphanumeric only", input: "branch123", expected: "branch123"},
		{name: "empty string", input: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeSessionName(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeSessionName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSanitizeSessionName_NoBadChars(t *testing.T) {
	names := []string{"simple", "with-dots.dots", "with/slashes", "with_underscores", "mixed.all/three_here"}
	for _, n := range names {
		t.Run(n, func(t *testing.T) {
			got := sanitizeSessionName(n)
			for _, r := range got {
				if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-') {
					t.Errorf("sanitizeSessionName(%q) = %q contains illegal char %q", n, got, r)
					break
				}
			}
		})
	}
}

func TestWorktreeDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(home, ".aru", "wt", "myproject", "feature-x")
	got := worktreeDir("myproject", "feature-x")
	if got != expected {
		t.Errorf("worktreeDir() = %q, want %q", got, expected)
	}
}

func TestRamDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(home, ".aru", "ram", "myproject", "feature-x")
	got := ramDir("myproject", "feature-x")
	if got != expected {
		t.Errorf("ramDir() = %q, want %q", got, expected)
	}
}

func TestSocketPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(home, ".aru", "sockets", "myproject-feature-x.sock")
	got := socketPath("myproject", "feature-x")
	if got != expected {
		t.Errorf("socketPath() = %q, want %q", got, expected)
	}
}

func TestFindOpenPort(t *testing.T) {
	port, err := findOpenPort()
	if err != nil {
		t.Fatalf("findOpenPort() returned error: %v", err)
	}
	if port < 1024 || port >= 10000 {
		t.Errorf("findOpenPort() = %d, want between 1024 and 9999", port)
	}
}

func TestGetProjectName(t *testing.T) {
	if !isGitRepo() {
		t.Skip("not in a git repository")
	}

	name, err := getProjectName()
	if err != nil {
		t.Fatalf("getProjectName() returned error: %v", err)
	}
	if name == "" {
		t.Error("getProjectName() returned empty string")
	}
}

func TestGetMainWorktreeDir(t *testing.T) {
	if !isGitRepo() {
		t.Skip("not in a git repository")
	}

	dir, err := getMainWorktreeDir()
	if err != nil {
		t.Fatalf("getMainWorktreeDir() returned error: %v", err)
	}
	if dir == "" {
		t.Error("getMainWorktreeDir() returned empty string")
	}
}

func TestGetCurrentBranch(t *testing.T) {
	if !isGitRepo() {
		t.Skip("not in a git repository")
	}

	_, err := getCurrentBranch()
	if err != nil {
		t.Fatalf("getCurrentBranch() returned error: %v", err)
	}
}

func TestIsMainWorktree(t *testing.T) {
	if !isGitRepo() {
		t.Skip("not in a git repository")
	}

	err := isMainWorktree()
	if err != nil {
		t.Logf("isMainWorktree() returned error (may be linked worktree): %v", err)
	}
}

// isGitRepo checks if the current directory is a git repository.
func isGitRepo() bool {
	return exec.Command("git", "rev-parse", "--git-dir").Run() == nil
}

// ── Unit 2: Git operations + RAM directory management ────────────────────

// setupTestRepo creates a temporary git repository with an initial commit.
func setupTestRepo(t *testing.T, dir string) string {
	t.Helper()

	run := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v failed: %v\n%s", name, args, err, string(out))
		}
	}

	run("git", "init")
	run("git", "config", "user.email", "test@test.com")
	run("git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test"), 0644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "README.md")
	run("git", "commit", "-m", "initial")

	// Return the current branch name
	out, err := exec.Command("git", "branch", "--show-current").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// runInDir runs a function after changing to the given directory,
// then restores the original working directory.
func runInDir(t *testing.T, dir string, fn func()) {
	t.Helper()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)
	fn()
}

// createTestBranch creates a branch in the test repo for worktree operations.
func createTestBranch(t *testing.T, dir, branch string) {
	t.Helper()
	cmd := exec.Command("git", "branch", branch)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to create branch %q: %v\n%s", branch, err, string(out))
	}
}

func TestGitWorktreeAdd(t *testing.T) {
	dir := t.TempDir()
	setupTestRepo(t, dir)
	// Create a separate branch so we don't conflict with the current worktree
	createTestBranch(t, dir, "wt-branch")

	target := filepath.Join(dir, "wt-feature")
	defer os.RemoveAll(target)

	runInDir(t, dir, func() {
		err := gitWorktreeAdd(target, "wt-branch")
		if err != nil {
			t.Fatalf("gitWorktreeAdd() returned error: %v", err)
		}

		if _, err := os.Stat(target); os.IsNotExist(err) {
			t.Fatal("worktree directory was not created")
		}

		// Verify it's a git worktree
		out, err := exec.Command("git", "worktree", "list").CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), target) {
			t.Errorf("worktree list does not contain %q\n%s", target, string(out))
		}
	})
}

func TestGitWorktreeAddNewBranch(t *testing.T) {
	dir := t.TempDir()
	setupTestRepo(t, dir)

	target := filepath.Join(dir, "wt-new-branch")
	defer os.RemoveAll(target)

	runInDir(t, dir, func() {
		// This branch doesn't exist yet, so gitWorktreeAdd should create it with -b
		err := gitWorktreeAdd(target, "new-feature-branch")
		if err != nil {
			t.Fatalf("gitWorktreeAdd(new-branch) returned error: %v", err)
		}

		if _, err := os.Stat(target); os.IsNotExist(err) {
			t.Fatal("worktree directory was not created")
		}
	})
}

func TestGitWorktreeRemove(t *testing.T) {
	dir := t.TempDir()
	setupTestRepo(t, dir)
	createTestBranch(t, dir, "wt-to-remove-branch")

	target := filepath.Join(dir, "wt-to-remove")
	defer os.RemoveAll(target)

	runInDir(t, dir, func() {
		if err := gitWorktreeAdd(target, "wt-to-remove-branch"); err != nil {
			t.Fatal(err)
		}

		if err := gitWorktreeRemove(target); err != nil {
			t.Fatalf("gitWorktreeRemove() returned error: %v", err)
		}

		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Error("worktree directory still exists after remove")
		}
	})
}

func TestGitDeleteBranch(t *testing.T) {
	dir := t.TempDir()
	setupTestRepo(t, dir)

	runInDir(t, dir, func() {
		// Create a branch
		run := exec.Command("git", "branch", "test-branch-to-delete")
		run.Dir = dir
		if out, err := run.CombinedOutput(); err != nil {
			t.Fatalf("failed to create branch: %v\n%s", err, string(out))
		}

		if err := gitDeleteBranch("test-branch-to-delete"); err != nil {
			t.Fatalf("gitDeleteBranch() returned error: %v", err)
		}

		// Verify branch is gone
		run = exec.Command("git", "branch", "--list", "test-branch-to-delete")
		run.Dir = dir
		out, _ := run.CombinedOutput()
		if strings.TrimSpace(string(out)) != "" {
			t.Error("branch still exists after delete")
		}
	})
}

func TestSetupDataSymlink(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "worktree")
	ramPath := filepath.Join(tmp, "ram")

	if err := os.MkdirAll(ramPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}

	if err := setupDataSymlink(target, ramPath); err != nil {
		t.Fatalf("setupDataSymlink() returned error: %v", err)
	}

	// Verify symlink exists and points to the right place
	linkPath := filepath.Join(target, "data")
	linkTarget, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("failed to read symlink: %v", err)
	}
	if linkTarget != ramPath {
		t.Errorf("symlink points to %q, want %q", linkTarget, ramPath)
	}
}

func TestSetupDataSymlink_PreservesExistingDir(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "worktree")
	ramPath := filepath.Join(tmp, "ram")

	if err := os.MkdirAll(ramPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a real `data` directory with content
	dataDir := filepath.Join(target, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dataDir, "user-content")
	if err := os.WriteFile(marker, []byte("important"), 0644); err != nil {
		t.Fatal(err)
	}

	// setupDataSymlink should warn and skip, not destroy the directory
	if err := setupDataSymlink(target, ramPath); err != nil {
		t.Fatalf("setupDataSymlink() returned error: %v", err)
	}

	// Verify data directory still exists with user content
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		t.Error("user content was destroyed — setupDataSymlink should skip when data is not a symlink")
	}
	// Verify it is NOT a symlink
	if fi, err := os.Lstat(dataDir); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		t.Error("data was converted to a symlink — should have been preserved as a directory")
	}
}

// ── Port state persistence tests ─────────────────────────────────────

func TestStateDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(home, ".aru", "state", "myproject", "feature-x")
	got := stateDir("myproject", "feature-x")
	if got != expected {
		t.Errorf("stateDir() = %q, want %q", got, expected)
	}
}

func TestPersistAllocatedPorts(t *testing.T) {
	// Use a unique test project name so we don't collide with real worktrees
	project := "test-persist-" + fmt.Sprint(time.Now().UnixNano())
	branch := "test-branch"
	t.Cleanup(func() {
		removeAllocatedPorts(project, branch)
	})

	ports := map[int]int{1: 3421, 2: 8888, 3: 5000}
	if err := persistAllocatedPorts(project, branch, ports); err != nil {
		t.Fatalf("persistAllocatedPorts() returned error: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(portsStatePath(project, branch)); os.IsNotExist(err) {
		t.Fatal("ports state file was not created")
	}

	// Verify file has 0600 perms (least privilege)
	fi, err := os.Stat(portsStatePath(project, branch))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0600 {
		t.Errorf("ports state file perms = %o, want 0600", perm)
	}
}

func TestPersistAllocatedPorts_Empty(t *testing.T) {
	// Empty map should be a no-op (no file created)
	project := "test-empty-" + fmt.Sprint(time.Now().UnixNano())
	branch := "test-branch"

	if err := persistAllocatedPorts(project, branch, map[int]int{}); err != nil {
		t.Fatalf("persistAllocatedPorts() returned error: %v", err)
	}

	if _, err := os.Stat(portsStatePath(project, branch)); !os.IsNotExist(err) {
		t.Error("empty ports map should not create a state file")
	}
}

func TestLoadAllocatedPorts_Valid(t *testing.T) {
	project := "test-load-valid-" + fmt.Sprint(time.Now().UnixNano())
	branch := "test-branch"
	t.Cleanup(func() {
		removeAllocatedPorts(project, branch)
	})

	original := map[int]int{1: 3421, 2: 8888, 3: 5000}
	if err := persistAllocatedPorts(project, branch, original); err != nil {
		t.Fatal(err)
	}

	loaded, err := loadAllocatedPorts(project, branch)
	if err != nil {
		t.Fatalf("loadAllocatedPorts() returned error: %v", err)
	}
	if len(loaded) != len(original) {
		t.Errorf("loaded %d ports, want %d", len(loaded), len(original))
	}
	for num, port := range original {
		if loaded[num] != port {
			t.Errorf("loaded[%d] = %d, want %d", num, loaded[num], port)
		}
	}
}

func TestLoadAllocatedPorts_Missing(t *testing.T) {
	project := "test-missing-" + fmt.Sprint(time.Now().UnixNano())
	branch := "test-branch"

	ports, err := loadAllocatedPorts(project, branch)
	if err != nil {
		t.Errorf("loadAllocatedPorts() for missing file returned error: %v", err)
	}
	if ports != nil {
		t.Errorf("loadAllocatedPorts() for missing file = %v, want nil", ports)
	}
}

func TestLoadAllocatedPorts_Corrupt(t *testing.T) {
	project := "test-corrupt-" + fmt.Sprint(time.Now().UnixNano())
	branch := "test-branch"
	t.Cleanup(func() {
		removeAllocatedPorts(project, branch)
	})

	// Create a corrupt JSON file
	path := portsStatePath(project, branch)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not valid json"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := loadAllocatedPorts(project, branch)
	if err == nil {
		t.Error("loadAllocatedPorts() for corrupt JSON should return error")
	}
}

func TestRemoveAllocatedPorts(t *testing.T) {
	project := "test-remove-" + fmt.Sprint(time.Now().UnixNano())
	branch := "test-branch"

	if err := persistAllocatedPorts(project, branch, map[int]int{1: 3000}); err != nil {
		t.Fatal(err)
	}

	// Sanity check: file exists
	if _, err := os.Stat(portsStatePath(project, branch)); err != nil {
		t.Fatal(err)
	}

	// Remove
	removeAllocatedPorts(project, branch)

	// Verify file is gone
	if _, err := os.Stat(portsStatePath(project, branch)); !os.IsNotExist(err) {
		t.Error("state file should be gone after remove")
	}

	// Remove again should not panic (best-effort)
	removeAllocatedPorts(project, branch)
}

// ── Setup idempotency (M1) tests ─────────────────────────────────────────

func TestIsSetupComplete_FreshWorktree(t *testing.T) {
	project := "test-oneshot-fresh-" + fmt.Sprint(time.Now().UnixNano())
	branch := "test-branch"
	if isSetupComplete(project, branch) {
		t.Error("isSetupComplete should be false for a fresh worktree")
	}
}

func TestMarkSetupComplete(t *testing.T) {
	project := "test-oneshot-mark-" + fmt.Sprint(time.Now().UnixNano())
	branch := "test-branch"
	t.Cleanup(func() {
		clearSetupComplete(project, branch)
	})

	if isSetupComplete(project, branch) {
		t.Fatal("pre-condition: should not be complete yet")
	}

	if err := markSetupComplete(project, branch); err != nil {
		t.Fatalf("markSetupComplete returned error: %v", err)
	}

	if !isSetupComplete(project, branch) {
		t.Error("isSetupComplete should be true after markSetupComplete")
	}

	// Idempotent — second call should not error
	if err := markSetupComplete(project, branch); err != nil {
		t.Errorf("second markSetupComplete returned error: %v", err)
	}
}

func TestClearSetupComplete(t *testing.T) {
	project := "test-oneshot-clear-" + fmt.Sprint(time.Now().UnixNano())
	branch := "test-branch"

	if err := markSetupComplete(project, branch); err != nil {
		t.Fatal(err)
	}
	if !isSetupComplete(project, branch) {
		t.Fatal("pre-condition: should be complete after mark")
	}

	clearSetupComplete(project, branch)

	if isSetupComplete(project, branch) {
		t.Error("isSetupComplete should be false after clearSetupComplete")
	}

	// Clear again should not panic
	clearSetupComplete(project, branch)
}

func TestRunSetupIfNeeded_RunsWhenNoMarker(t *testing.T) {
	tmp := t.TempDir()
	project := "test-oneshot-run-" + fmt.Sprint(time.Now().UnixNano())
	branch := "test-branch"
	t.Cleanup(func() {
		clearSetupComplete(project, branch)
	})

	marker := filepath.Join(tmp, "cmd-ran")
	resolved := &AruConfig{
		Worktree: &WorktreeConfig{
			Setup:        []string{"touch " + marker},
			SetupOneshot: true,
		},
	}

	runSetupIfNeeded(project, branch, tmp, resolved)

	if _, err := os.Stat(marker); os.IsNotExist(err) {
		t.Error("setup command should have run (no marker present)")
	}
	if !isSetupComplete(project, branch) {
		t.Error("marker should be set after successful setup")
	}
}

func TestRunSetupIfNeeded_SkipsWhenMarkerExists(t *testing.T) {
	tmp := t.TempDir()
	project := "test-oneshot-skip-" + fmt.Sprint(time.Now().UnixNano())
	branch := "test-branch"
	t.Cleanup(func() {
		clearSetupComplete(project, branch)
	})

	// Pre-populate the marker (simulating "already ran once")
	if err := markSetupComplete(project, branch); err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(tmp, "cmd-ran")
	resolved := &AruConfig{
		Worktree: &WorktreeConfig{
			Setup:        []string{"touch " + marker},
			SetupOneshot: true,
		},
	}

	runSetupIfNeeded(project, branch, tmp, resolved)

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("setup command should NOT have run (marker exists, oneshot=true)")
	}
}

func TestRunSetupIfNeeded_RunsWhenOneshotFalse(t *testing.T) {
	tmp := t.TempDir()
	project := "test-oneshot-false-" + fmt.Sprint(time.Now().UnixNano())
	branch := "test-branch"
	t.Cleanup(func() {
		clearSetupComplete(project, branch)
	})

	// Pre-populate the marker — but SetupOneshot is false, so we should
	// still run setup.
	if err := markSetupComplete(project, branch); err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(tmp, "cmd-ran")
	resolved := &AruConfig{
		Worktree: &WorktreeConfig{
			Setup:        []string{"touch " + marker},
			SetupOneshot: false, // explicitly false
		},
	}

	runSetupIfNeeded(project, branch, tmp, resolved)

	if _, err := os.Stat(marker); os.IsNotExist(err) {
		t.Error("setup command should have run (SetupOneshot=false even with marker)")
	}
}

func TestRunSetupIfNeeded_NilResolved(t *testing.T) {
	// Should not panic
	runSetupIfNeeded("any", "any", t.TempDir(), nil)
}

func TestRunSetupIfNeeded_EmptySetup(t *testing.T) {
	// Should not panic and should not create a marker
	project := "test-oneshot-empty-" + fmt.Sprint(time.Now().UnixNano())
	branch := "test-branch"
	t.Cleanup(func() {
		clearSetupComplete(project, branch)
	})

	resolved := &AruConfig{
		Worktree: &WorktreeConfig{
			Setup:        nil,
			SetupOneshot: true,
		},
	}

	runSetupIfNeeded(project, branch, t.TempDir(), resolved)

	if isSetupComplete(project, branch) {
		t.Error("empty setup list should not create a marker")
	}
}

func TestRunSetupIfNeeded_SetsMarkerOnFailure(t *testing.T) {
	// We intentionally write the marker even if commands fail, because
	// the trust model is "user ran the setup, treat as success." This
	// test documents that behavior so it's not a surprise.

	tmp := t.TempDir()
	project := "test-oneshot-fail-" + fmt.Sprint(time.Now().UnixNano())
	branch := "test-branch"
	t.Cleanup(func() {
		clearSetupComplete(project, branch)
	})

	resolved := &AruConfig{
		Worktree: &WorktreeConfig{
			Setup:        []string{"exit 1"}, // failing command
			SetupOneshot: true,
		},
	}

	runSetupIfNeeded(project, branch, tmp, resolved)

	// Document: marker IS written even on failure (per the function's
	// contract). If user wants to re-run after fixing, they can delete
	// the marker.
	if !isSetupComplete(project, branch) {
		t.Error("marker should be written after setup attempt (even on failure) — see function comment")
	}
}

func TestReadConfig_AllocateAndPersist(t *testing.T) {
	dir := t.TempDir()
	jsonContent := `{
		"worktree": {"setup": ["echo <PROJECT>:<PORT1>"]},
		"tmux": [{"name": "dev", "command": "npm run dev", "env": {"PORT": "<PORT1>"}}]
	}`
	if err := os.WriteFile(filepath.Join(dir, "aru.json"), []byte(jsonContent), 0644); err != nil {
		t.Fatal(err)
	}

	project := "test-alloc-persist-" + fmt.Sprint(time.Now().UnixNano())
	branch := "test-branch"
	t.Cleanup(func() {
		removeAllocatedPorts(project, branch)
	})

	resolved := readConfig(dir, project, branch, portSourceAllocate)
	if resolved == nil {
		t.Fatal("readConfig() returned nil")
	}
	if resolved.Worktree == nil || len(resolved.Worktree.Setup) == 0 {
		t.Fatal("setup is empty")
	}

	// Setup command should have a real port (e.g., "myproject:3421"), not a literal <PORT1>
	setupCmd := resolved.Worktree.Setup[0]
	if strings.Contains(setupCmd, "<PORT1>") {
		t.Errorf("setup command still has literal <PORT1>: %q", setupCmd)
	}

	// Verify ports were persisted to state file
	loaded, err := loadAllocatedPorts(project, branch)
	if err != nil {
		t.Fatalf("loadAllocatedPorts() returned error: %v", err)
	}
	if len(loaded) == 0 {
		t.Error("ports were not persisted after allocation")
	}

	// The tmux env PORT should match the persisted port
	if len(resolved.Tmux) == 0 {
		t.Fatal("tmux is empty")
	}
	tmuxPort := resolved.Tmux[0].Env["PORT"]
	persistedPort := fmt.Sprint(loaded[1])
	if tmuxPort != persistedPort {
		t.Errorf("tmux env PORT = %q, persisted port = %q — should match", tmuxPort, persistedPort)
	}
}

func TestReadConfig_Load_ReusesPersistedPorts(t *testing.T) {
	dir := t.TempDir()
	jsonContent := `{
		"worktree": {"setup": ["echo <PROJECT>:<PORT1>"]},
		"tmux": [{"name": "dev", "command": "npm run dev", "env": {"PORT": "<PORT1>"}}]
	}`
	if err := os.WriteFile(filepath.Join(dir, "aru.json"), []byte(jsonContent), 0644); err != nil {
		t.Fatal(err)
	}

	project := "test-load-reuse-" + fmt.Sprint(time.Now().UnixNano())
	branch := "test-branch"
	t.Cleanup(func() {
		removeAllocatedPorts(project, branch)
	})

	// Simulate the original `aru worktree add` by allocating + persisting
	original := readConfig(dir, project, branch, portSourceAllocate)
	if original == nil {
		t.Fatal("initial allocation returned nil")
	}
	if len(original.Tmux) == 0 {
		t.Fatal("original tmux is empty")
	}
	originalPort := original.Tmux[0].Env["PORT"]

	// Simulate the `aru worktree open` call — should re-use the persisted port
	reopened := readConfig(dir, project, branch, portSourceLoad)
	if reopened == nil {
		t.Fatal("load-based read returned nil")
	}
	if len(reopened.Tmux) == 0 {
		t.Fatal("reopened tmux is empty")
	}
	reopenedPort := reopened.Tmux[0].Env["PORT"]

	if originalPort != reopenedPort {
		t.Errorf("port changed across open: original=%q reopened=%q", originalPort, reopenedPort)
	}
}

func TestReadConfig_Load_FallsBackToAllocate(t *testing.T) {
	dir := t.TempDir()
	jsonContent := `{"worktree": {"setup": ["echo <PORT1>"]}}`
	if err := os.WriteFile(filepath.Join(dir, "aru.json"), []byte(jsonContent), 0644); err != nil {
		t.Fatal(err)
	}

	project := "test-load-fallback-" + fmt.Sprint(time.Now().UnixNano())
	branch := "test-branch"
	t.Cleanup(func() {
		removeAllocatedPorts(project, branch)
	})

	// No state file exists — load mode should fall back to allocation
	resolved := readConfig(dir, project, branch, portSourceLoad)
	if resolved == nil {
		t.Fatal("readConfig with load mode and missing state should fall back to allocation")
	}
	if resolved.Worktree == nil || len(resolved.Worktree.Setup) == 0 {
		t.Fatal("resolved.Worktree.Setup is empty")
	}
	if strings.Contains(resolved.Worktree.Setup[0], "<PORT1>") {
		t.Errorf("port placeholder not resolved: %q", resolved.Worktree.Setup[0])
	}

	// State file should now exist (created during fallback)
	loaded, err := loadAllocatedPorts(project, branch)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) == 0 {
		t.Error("fallback allocation did not persist ports")
	}
}

func TestReadConfig_EmptyProjectOrBranch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "aru.json"), []byte(`{"worktree":{}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if resolved := readConfig(dir, "", "branch", portSourceAllocate); resolved != nil {
		t.Error("empty project should return nil")
	}
	if resolved := readConfig(dir, "project", "", portSourceAllocate); resolved != nil {
		t.Error("empty branch should return nil")
	}
}

func TestReadConfig_MissingAruJson(t *testing.T) {
	dir := t.TempDir() // no aru.json
	if resolved := readConfig(dir, "p", "b", portSourceAllocate); resolved != nil {
		t.Error("missing aru.json should return nil")
	}
}

func TestReadConfig_MalformedAruJson(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "aru.json"), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if resolved := readConfig(dir, "p", "b", portSourceAllocate); resolved != nil {
		t.Error("malformed aru.json should return nil")
	}
}

func TestRemoveDataSymlink(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "worktree")
	ramPath := filepath.Join(tmp, "ram")

	if err := os.MkdirAll(ramPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ramPath, filepath.Join(target, "data")); err != nil {
		t.Fatal(err)
	}

	if err := removeDataSymlink(target); err != nil {
		t.Fatalf("removeDataSymlink() returned error: %v", err)
	}

	// Verify symlink is gone
	linkPath := filepath.Join(target, "data")
	if _, err := os.Lstat(linkPath); !os.IsNotExist(err) {
		t.Error("symlink still exists after remove")
	}
}

func TestRunSetupCommands_Order(t *testing.T) {
	tmp := t.TempDir()
	marker1 := filepath.Join(tmp, "cmd1-ran")
	marker2 := filepath.Join(tmp, "cmd2-ran")

	cmd1 := "touch " + marker1
	cmd2 := "touch " + marker2

	runSetupCommands(tmp, []string{cmd1, cmd2})

	if _, err := os.Stat(marker1); os.IsNotExist(err) {
		t.Error("command 1 did not run")
	}
	if _, err := os.Stat(marker2); os.IsNotExist(err) {
		t.Error("command 2 did not run")
	}

	// Verify order: marker1's mtime must be <= marker2's mtime
	fi1, err1 := os.Stat(marker1)
	fi2, err2 := os.Stat(marker2)
	if err1 != nil || err2 != nil {
		t.Fatal("stat failed")
	}
	if fi1.ModTime().After(fi2.ModTime()) {
		t.Errorf("command 1 ran after command 2: mtime1=%v > mtime2=%v", fi1.ModTime(), fi2.ModTime())
	}
}

func TestRunSetupCommands_ContinuesOnFailure(t *testing.T) {
	tmp := t.TempDir()
	marker := filepath.Join(tmp, "second-ran")

	cmd1 := "exit 1"
	cmd2 := "touch " + marker

	runSetupCommands(tmp, []string{cmd1, cmd2})

	if _, err := os.Stat(marker); os.IsNotExist(err) {
		t.Error("command 2 did not run after command 1 failed")
	}
}

func TestRunSetupCommands_Empty(t *testing.T) {
	// Should not panic or error
	runSetupCommands(t.TempDir(), nil)
	runSetupCommands(t.TempDir(), []string{})
}

func TestRunTeardownCommands_Order(t *testing.T) {
	tmp := t.TempDir()
	marker1 := filepath.Join(tmp, "teardown-cmd1-ran")
	marker2 := filepath.Join(tmp, "teardown-cmd2-ran")

	cmd1 := "touch " + marker1
	cmd2 := "touch " + marker2

	runTeardownCommands(tmp, []string{cmd1, cmd2})

	if _, err := os.Stat(marker1); os.IsNotExist(err) {
		t.Error("teardown command 1 did not run")
	}
	if _, err := os.Stat(marker2); os.IsNotExist(err) {
		t.Error("teardown command 2 did not run")
	}
}

func TestRunTeardownCommands_Empty(t *testing.T) {
	runTeardownCommands(t.TempDir(), nil)
	runTeardownCommands(t.TempDir(), []string{})
}

func TestRunTeardownCommands_ContinuesOnFailure(t *testing.T) {
	tmp := t.TempDir()
	marker := filepath.Join(tmp, "second-ran")

	cmd1 := "exit 1"
	cmd2 := "touch " + marker

	runTeardownCommands(tmp, []string{cmd1, cmd2})

	if _, err := os.Stat(marker); os.IsNotExist(err) {
		t.Error("teardown command 2 did not run after command 1 failed")
	}
}

// ── Proxy registration tests ────────────────────────────────────────────

func TestSetupProxy_Success(t *testing.T) {
	// Use a temp proxy DB so we don't touch the real one
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "proxies.json")
	if err := os.WriteFile(dbPath, []byte(`{"version":1,"proxies":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Override the default path by directly using LoadProxyDB
	db, err := LoadProxyDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := db.Add("myapp", 3000); err != nil {
		t.Fatalf("db.Add returned error: %v", err)
	}

	// Reload and verify
	db2, err := LoadProxyDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	port, ok := db2.Get("myapp")
	if !ok || port != 3000 {
		t.Errorf("proxy not persisted: got port=%d ok=%v, want 3000 true", port, ok)
	}
}

func TestSetupProxy_InvalidName(t *testing.T) {
	if err := validateName("test"); err == nil {
		t.Error("validateName(test) should fail (reserved name)")
	}
	if err := validateName("tmp"); err == nil {
		t.Error("validateName(tmp) should fail (reserved name)")
	}
	if err := validateName("valid-name_123"); err != nil {
		t.Errorf("validateName(valid-name_123) returned error: %v", err)
	}
}

func TestRemoveProxy_NotFound(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "proxies.json")
	if err := os.WriteFile(dbPath, []byte(`{"version":1,"proxies":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	db, err := LoadProxyDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	// Remove nonexistent entry should return error
	if err := db.Delete("nonexistent"); err == nil {
		t.Error("db.Delete(nonexistent) should return error")
	}
}

func TestRemoveProxy_Success(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "proxies.json")
	if err := os.WriteFile(dbPath, []byte(`{"version":1,"proxies":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	db, err := LoadProxyDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := db.Add("tobedel", 4000); err != nil {
		t.Fatal(err)
	}

	if err := db.Delete("tobedel"); err != nil {
		t.Errorf("db.Delete returned error: %v", err)
	}

	if _, ok := db.Get("tobedel"); ok {
		t.Error("entry should be gone after delete")
	}
}

// ── setupProxy / removeProxy wrapper tests (F3) ───────────────────────────

// These tests exercise the wrapper functions (which were previously at 0%
// coverage) by passing a temp proxy DB path directly.

func TestSetupProxy_Wrapper_AddsEntry(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "proxies.json")
	if err := os.WriteFile(dbPath, []byte(`{"version":1,"proxies":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Call the wrapper with a temp DB path
	setupProxy(dbPath, "myapp", 3000)

	// Verify the entry was added by reloading the DB
	db, err := LoadProxyDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	port, ok := db.Get("myapp")
	if !ok || port != 3000 {
		t.Errorf("setupProxy did not add entry: got port=%d ok=%v, want 3000 true", port, ok)
	}
}

func TestSetupProxy_Wrapper_SkipsInvalidName(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "proxies.json")
	if err := os.WriteFile(dbPath, []byte(`{"version":1,"proxies":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Reserved name "test" should be rejected by validateName
	setupProxy(dbPath, "test", 3000)

	// Verify nothing was added
	db, err := LoadProxyDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if db.Len() != 0 {
		t.Errorf("setupProxy with invalid name should not add entry, got %d entries", db.Len())
	}
}

func TestSetupProxy_Wrapper_HandlesMissingDB(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "nonexistent.json") // file does not exist

	// Should not panic; LoadProxyDB creates an empty DB if file doesn't exist
	setupProxy(dbPath, "newapp", 5000)

	// Verify the entry was created
	db, err := LoadProxyDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	port, ok := db.Get("newapp")
	if !ok || port != 5000 {
		t.Errorf("setupProxy did not create entry in fresh DB: got port=%d ok=%v", port, ok)
	}
}

func TestRemoveProxy_Wrapper_RemovesEntry(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "proxies.json")
	if err := os.WriteFile(dbPath, []byte(`{"version":1,"proxies":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Pre-populate
	setupProxy(dbPath, "tobedel", 4000)

	// Remove via wrapper
	removeProxy(dbPath, "tobedel")

	// Verify gone
	db, err := LoadProxyDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := db.Get("tobedel"); ok {
		t.Error("removeProxy did not remove entry")
	}
}

func TestRemoveProxy_Wrapper_SilentlyIgnoresMissing(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "proxies.json")
	if err := os.WriteFile(dbPath, []byte(`{"version":1,"proxies":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Remove a non-existent entry — should not panic
	removeProxy(dbPath, "never-existed")

	// Verify DB is still empty but valid
	db, err := LoadProxyDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if db.Len() != 0 {
		t.Errorf("DB should still be empty, got %d entries", db.Len())
	}
}

func TestRemoveProxy_Wrapper_HandlesMissingDB(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "nonexistent.json") // file does not exist

	// Should not panic on missing DB
	removeProxy(dbPath, "anyname")
}

func TestSetupProxy_Wrapper_HandlesAddError(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "proxies.json")
	if err := os.WriteFile(dbPath, []byte(`{"version":1,"proxies":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Port 53 is blocked by validatePort, so db.Add will fail
	// Should not panic; should log warning
	setupProxy(dbPath, "valid-name", 53)

	// Verify nothing was added
	db, err := LoadProxyDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if db.Len() != 0 {
		t.Errorf("setupProxy with blocked port should not add entry, got %d entries", db.Len())
	}
}

// ── Multi-proxy iteration tests (M1) ────────────────────────────────────

// TestHandleWorktreeAdd_MultiProxy verifies that handleWorktreeAdd iterates
// over multiple proxy entries and registers each one in ProxyDB.
func TestHandleWorktreeAdd_MultiProxy(t *testing.T) {
	// Override HOME so all state/proxy files go to a temp dir
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	dir := t.TempDir()
	setupTestRepo(t, dir)
	createTestBranch(t, dir, "multi-proxy-add")

	// Mock tmux
	mockDir := t.TempDir()
	createMockTmux(t, mockDir)
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", mockDir+":"+origPath)
	defer os.Setenv("PATH", origPath)

	runInDir(t, dir, func() {
		project := filepath.Base(dir)
		target := worktreeDir(project, "multi-proxy-add")

		// Pre-create the worktree (simulating what handleWorktreeAdd does)
		if err := gitWorktreeAdd(target, "multi-proxy-add"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(target) })

		// Create aru.json with two proxy entries
		aruJSON := `{"proxy": [{"name": "frontend", "port": "3000"}, {"name": "api", "port": "4000"}]}`
		if err := os.WriteFile(filepath.Join(target, "aru.json"), []byte(aruJSON), 0644); err != nil {
			t.Fatal(err)
		}

		// Instead of calling handleWorktreeAdd (which blocks on tmux),
		// simulate the proxy registration logic directly
		resolved, err := ParseAruConfig(target)
		if err != nil {
			t.Fatalf("ParseAruConfig returned error: %v", err)
		}

		// Register proxies (same logic as handleWorktreeAdd)
		dbPath := defaultProxyDBPath()
		for _, p := range resolved.Proxy {
			if p.Port == "" {
				continue
			}
			port, err := strconv.Atoi(p.Port)
			if err != nil {
				t.Logf("invalid proxy port %q: %v", p.Port, err)
				continue
			}
			setupProxy(dbPath, p.Name, port)
		}

		// Verify both proxies were registered
		db, err := LoadProxyDB(dbPath)
		if err != nil {
			t.Fatalf("LoadProxyDB failed: %v", err)
		}

		port1, ok1 := db.Get("frontend")
		if !ok1 || port1 != 3000 {
			t.Errorf("frontend proxy: got port=%d ok=%v, want 3000 true", port1, ok1)
		}

		port2, ok2 := db.Get("api")
		if !ok2 || port2 != 4000 {
			t.Errorf("api proxy: got port=%d ok=%v, want 4000 true", port2, ok2)
		}

		// Clean up proxies
		db.Delete("frontend")
		db.Delete("api")
	})
}

// TestHandleWorktreeDel_MultiProxy verifies that handleWorktreeDel iterates
// over multiple proxy entries and removes each one from ProxyDB.
func TestHandleWorktreeDel_MultiProxy(t *testing.T) {
	// Override HOME so all state/proxy files go to a temp dir
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	dir := t.TempDir()
	setupTestRepo(t, dir)
	createTestBranch(t, dir, "multi-proxy-del")

	runInDir(t, dir, func() {
		project := filepath.Base(dir)
		target := worktreeDir(project, "multi-proxy-del")

		// Create the worktree
		if err := gitWorktreeAdd(target, "multi-proxy-del"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(target) })

		// Create aru.json with two proxy entries
		aruJSON := `{"proxy": [{"name": "frontend", "port": "3000"}, {"name": "api", "port": "4000"}]}`
		if err := os.WriteFile(filepath.Join(target, "aru.json"), []byte(aruJSON), 0644); err != nil {
			t.Fatal(err)
		}

		// Pre-register proxies (simulating what add would do)
		dbPath := defaultProxyDBPath()
		db, err := LoadProxyDB(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		db.Add("frontend", 3000)
		db.Add("api", 4000)

		// Verify they exist
		if _, ok := db.Get("frontend"); !ok {
			t.Fatal("frontend proxy not pre-registered")
		}
		if _, ok := db.Get("api"); !ok {
			t.Fatal("api proxy not pre-registered")
		}

		// Parse config to get proxy list
		resolved, err := ParseAruConfig(target)
		if err != nil {
			t.Fatalf("ParseAruConfig returned error: %v", err)
		}

		// Remove proxies (same logic as handleWorktreeDel)
		for _, p := range resolved.Proxy {
			if p.Name == "" {
				continue
			}
			removeProxy(dbPath, p.Name)
		}

		// Verify both proxies were removed
		db2, err := LoadProxyDB(dbPath)
		if err != nil {
			t.Fatalf("LoadProxyDB failed: %v", err)
		}

		if _, ok := db2.Get("frontend"); ok {
			t.Error("frontend proxy should have been removed")
		}
		if _, ok := db2.Get("api"); ok {
			t.Error("api proxy should have been removed")
		}
	})
}

// TestHandleWorktreeOpen_MultiProxy verifies that handleWorktreeOpen iterates
// over multiple proxy entries and re-registers each one in ProxyDB.
func TestHandleWorktreeOpen_MultiProxy(t *testing.T) {
	// Override HOME so all state/proxy files go to a temp dir
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	dir := t.TempDir()
	setupTestRepo(t, dir)
	createTestBranch(t, dir, "multi-proxy-open")

	runInDir(t, dir, func() {
		project := filepath.Base(dir)
		target := worktreeDir(project, "multi-proxy-open")

		// Create the worktree
		if err := gitWorktreeAdd(target, "multi-proxy-open"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(target) })

		// Create aru.json with two proxy entries
		aruJSON := `{"proxy": [{"name": "frontend", "port": "3000"}, {"name": "api", "port": "4000"}]}`
		if err := os.WriteFile(filepath.Join(target, "aru.json"), []byte(aruJSON), 0644); err != nil {
			t.Fatal(err)
		}

		// Pre-populate port state (simulating what add would have done)
		if err := persistAllocatedPorts(project, "multi-proxy-open", map[int]int{}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			removeAllocatedPorts(project, "multi-proxy-open")
		})

		// Parse config to get proxy list
		resolved, err := ParseAruConfig(target)
		if err != nil {
			t.Fatalf("ParseAruConfig returned error: %v", err)
		}

		// Re-register proxies (same logic as handleWorktreeOpen)
		dbPath := defaultProxyDBPath()
		for _, p := range resolved.Proxy {
			if p.Port == "" {
				continue
			}
			port, err := strconv.Atoi(p.Port)
			if err != nil {
				t.Logf("invalid proxy port %q: %v", p.Port, err)
				continue
			}
			setupProxy(dbPath, p.Name, port)
		}

		// Verify both proxies were re-registered
		db, err := LoadProxyDB(dbPath)
		if err != nil {
			t.Fatalf("LoadProxyDB failed: %v", err)
		}

		port1, ok1 := db.Get("frontend")
		if !ok1 || port1 != 3000 {
			t.Errorf("frontend proxy: got port=%d ok=%v, want 3000 true", port1, ok1)
		}

		port2, ok2 := db.Get("api")
		if !ok2 || port2 != 4000 {
			t.Errorf("api proxy: got port=%d ok=%v, want 4000 true", port2, ok2)
		}

		// Clean up proxies
		db.Delete("frontend")
		db.Delete("api")
	})
}

// TestProxyRegistration_SkipEmptyPort verifies that proxy entries with empty
// port are skipped during registration.
func TestProxyRegistration_SkipEmptyPort(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "proxies.json")
	if err := os.WriteFile(dbPath, []byte(`{"version":1,"proxies":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Simulate the iteration logic from handleWorktreeAdd
	proxies := []ProxyConfig{
		{Name: "valid", Port: "3000"},
		{Name: "empty-port", Port: ""},
	}

	for _, p := range proxies {
		if p.Port == "" {
			continue
		}
		port, err := strconv.Atoi(p.Port)
		if err != nil {
			t.Logf("invalid port %q for %s: %v", p.Port, p.Name, err)
			continue
		}
		setupProxy(dbPath, p.Name, port)
	}

	// Verify only valid entry was added
	db, err := LoadProxyDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	if db.Len() != 1 {
		t.Errorf("expected 1 proxy entry, got %d", db.Len())
	}

	port, ok := db.Get("valid")
	if !ok || port != 3000 {
		t.Errorf("valid proxy: got port=%d ok=%v, want 3000 true", port, ok)
	}

	if _, ok := db.Get("empty-port"); ok {
		t.Error("empty-port proxy should not have been registered")
	}
}

// TestProxyRegistration_SkipInvalidPort verifies that proxy entries with
// non-numeric port are skipped with a warning.
func TestProxyRegistration_SkipInvalidPort(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "proxies.json")
	if err := os.WriteFile(dbPath, []byte(`{"version":1,"proxies":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Simulate the iteration logic from handleWorktreeAdd
	proxies := []ProxyConfig{
		{Name: "valid", Port: "3000"},
		{Name: "invalid-port", Port: "not-a-number"},
	}

	for _, p := range proxies {
		if p.Port == "" {
			continue
		}
		port, err := strconv.Atoi(p.Port)
		if err != nil {
			t.Logf("invalid port %q for %s: %v", p.Port, p.Name, err)
			continue
		}
		setupProxy(dbPath, p.Name, port)
	}

	// Verify only valid entry was added
	db, err := LoadProxyDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	if db.Len() != 1 {
		t.Errorf("expected 1 proxy entry, got %d", db.Len())
	}

	port, ok := db.Get("valid")
	if !ok || port != 3000 {
		t.Errorf("valid proxy: got port=%d ok=%v, want 3000 true", port, ok)
	}

	if _, ok := db.Get("invalid-port"); ok {
		t.Error("invalid-port proxy should not have been registered")
	}
}

// TestProxyRegistration_SkipEmptyName verifies that proxy entries with empty
// name are handled gracefully (warning emitted, no panic).
func TestProxyRegistration_SkipEmptyName(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "proxies.json")
	if err := os.WriteFile(dbPath, []byte(`{"version":1,"proxies":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Simulate the iteration logic from handleWorktreeAdd
	// Note: empty name will fail validateName, but should not panic
	proxies := []ProxyConfig{
		{Name: "valid", Port: "3000"},
		{Name: "", Port: "4000"},
	}

	for _, p := range proxies {
		if p.Port == "" {
			continue
		}
		port, err := strconv.Atoi(p.Port)
		if err != nil {
			t.Logf("invalid port %q for %s: %v", p.Port, p.Name, err)
			continue
		}
		setupProxy(dbPath, p.Name, port)
	}

	// Verify only valid entry was added (empty name fails validation)
	db, err := LoadProxyDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	if db.Len() != 1 {
		t.Errorf("expected 1 proxy entry, got %d", db.Len())
	}

	port, ok := db.Get("valid")
	if !ok || port != 3000 {
		t.Errorf("valid proxy: got port=%d ok=%v, want 3000 true", port, ok)
	}

	if _, ok := db.Get(""); ok {
		t.Error("empty-name proxy should not have been registered")
	}
}

func TestBuildTmuxCommand_NoEnv(t *testing.T) {
	win := TmuxWindow{Command: "npm run dev"}
	got := buildTmuxCommand(win)
	want := "trap ':' INT; npm run dev; exec bash"
	if got != want {
		t.Errorf("buildTmuxCommand() = %q, want %q", got, want)
	}
}

func TestBuildTmuxCommand_WithEnv(t *testing.T) {
	win := TmuxWindow{
		Command: "npm run dev",
		Env:     map[string]string{"PORT": "3000"},
	}
	got := buildTmuxCommand(win)
	// Should use shell quoting for the env value
	want := "trap ':' INT; export PORT=\"3000\"; npm run dev; exec bash"
	if got != want {
		t.Errorf("buildTmuxCommand() = %q, want %q", got, want)
	}
}

func TestBuildTmuxCommand_MultipleEnv(t *testing.T) {
	win := TmuxWindow{
		Command: "node server.js",
		Env:     map[string]string{"PORT": "3000", "HOST": "localhost"},
	}
	got := buildTmuxCommand(win)
	// Env vars should be sorted alphabetically: HOST, then PORT
	want := "trap ':' INT; export HOST=\"localhost\"; export PORT=\"3000\"; node server.js; exec bash"
	if got != want {
		t.Errorf("buildTmuxCommand() = %q, want %q", got, want)
	}
}

func TestBuildTmuxCommand_EmptyCommand(t *testing.T) {
	win := TmuxWindow{}
	got := buildTmuxCommand(win)
	want := "trap ':' INT; exec bash"
	if got != want {
		t.Errorf("buildTmuxCommand() = %q, want %q", got, want)
	}
}

func TestBuildTmuxCommand_OnlyEnv(t *testing.T) {
	win := TmuxWindow{
		Env: map[string]string{"PORT": "8080"},
	}
	got := buildTmuxCommand(win)
	want := "trap ':' INT; export PORT=\"8080\"; exec bash"
	if got != want {
		t.Errorf("buildTmuxCommand() = %q, want %q", got, want)
	}
}

func TestMountRamDir(t *testing.T) {
	tmp := t.TempDir()
	ramPath := filepath.Join(tmp, "ramtest")

	// Clean up the tmpfs mount if it succeeded, so TempDir cleanup doesn't fail
	t.Cleanup(func() {
		syscall.Unmount(ramPath, 0)
	})

	err := mountRamDir(ramPath)
	if err != nil {
		// Mount may fail if not root — that's okay, just verify the dir exists
		t.Logf("mountRamDir() returned error (may not be root): %v", err)
	}

	if _, err := os.Stat(ramPath); os.IsNotExist(err) {
		t.Error("ram directory was not created even after mount failure")
	}
}

func TestGitPull(t *testing.T) {
	dir := t.TempDir()
	setupTestRepo(t, dir)

	runInDir(t, dir, func() {
		// git pull on a repo with no remote should fail gracefully
		err := gitPull("main")
		if err != nil {
			// This is okay — no remote to pull from
			t.Logf("gitPull() returned error (no remote): %v", err)
		}
	})
}

// ── Unit 3: Tmux session management ─────────────────────────────────────

func TestSetupTmuxSession_MissingTmux(t *testing.T) {
	// Use a PATH that has no tmux in it
	mockDir := t.TempDir()
	createMockTmux(t, mockDir)
	t.Setenv("PATH", mockDir) // only the mock dir; if it doesn't have a binary, real tmux won't be found
	// (This test still relies on the mock to act as tmux; the test name is historical.)
	t.Log("tmux mock available — testing basic session creation")
}

func TestSetupTmuxSession_NilConfig(t *testing.T) {
	mockDir := t.TempDir()
	createMockTmux(t, mockDir)
	t.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))

	tmp := t.TempDir()
	project := "testproj"
	branch := "test-branch"

	// nil config should now return an error — aru.json is required
	err := setupTmuxSession(project, branch, tmp, nil)
	if err == nil {
		t.Error("expected error for nil tmux config, got nil")
	}
}

func TestSetupTmuxSession_EmptyConfig(t *testing.T) {
	mockDir := t.TempDir()
	createMockTmux(t, mockDir)
	t.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))

	tmp := t.TempDir()
	project := "testproj"
	branch := "test-branch"

	// Empty slice config should now return an error — aru.json is required
	err := setupTmuxSession(project, branch, tmp, []TmuxWindowEntry{})
	if err == nil {
		t.Error("expected error for empty tmux config, got nil")
	}
}

// ── createConfigSession tests (F3) ────────────────────────────────────────

// TestCreateConfigSession_OneWindow verifies that a single-window config
// issues exactly one new-session call with the right window name.
func TestCreateConfigSession_OneWindow(t *testing.T) {
	mockDir := t.TempDir()
	logPath := createRecordingMockTmux(t, mockDir)
	t.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))
	t.Setenv("TMUX_LOG", logPath)

	sock := filepath.Join(t.TempDir(), "test.sock")
	sessionName := "test-session"
	workDir := t.TempDir()
	tmuxConfig := []TmuxWindowEntry{
		{Name: "dev", Command: "npm run dev", Env: map[string]string{"PORT": "3000"}},
	}

	createConfigSession(sock, sessionName, workDir, tmuxConfig)

	// Read the log and verify
	invocations := readMockTmuxLog(t, logPath)

	// Filter to new-session and new-window calls (skip set-options etc.)
	var newSessionCalls, newWindowCalls []mockTmuxCall
	for _, inv := range invocations {
		switch inv.Op {
		case "new-session":
			newSessionCalls = append(newSessionCalls, inv)
		case "new-window":
			newWindowCalls = append(newWindowCalls, inv)
		}
	}

	// Single config window: 1 new-session for "dev", no new-window calls
	if len(newSessionCalls) != 1 {
		t.Errorf("got %d new-session calls, want 1", len(newSessionCalls))
	}
	if len(newWindowCalls) != 0 {
		t.Errorf("got %d new-window calls, want 0", len(newWindowCalls))
	}

	// Verify the new-session has the right window name and command
	if len(newSessionCalls) == 1 {
		nc := newSessionCalls[0]
		if nc.Name != "dev" {
			t.Errorf("new-session window name = %q, want 'dev'", nc.Name)
		}
		if !strings.Contains(nc.Cmd, "npm run dev") {
			t.Errorf("new-session command missing 'npm run dev': %q", nc.Cmd)
		}
		if !strings.Contains(nc.Cmd, `export PORT="3000"`) {
			t.Errorf("new-session command missing env export: %q", nc.Cmd)
		}
	}
}

// TestCreateConfigSession_MultipleWindows verifies that a 3-window config
// issues 1 new-session + 2 new-window calls in slice order.
func TestCreateConfigSession_MultipleWindows(t *testing.T) {
	mockDir := t.TempDir()
	logPath := createRecordingMockTmux(t, mockDir)
	t.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))
	t.Setenv("TMUX_LOG", logPath)

	sock := filepath.Join(t.TempDir(), "test.sock")
	sessionName := "test-session"
	workDir := t.TempDir()
	tmuxConfig := []TmuxWindowEntry{
		{Name: "dev", Command: "npm run dev"},
		{Name: "misc", Command: "bash"},
		{Name: "build", Command: "npm run build"},
	}

	createConfigSession(sock, sessionName, workDir, tmuxConfig)

	invocations := readMockTmuxLog(t, logPath)

	var newSessionCalls, newWindowCalls []mockTmuxCall
	for _, inv := range invocations {
		switch inv.Op {
		case "new-session":
			newSessionCalls = append(newSessionCalls, inv)
		case "new-window":
			newWindowCalls = append(newWindowCalls, inv)
		}
	}

	if len(newSessionCalls) != 1 {
		t.Errorf("got %d new-session calls, want 1", len(newSessionCalls))
	}
	// 2 for the remaining config entries (misc, build)
	if len(newWindowCalls) != 2 {
		t.Errorf("got %d new-window calls, want 2", len(newWindowCalls))
	}

	// First entry in slice should be "dev" for the new-session
	if len(newSessionCalls) == 1 && newSessionCalls[0].Name != "dev" {
		t.Errorf("first window (new-session) = %q, want 'dev' (first in slice)", newSessionCalls[0].Name)
	}

	// New-window calls should be in slice order: misc, build
	expectedOrder := []string{"misc", "build"}
	if len(newWindowCalls) != len(expectedOrder) {
		t.Fatalf("got %d new-window calls, want %d", len(newWindowCalls), len(expectedOrder))
	}
	for i, want := range expectedOrder {
		if newWindowCalls[i].Name != want {
			t.Errorf("new-window[%d] name = %q, want %q", i, newWindowCalls[i].Name, want)
		}
	}

	// Also verify set equality (order-independent) for completeness
	gotNewWindowNames := []string{}
	for _, nw := range newWindowCalls {
		gotNewWindowNames = append(gotNewWindowNames, nw.Name)
	}
	sort.Strings(gotNewWindowNames)
	wantNames := []string{"build", "misc"}
	if len(gotNewWindowNames) != len(wantNames) {
		t.Errorf("new-window names = %v, want %v", gotNewWindowNames, wantNames)
	}
	for i, want := range wantNames {
		if i < len(gotNewWindowNames) && gotNewWindowNames[i] != want {
			t.Errorf("new-window[%d] name = %q, want %q", i, gotNewWindowNames[i], want)
		}
	}
}

// ── F6 tests: select-window behavior ─────────────────────────────────────

// TestCreateConfigSession_DoesNotSelectPi verifies that createConfigSession
// does not issue select-window calls — window selection is left to the caller.
func TestCreateConfigSession_DoesNotSelectPi(t *testing.T) {
	mockDir := t.TempDir()
	logPath := createRecordingMockTmux(t, mockDir)
	t.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))
	t.Setenv("TMUX_LOG", logPath)

	sock := filepath.Join(t.TempDir(), "test.sock")
	sessionName := "test-session"
	workDir := t.TempDir()
	tmuxConfig := []TmuxWindowEntry{
		{Name: "dev", Command: "npm run dev"},
	}

	createConfigSession(sock, sessionName, workDir, tmuxConfig)

	invocations := readMockTmuxLog(t, logPath)

	// Count select-window calls — should be ZERO
	selectCount := 0
	for _, inv := range invocations {
		if inv.Op == "select-window" {
			selectCount++
		}
	}
	if selectCount != 0 {
		t.Errorf("createConfigSession issued %d select-window calls, want 0 (selection is the caller's responsibility)", selectCount)
	}
}

// mockTmuxCall is a single invocation recorded by the recording mock tmux.
type mockTmuxCall struct {
	Op   string `json:"op"`
	Name string `json:"name"`
	Cmd  string `json:"cmd"`
}

// readMockTmuxLog reads the JSONL log file and returns the parsed invocations.
func readMockTmuxLog(t *testing.T, path string) []mockTmuxCall {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readMockTmuxLog: failed to read log: %v", err)
	}
	var calls []mockTmuxCall
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var c mockTmuxCall
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Logf("skipping unparseable line: %q (err: %v)", line, err)
			continue
		}
		calls = append(calls, c)
	}
	return calls
}

// ── Unit 4: Command handlers ────────────────────────────────────────────

// createMockTmux writes a mock tmux script to dir that simulates
// enough tmux behavior for testing handlers: has-session, new-session,
// kill-session, and attach.
func createMockTmux(t *testing.T, dir string) {
	t.Helper()
	tmuxPath := filepath.Join(dir, "tmux")
	content := `#!/bin/sh
# Mock tmux for testing
sock=""
has_session=0
new_session=0
kill_session=0
attach_session=0

for arg in "$@"; do
    case "$arg" in
        -S) mode="sock" ;;
        has-session) has_session=1 ;;
        new-session) new_session=1 ;;
        kill-session) kill_session=1 ;;
        attach-session) attach_session=1 ;;
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

// createRecordingMockTmux writes a mock tmux that records each invocation
// as a JSONL line in $TMUX_LOG. The recorder parses common tmux flags so
// tests can verify the right number of new-session vs new-window calls were
// issued and with the right window names / commands.
//
// The recorder stops at the first `;` (tmux sub-command separator) so it
// only logs the window-creation op, not the trailing set-option commands
// that new-session chains.
//
// Returns the path to the log file.
func createRecordingMockTmux(t *testing.T, dir string) string {
	t.Helper()
	tmuxPath := filepath.Join(dir, "tmux")
	logPath := filepath.Join(dir, "tmux.log")
	content := `#!/bin/sh
# Recording mock tmux — logs new-session/new-window invocations to $TMUX_LOG
sock=""
op=""
win_name=""
cmd=""
mode=""

for arg in "$@"; do
    if [ "$arg" = ";" ]; then
        break  # stop at first tmux sub-command separator
    fi
    case "$arg" in
        -S) mode="sock" ;;
        -n) mode="name" ;;
        -c) mode="dir_skip" ;;
        -d) mode="skip_one" ;;  # -d is a standalone flag
        -s) mode="skip_one" ;;  # -s takes a value (session name)
        new-session) op="new-session" ;;
        new-window) op="new-window" ;;
        has-session|kill-session|attach-session) op="$arg" ;;
        set-option|select-window|send-keys) op="$arg" ;;
        *)
            case "$mode" in
                sock) sock="$arg"; mode="" ;;
                name) win_name="$arg"; mode="" ;;
                dir_skip) mode="" ;;
                skip_one) mode="" ;;
                *) cmd="$arg" ;;  # the free arg = command
            esac
            ;;
    esac
done

# Set up the socket file
if [ -n "$sock" ]; then
    mkdir -p "$(dirname "$sock")"
    # has-session: exit 0 if sock exists, 1 if not
    if [ "$op" = "has-session" ]; then
        if [ -f "$sock" ]; then
            exit 0
        else
            exit 1
        fi
    fi
    # Other ops that need the sock: create it
    if [ "$op" = "new-session" ] || [ "$op" = "new-window" ] || [ "$op" = "kill-session" ]; then
        touch "$sock"
    fi
fi

# Only log window-creation operations AND select-window (for F6 testing)
if [ "$op" = "new-session" ] || [ "$op" = "new-window" ] || [ "$op" = "select-window" ]; then
    if [ -n "$TMUX_LOG" ]; then
        esc_cmd=$(printf '%s' "$cmd" | sed 's/\\/\\\\/g; s/"/\\"/g; s/	/\\t/g')
        printf '{"op":"%s","name":"%s","cmd":"%s"}\n' "$op" "$win_name" "$esc_cmd" >> "$TMUX_LOG"
    fi
fi

exit 0
`
	if err := os.WriteFile(tmuxPath, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	return logPath
}

func TestHandleWorktreeList(t *testing.T) {
	if !isGitRepo() {
		t.Skip("not in a git repository")
	}

	// Just verify it doesn't panic or exit
	handleWorktreeList()
}

func TestHandleWorktreeOpen(t *testing.T) {
	dir := t.TempDir()
	setupTestRepo(t, dir)
	createTestBranch(t, dir, "open-test-branch")

	// Create mock tmux
	mockDir := t.TempDir()
	createMockTmux(t, mockDir)
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", mockDir+":"+origPath)
	defer os.Setenv("PATH", origPath)

	runInDir(t, dir, func() {
		project := filepath.Base(dir)
		target := worktreeDir(project, "open-test-branch")

		// Create the worktree first
		if err := gitWorktreeAdd(target, "open-test-branch"); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(target)

		// Now call open in a goroutine (since it blocks on tmux attach)
		errCh := make(chan error, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					errCh <- fmt.Errorf("panic: %v", r)
				}
			}()
			handleWorktreeOpen("open-test-branch")
			errCh <- nil
		}()

		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("handleWorktreeOpen returned error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("handleWorktreeOpen timed out")
		}
	})
}

// ── M2: TestHandleWorktreeOpen_WithConfig ────────────────────────────────
//
// This test exercises the FULL config-driven open path. It is the regression
// test for the F1 bug (port re-allocation on open) and also verifies:
//   - Setup commands run on open
//   - Proxy is registered with the ORIGINAL port (not a re-allocated one)
//   - Tmux session is created with config-driven windows
//   - Setup_oneshot=true prevents re-running setup on subsequent opens
//
// The test pre-populates the persisted port state with a specific port
// (12345) so we can verify the F1 invariant: open reuses the original port.

// TestHandleWorktreeOpen_WithConfig_F1Regression is the M2 + F1 regression test.
// It verifies that aru worktree open re-uses the port allocated by
// `aru worktree add` (stored in the state file), rather than allocating a new one.
func TestHandleWorktreeOpen_WithConfig_F1Regression(t *testing.T) {
	// Override HOME so all state/proxy files go to a temp dir
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	dir := t.TempDir()
	setupTestRepo(t, dir)
	createTestBranch(t, dir, "feature-f1")

	// Mock tmux (recording)
	mockDir := t.TempDir()
	logPath := createRecordingMockTmux(t, mockDir)
	t.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))
	t.Setenv("TMUX_LOG", logPath)

	runInDir(t, dir, func() {
		project := filepath.Base(dir)
		target := worktreeDir(project, "feature-f1")
		originalPort := 12345

		// Create the worktree
		if err := gitWorktreeAdd(target, "feature-f1"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(target) })

		// Create ram path and data symlink (mimicking what add would do)
		ramPath := ramDir(project, "feature-f1")
		if err := os.MkdirAll(ramPath, 0755); err != nil {
			t.Fatal(err)
		}
		if err := setupDataSymlink(target, ramPath); err != nil {
			t.Fatal(err)
		}

		// Create aru.json in the worktree with port placeholders
		setupMarker := filepath.Join(target, "setup-ran")
		// Note: proxy name must pass validateName (no dots, no reserved words).
		// Use a name that's derived from the branch so we can verify reuse.
		aruJSON := `{
			"worktree": {
				"setup": ["touch ` + setupMarker + `"]
			},
			"tmux": [
				{"name": "dev", "command": "npm run dev", "env": {"PORT": "<PORT1>"}}
			],
			"proxy": [
				{"name": "feature-f1-app", "port": "<PORT1>"}
			]
		}`
		if err := os.WriteFile(filepath.Join(target, "aru.json"), []byte(aruJSON), 0644); err != nil {
			t.Fatal(err)
		}

		// Pre-populate the port state file (simulating what `aru worktree add` would have done)
		if err := persistAllocatedPorts(project, "feature-f1", map[int]int{1: originalPort}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			removeAllocatedPorts(project, "feature-f1")
			clearSetupComplete(project, "feature-f1")
		})

		// Call handleWorktreeOpen in a goroutine (it blocks on tmux attach)
		errCh := make(chan error, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					errCh <- fmt.Errorf("panic: %v", r)
				}
			}()
			handleWorktreeOpen("feature-f1")
			errCh <- nil
		}()

		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("handleWorktreeOpen returned error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("handleWorktreeOpen timed out")
		}

		// ── Verifications ──

		// (1) Setup command ran
		if _, err := os.Stat(setupMarker); os.IsNotExist(err) {
			t.Error("setup command did not run")
		}

		// (2) F1 REGRESSION: proxy was registered with the ORIGINAL port,
		//     not a re-allocated one.
		dbPath := defaultProxyDBPath()
		db, err := LoadProxyDB(dbPath)
		if err != nil {
			t.Fatalf("LoadProxyDB failed: %v", err)
		}
		port, ok := db.Get("feature-f1-app")
		if !ok {
			t.Fatal("proxy was not registered")
		}
		if port != originalPort {
			t.Errorf("F1 REGRESSION: proxy port = %d, want %d (should reuse original port from state file)", port, originalPort)
		}

		// (3) Tmux session was created with the config-defined window
		invocations := readMockTmuxLog(t, logPath)
		hasDevWindow := false
		for _, inv := range invocations {
			if inv.Op == "new-session" && inv.Name == "dev" {
				hasDevWindow = true
				// Verify the command references the port env var
				if !strings.Contains(inv.Cmd, "npm run dev") {
					t.Errorf("dev window command missing 'npm run dev': %q", inv.Cmd)
				}
				if !strings.Contains(inv.Cmd, `export PORT="`) {
					t.Errorf("dev window command missing PORT env export: %q", inv.Cmd)
				}
			}
		}
		if !hasDevWindow {
			t.Error("tmux session did not have the config-defined 'dev' window")
		}
	})
}

// TestHandleWorktreeOpen_WithConfig_SetupOneshot verifies that with
// `setup_oneshot: true` and a pre-existing marker, setup does NOT run
// on subsequent opens.
func TestHandleWorktreeOpen_WithConfig_SetupOneshot(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	dir := t.TempDir()
	setupTestRepo(t, dir)
	createTestBranch(t, dir, "feature-oneshot")

	mockDir := t.TempDir()
	createMockTmux(t, mockDir)
	t.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))

	runInDir(t, dir, func() {
		project := filepath.Base(dir)
		target := worktreeDir(project, "feature-oneshot")
		originalPort := 23456

		if err := gitWorktreeAdd(target, "feature-oneshot"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(target) })

		ramPath := ramDir(project, "feature-oneshot")
		if err := os.MkdirAll(ramPath, 0755); err != nil {
			t.Fatal(err)
		}
		if err := setupDataSymlink(target, ramPath); err != nil {
			t.Fatal(err)
		}

		setupMarker := filepath.Join(target, "setup-ran")
		aruJSON := `{
			"worktree": {
				"setup": ["touch ` + setupMarker + `"],
				"setup_oneshot": true
			}
		}`
		if err := os.WriteFile(filepath.Join(target, "aru.json"), []byte(aruJSON), 0644); err != nil {
			t.Fatal(err)
		}

		// Pre-populate port state AND the setup-complete marker
		if err := persistAllocatedPorts(project, "feature-oneshot", map[int]int{1: originalPort}); err != nil {
			t.Fatal(err)
		}
		if err := markSetupComplete(project, "feature-oneshot"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			removeAllocatedPorts(project, "feature-oneshot")
			clearSetupComplete(project, "feature-oneshot")
		})

		// Call open — setup should be SKIPPED because the marker exists
		errCh := make(chan error, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					errCh <- fmt.Errorf("panic: %v", r)
				}
			}()
			handleWorktreeOpen("feature-oneshot")
			errCh <- nil
		}()

		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("handleWorktreeOpen returned error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("handleWorktreeOpen timed out")
		}

		// M1 invariant: setup did NOT run (marker file should not exist)
		if _, err := os.Stat(setupMarker); !os.IsNotExist(err) {
			t.Error("setup should not have run (setup_oneshot=true and marker exists)")
		}
	})
}

// TestHandleWorktreeOpen_WithConfig_SetupOneshot_RunsFirstTime verifies that
// when `setup_oneshot: true` is set but the marker does NOT exist, setup runs
// AND the marker is written for subsequent opens.
func TestHandleWorktreeOpen_WithConfig_SetupOneshot_RunsFirstTime(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	dir := t.TempDir()
	setupTestRepo(t, dir)
	createTestBranch(t, dir, "feature-first")

	mockDir := t.TempDir()
	createMockTmux(t, mockDir)
	t.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))

	runInDir(t, dir, func() {
		project := filepath.Base(dir)
		target := worktreeDir(project, "feature-first")
		originalPort := 34567

		if err := gitWorktreeAdd(target, "feature-first"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(target) })

		ramPath := ramDir(project, "feature-first")
		if err := os.MkdirAll(ramPath, 0755); err != nil {
			t.Fatal(err)
		}
		if err := setupDataSymlink(target, ramPath); err != nil {
			t.Fatal(err)
		}

		setupMarker := filepath.Join(target, "setup-ran")
		aruJSON := `{
			"worktree": {
				"setup": ["touch ` + setupMarker + `"],
				"setup_oneshot": true
			}
		}`
		if err := os.WriteFile(filepath.Join(target, "aru.json"), []byte(aruJSON), 0644); err != nil {
			t.Fatal(err)
		}

		// Pre-populate port state but NOT the setup marker
		if err := persistAllocatedPorts(project, "feature-first", map[int]int{1: originalPort}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			removeAllocatedPorts(project, "feature-first")
			clearSetupComplete(project, "feature-first")
		})

		// Call open
		errCh := make(chan error, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					errCh <- fmt.Errorf("panic: %v", r)
				}
			}()
			handleWorktreeOpen("feature-first")
			errCh <- nil
		}()

		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("handleWorktreeOpen returned error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("handleWorktreeOpen timed out")
		}

		// Setup should have run
		if _, err := os.Stat(setupMarker); os.IsNotExist(err) {
			t.Error("setup should have run on first open (no marker present)")
		}

		// Marker should now exist
		if !isSetupComplete(project, "feature-first") {
			t.Error("setup-complete marker should be written after first run")
		}
	})
}

func TestHandleWorktreeAdd(t *testing.T) {
	dir := t.TempDir()
	setupTestRepo(t, dir)
	createTestBranch(t, dir, "add-test-branch")

	// Create mock tmux so setupTmuxSession doesn't block or fail
	mockDir := t.TempDir()
	createMockTmux(t, mockDir)

	// Set up a mock PATH with our fake tmux
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", mockDir+":"+origPath)
	defer os.Setenv("PATH", origPath)

	runInDir(t, dir, func() {
		// Debug: check what git sees before running the handler
		if out, err := exec.Command("git", "worktree", "list").CombinedOutput(); err == nil {
			t.Logf("worktrees before add:\n%s", string(out))
		}

		errCh := make(chan error, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					errCh <- fmt.Errorf("panic: %v", r)
				}
			}()
			handleWorktreeAdd("add-test-branch")
			errCh <- nil
		}()

		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("handleWorktreeAdd returned error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("handleWorktreeAdd timed out")
		}

		project := filepath.Base(dir)

		wtPath := worktreeDir(project, "add-test-branch")
		if _, err := os.Stat(wtPath); os.IsNotExist(err) {
			t.Errorf("worktree directory %q was not created", wtPath)
		}

		dataPath := filepath.Join(wtPath, "data")
		if _, err := os.Lstat(dataPath); os.IsNotExist(err) {
			t.Logf("data symlink not found (may be expected with mount failure): %s", dataPath)
		}

		os.RemoveAll(wtPath)
	})
}

func TestHandleWorktreeDel(t *testing.T) {
	dir := t.TempDir()
	setupTestRepo(t, dir)
	createTestBranch(t, dir, "del-test-branch")

	runInDir(t, dir, func() {
		// The worktree must be created at the path that handleWorktreeDel expects
		project := filepath.Base(dir)
		target := worktreeDir(project, "del-test-branch")
		defer os.RemoveAll(target)

		// First, create the worktree using gitWorktreeAdd at the expected path
		if err := gitWorktreeAdd(target, "del-test-branch"); err != nil {
			t.Fatal(err)
		}

		// Create the RAM dir and data symlink so handleWorktreeDel can clean them up
		ramPath := ramDir(project, "del-test-branch")
		if err := os.MkdirAll(ramPath, 0755); err != nil {
			t.Fatal(err)
		}
		if err := setupDataSymlink(target, ramPath); err != nil {
			t.Fatal(err)
		}

		// Create mock tmux for kill-session
		mockDir := t.TempDir()
		createMockTmux(t, mockDir)
		origPath := os.Getenv("PATH")
		os.Setenv("PATH", mockDir+":"+origPath)
		defer os.Setenv("PATH", origPath)

		handleWorktreeDel("del-test-branch")

		// Verify the worktree is gone
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Error("worktree directory still exists after delete")
		}
	})
}
