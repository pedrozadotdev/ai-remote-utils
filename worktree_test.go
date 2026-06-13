package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

func TestRunDestroyScript(t *testing.T) {
	tmp := t.TempDir()
	scriptPath := filepath.Join(tmp, "wt-destroy.sh")
	marker := filepath.Join(tmp, "destroy-ran")

	script := "#!/usr/bin/env bash\ntouch " + marker + "\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	runDestroyScript(tmp)

	if _, err := os.Stat(marker); os.IsNotExist(err) {
		t.Error("destroy script did not run")
	}
}

func TestRunDestroyScript_SkipWhenAbsent(t *testing.T) {
	tmp := t.TempDir()
	// Should not error when script doesn't exist — just a no-op
	runDestroyScript(tmp)
}

func TestRunSetupScript(t *testing.T) {
	tmp := t.TempDir()
	scriptPath := filepath.Join(tmp, "wt-setup.sh")
	marker := filepath.Join(tmp, "setup-ran")

	script := "#!/usr/bin/env bash\necho \"$PORT\" > " + marker + "\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	runSetupScript(tmp, "8888")

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("setup script did not run: %v", err)
	}

	port := strings.TrimSpace(string(data))
	if port != "8888" {
		t.Errorf("setup script saw PORT=%q, want '8888'", port)
	}
}

func TestRunSetupScript_SkipWhenAbsent(t *testing.T) {
	tmp := t.TempDir()
	// Should not error when script doesn't exist
	runSetupScript(tmp, "8888")
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
	// Should exit with error when tmux is not in PATH
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found in PATH — cannot test tmux behavior")
	}

	// We can't easily test the missing tmux path because it calls os.Exit(1)
	// Just verify the function doesn't crash when tmux IS available
	t.Log("tmux is available — testing basic session creation")
}

func TestSetupTmuxSession_CreateAndKill(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping tmux integration test in short mode")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found in PATH")
	}

	tmp := t.TempDir()
	project := "testproj"
	branch := "test-branch"

	sock := socketPath(project, branch)
	sessionName := sanitizeSessionName(branch)

	t.Cleanup(func() {
		exec.Command("tmux", "-S", sock, "kill-session", "-t", sessionName).Run()
		os.Remove(sock)
	})

	errCh := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				errCh <- fmt.Errorf("panic: %v", r)
			}
		}()
		setupTmuxSession(project, branch, tmp, 0)
		errCh <- nil
	}()

	waitForSocket(sock, sessionName, 5*time.Second)

	check := exec.Command("tmux", "-S", sock, "has-session", "-t", sessionName)
	if out, err := check.CombinedOutput(); err != nil {
		t.Fatalf("session was not created: %v\n%s", err, string(out))
	}

	exec.Command("tmux", "-S", sock, "kill-session", "-t", sessionName).Run()
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
