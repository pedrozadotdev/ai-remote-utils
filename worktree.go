package main

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ── Subcommand dispatch ──────────────────────────────────────────────────────

// handleWorktreeSubcommand dispatches to the appropriate worktree subcommand.
func handleWorktreeSubcommand(args []string) {
	if len(args) == 0 {
		printWorktreeUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "add":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: ai-remote-utils worktree add <branch>\n")
			os.Exit(1)
		}
		handleWorktreeAdd(args[1])
	case "del", "d":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: ai-remote-utils worktree del <branch>\n")
			os.Exit(1)
		}
		handleWorktreeDel(args[1])
	case "open", "o":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: ai-remote-utils worktree open <branch>\n")
			os.Exit(1)
		}
		handleWorktreeOpen(args[1])
	case "list", "l":
		handleWorktreeList()
	case "-h", "--help":
		printWorktreeUsage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "Unknown worktree command: %q\n", args[0])
		printWorktreeUsage()
		os.Exit(1)
	}
}

// printWorktreeUsage prints the worktree subcommand usage.
func printWorktreeUsage() {
	fmt.Fprintf(os.Stderr, `Usage: ai-remote-utils worktree <command> [options]

Commands:
  add <branch>     Create a git worktree at ~/.aru/wt/<project>/<branch> and enter tmux
  del <branch>     Remove the git worktree at ~/.aru/wt/<project>/<branch>
  d    <branch>    Alias for del
  open <branch>    Enter the tmux session for the branch worktree
  o    <branch>    Alias for open
  list             List existing git worktrees
  l                Alias for list
  -h, --help       Show this help text
`)
}

// ── Command availability checks ──────────────────────────────────────────────

// requireCmd checks if a command exists in PATH. Exits with an error if not found.
func requireCmd(name string) {
	if _, err := exec.LookPath(name); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: Required command %q not found in PATH.\n", name)
		os.Exit(1)
	}
}

// requireGitRepo checks if the current directory is inside a git repository.
// Exits with an error if not.
func requireGitRepo() {
	if err := exec.Command("git", "rev-parse", "--git-dir").Run(); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: Current directory is not a git repository.")
		os.Exit(1)
	}
}

// ── Git repository helpers ───────────────────────────────────────────────────

// isMainWorktree checks if we are in the main (non-linked) worktree.
// Returns an error if we are in a linked worktree.
func isMainWorktree() error {
	gitCommonDirB, err := exec.Command("git", "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return fmt.Errorf("worktree: failed to get git common dir: %w", err)
	}

	gitDirB, err := exec.Command("git", "rev-parse", "--git-dir").Output()
	if err != nil {
		return fmt.Errorf("worktree: failed to get git dir: %w", err)
	}

	gitCommonDir := strings.TrimSpace(string(gitCommonDirB))
	gitDir := strings.TrimSpace(string(gitDirB))

	if gitDir != gitCommonDir {
		return fmt.Errorf("worktree: you must run from the main (non-linked) worktree root")
	}
	return nil
}

// getMainWorktreeDir returns the path of the main worktree (first worktree
// listed by "git worktree list --porcelain").
func getMainWorktreeDir() (string, error) {
	out, err := exec.Command("git", "worktree", "list", "--porcelain").Output()
	if err != nil {
		return "", fmt.Errorf("worktree: failed to list worktrees: %w", err)
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "worktree ") {
			return line[len("worktree "):], nil
		}
	}

	return "", fmt.Errorf("worktree: no worktree found in git output")
}

// getCurrentBranch returns the name of the currently checked out branch.
// Returns an error if in detached HEAD state.
func getCurrentBranch() (string, error) {
	out, err := exec.Command("git", "branch", "--show-current").Output()
	if err != nil {
		return "", fmt.Errorf("worktree: failed to get current branch: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// getProjectName derives the project name from the main worktree directory.
func getProjectName() (string, error) {
	mainDir, err := getMainWorktreeDir()
	if err != nil {
		return "", err
	}
	return filepath.Base(mainDir), nil
}

// ── Path builders ────────────────────────────────────────────────────────────

// baseDir returns the base directory for all ai-remote-utils data (~/.aru).
func baseDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".aru"
	}
	return filepath.Join(home, ".aru")
}

// worktreeDir returns the worktree path for the given project and branch.
func worktreeDir(project, branch string) string {
	return filepath.Join(baseDir(), "wt", project, branch)
}

// ramDir returns the RAM data directory path for the given project and branch.
func ramDir(project, branch string) string {
	return filepath.Join(baseDir(), "ram", project, branch)
}

// socketPath returns the tmux socket path for the given project and branch.
func socketPath(project, branch string) string {
	return filepath.Join(baseDir(), "sockets", project+"-"+sanitizeSessionName(branch)+".sock")
}

// ── Session name sanitization ────────────────────────────────────────────────

// sanitizeSessionName converts a branch name to a tmux-safe session name
// by replacing dots, slashes, and underscores with hyphens, and removing
// any other non-alphanumeric characters.
func sanitizeSessionName(name string) string {
	// First pass: replace common separators with hyphens
	result := strings.NewReplacer(
		"/", "-",
		".", "-",
		"_", "-",
	).Replace(name)

	// Second pass: remove any remaining non-alphanumeric/non-hyphen chars
	var cleaned strings.Builder
	for _, r := range result {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' {
			cleaned.WriteRune(r)
		}
	}
	return cleaned.String()
}

// ── Port finding ─────────────────────────────────────────────────────────────

// findOpenPort scans for an available TCP port in the range 1024-9999.
func findOpenPort() (int, error) {
	for port := 1024; port < 10000; port++ {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		listener, err := net.Listen("tcp", addr)
		if err == nil {
			listener.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("worktree: no available port found in range 1024-9999")
}

// ── Git operations ───────────────────────────────────────────────────────

// gitPull runs 'git pull origin <branch>' to sync from remote.
func gitPull(branch string) error {
	slog.Info("pulling latest changes", "branch", branch)
	cmd := exec.Command("git", "pull", "origin", branch)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// gitWorktreeAdd creates a git worktree at target for the given branch.
// If the branch already exists, it checks it out. Otherwise it creates a new branch.
func gitWorktreeAdd(target, branch string) error {
	// Try adding with existing branch first
	cmd := exec.Command("git", "worktree", "add", target, branch)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}

	// Log the failure before trying -b
	slog.Debug("git worktree add failed (may try -b)", "error", err, "output", string(out))

	// Branch doesn't exist — create a new one
	slog.Info("branch does not exist locally, creating new branch", "branch", branch)
	cmd = exec.Command("git", "worktree", "add", "-b", branch, target)
	return cmd.Run()
}

// gitWorktreeRemove removes a git worktree at the given target path.
func gitWorktreeRemove(target string) error {
	cmd := exec.Command("git", "worktree", "remove", "--force", target)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("worktree: git worktree remove %s: %w (output: %s)", target, err, string(out))
	}
	return nil
}

// gitDeleteBranch deletes a branch locally.
func gitDeleteBranch(branch string) error {
	cmd := exec.Command("git", "branch", "-D", branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("worktree: git branch -D %s: %w (output: %s)", branch, err, string(out))
	}
	return nil
}

// gitPrune prunes stale git worktree administrative files.
func gitPrune() {
	cmd := exec.Command("git", "worktree", "prune")
	cmd.Run()
}

// ── RAM directory management ─────────────────────────────────────────────

// mountRamDir creates a directory and mounts tmpfs at the given path.
// If tmpfs mount fails (e.g. not root), it falls back to a regular directory.
func mountRamDir(path string) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("worktree: failed to create RAM directory %s: %w", path, err)
	}

	if err := syscall.Mount("tmpfs", path, "tmpfs", 0, "size=200m"); err != nil {
		slog.Warn("failed to mount tmpfs, using regular directory", "path", path, "error", err)
		// The directory exists already — fallback to regular dir
		return nil
	}

	slog.Info("tmpfs mounted", "path", path, "size", "200m")
	return nil
}

// unmountRamDir unmounts tmpfs at the given path (if mounted) and removes the directory.
func unmountRamDir(path string) error {
	// Try to unmount first (ignore error if not mounted)
	syscall.Unmount(path, 0)

	// Remove the directory and all contents
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("worktree: failed to remove RAM directory %s: %w", path, err)
	}
	return nil
}

// setupDataSymlink creates a symlink at <target>/data pointing to ramPath.
// If the symlink already exists, it is replaced.
func setupDataSymlink(target, ramPath string) error {
	linkPath := filepath.Join(target, "data")

	// Remove existing symlink or directory
	if err := os.RemoveAll(linkPath); err != nil {
		return fmt.Errorf("worktree: failed to remove existing data path %s: %w", linkPath, err)
	}

	if err := os.Symlink(ramPath, linkPath); err != nil {
		return fmt.Errorf("worktree: failed to create data symlink: %w", err)
	}

	return nil
}

// removeDataSymlink removes the data symlink at the worktree target.
func removeDataSymlink(target string) error {
	linkPath := filepath.Join(target, "data")

	// Check if it's a symlink
	if fi, err := os.Lstat(linkPath); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(linkPath); err != nil {
			return fmt.Errorf("worktree: failed to remove symlink %s: %w", linkPath, err)
		}
	}
	return nil
}

// ── Lifecycle script helpers ────────────────────────────────────────────

// runDestroyScript runs wt-destroy.sh in the given directory if it exists.
func runDestroyScript(target string) {
	scriptPath := filepath.Join(target, "wt-destroy.sh")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return
	}

	slog.Info("running wt-destroy.sh", "dir", target)
	cmd := exec.Command("bash", scriptPath)
	cmd.Dir = target
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		slog.Warn("wt-destroy.sh returned an error", "error", err)
	}
}

// runSetupScript runs wt-setup.sh in the given directory with the PORT env var if it exists.
func runSetupScript(target, port string) {
	scriptPath := filepath.Join(target, "wt-setup.sh")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return
	}

	slog.Info("running wt-setup.sh", "dir", target, "port", port)
	cmd := exec.Command("bash", scriptPath)
	cmd.Dir = target
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "PORT="+port)
	if err := cmd.Run(); err != nil {
		slog.Warn("wt-setup.sh returned an error", "error", err)
	}
}

// ── Tmux session management ─────────────────────────────────────────────

// setupTmuxSession creates or attaches to a tmux session for a worktree.
// If port is 0, the setup window is skipped. Blocks until the user detaches.
func setupTmuxSession(project, branch, worktreeDir string, port int) error {
	requireCmd("tmux")

	sessionName := sanitizeSessionName(branch)
	sock := socketPath(project, branch)

	// Create the sockets directory
	socketDir := filepath.Dir(sock)
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		return fmt.Errorf("worktree: failed to create tmux sockets directory %s: %w", socketDir, err)
	}

	// Clean up stale socket file
	os.Remove(sock)

	// Check if session already exists
	checkCmd := exec.Command("tmux", "-S", sock, "has-session", "-t", sessionName)
	if checkCmd.Run() != nil {
		// Session does not exist — create it
		slog.Info("creating tmux session", "session", sessionName, "dir", worktreeDir)

		// Build tmux new-session command
		args := []string{
			"-S", sock,
			"new-session", "-d", "-s", sessionName, "-n", "misc", "-c", worktreeDir,
			";", "set-option", "-g", "default-terminal", "xterm-256color",
			";", "set-option", "-ag", "terminal-overrides", "xterm-256color:Tc",
			";", "set-option", "-g", "allow-passthrough", "on",
			";", "set-option", "-g", "mouse", "on",
			";", "set-option", "-s", "escape-time", "10",
			";", "set-option", "-g", "default-command", "bash",
			";", "set-option", "-qs", "extended-keys", "off",
			";", "set-option", "-qg", "extended-keys", "off",
		}

		cmd := exec.Command("tmux", args...)
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("worktree: failed to create tmux session: %w", err)
		}

		// Create pi window if pi command is available
		if _, err := exec.LookPath("pi"); err == nil {
			piArgs := []string{
				"-S", sock,
				"new-window", "-n", "pi", "-c", worktreeDir,
				"pi || echo 'pi command not found, falling back to bash'; exec bash",
			}
			exec.Command("tmux", piArgs...).Run()
			exec.Command("tmux", "-S", sock, "select-window", "-t", "pi").Run()
		}

		// Create setup window if port > 0 and wt-setup.sh exists
		if port > 0 {
			setupScript := filepath.Join(worktreeDir, "wt-setup.sh")
			if _, err := os.Stat(setupScript); err == nil {
				slog.Info("found wt-setup.sh, creating setup window", "port", port)
				setupArgs := []string{
					"-S", sock,
					"new-window", "-n", "setup", "-c", worktreeDir,
					fmt.Sprintf("export PORT=%d; bash wt-setup.sh; exec bash", port),
				}
				exec.Command("tmux", setupArgs...).Run()
			}
		}
	}

	// Wait for session to be ready
	waitForSocket(sock, sessionName, 5*time.Second)

	// Attach to the session
	slog.Info("attaching to tmux session", "session", sessionName)
	attachCmd := exec.Command("tmux", "-S", sock, "attach-session", "-t", sessionName)
	attachCmd.Stdin = os.Stdin
	attachCmd.Stdout = os.Stdout
	attachCmd.Stderr = os.Stderr
	if err := attachCmd.Run(); err != nil {
		return fmt.Errorf("worktree: failed to attach tmux session: %w", err)
	}

	return nil
}

// waitForSocket polls until tmux reports the session is ready.
func waitForSocket(sock, sessionName string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cmd := exec.Command("tmux", "-S", sock, "has-session", "-t", sessionName)
		if cmd.Run() == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// ── Command handlers ────────────────────────────────────────────────────

// handleWorktreeAdd creates a git worktree, mounts tmpfs RAM directory,
// creates the data symlink, and opens a tmux session.
func handleWorktreeAdd(branch string) {
	requireCmd("git")
	requireGitRepo()

	if err := isMainWorktree(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}

	mainDir, err := getMainWorktreeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}

	projectName := filepath.Base(mainDir)
	target := worktreeDir(projectName, branch)

	// Create parent directory
	parentDir := filepath.Dir(target)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: failed to create parent directory: %v\n", err)
		os.Exit(1)
	}

	// Pull latest changes from the current branch
	currentBranch, err := getCurrentBranch()
	if err == nil && currentBranch != "" {
		if err := gitPull(currentBranch); err != nil {
			slog.Warn("failed to pull latest changes", "error", err)
		}
	} else {
		slog.Warn("could not determine current branch, skipping pull", "error", err)
	}

	// Create the worktree
	slog.Info("creating worktree", "branch", branch, "target", target)
	if err := gitWorktreeAdd(target, branch); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: failed to create worktree: %v\n", err)
		os.Exit(1)
	}

	// Mount RAM directory
	ramPath := ramDir(projectName, branch)
	if err := mountRamDir(ramPath); err != nil {
		slog.Warn("failed to set up RAM directory", "path", ramPath, "error", err)
	}

	// Create data symlink
	if err := setupDataSymlink(target, ramPath); err != nil {
		slog.Warn("failed to create data symlink", "error", err)
	}

	// Find open port for setup script
	port, err := findOpenPort()
	if err != nil {
		slog.Warn("no port available, skipping setup window", "error", err)
		port = 0
	}

	// wt-setup.sh runs inside the tmux setup window (not here)

	// Launch tmux session
	fmt.Printf("\nEntering worktree and starting tmux session...\n")
	if err := setupTmuxSession(projectName, branch, target, port); err != nil {
		slog.Warn("failed to start tmux session, falling back to bash", "error", err)
		fmt.Printf("Falling back to bash in %s\n", target)
		os.Chdir(target)
		bashCmd := exec.Command("bash")
		bashCmd.Stdin = os.Stdin
		bashCmd.Stdout = os.Stdout
		bashCmd.Stderr = os.Stderr
		bashCmd.Run()
	}
}

// handleWorktreeDel removes a git worktree, unmounts RAM, cleans up,
// and deletes the branch.
func handleWorktreeDel(branch string) {
	requireCmd("git")
	requireGitRepo()

	mainDir, err := getMainWorktreeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}

	projectName := filepath.Base(mainDir)
	target := worktreeDir(projectName, branch)

	if _, err := os.Stat(target); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "ERROR: Worktree directory %q not found.\n", target)
		os.Exit(1)
	}

	// Remove data symlink
	if err := removeDataSymlink(target); err != nil {
		slog.Warn("failed to remove data symlink", "error", err)
	}

	// Unmount and remove RAM directory
	ramPath := ramDir(projectName, branch)
	if err := unmountRamDir(ramPath); err != nil {
		slog.Warn("failed to clean up RAM directory", "path", ramPath, "error", err)
	}

	// Run wt-destroy.sh if it exists
	runDestroyScript(target)

	// Kill tmux session
	sock := socketPath(projectName, branch)
	sessionName := sanitizeSessionName(branch)
	killCmd := exec.Command("tmux", "-S", sock, "kill-session", "-t", sessionName)
	killCmd.Run() // Ignore error if session doesn't exist

	// Remove worktree
	slog.Info("removing worktree", "target", target)
	if err := gitWorktreeRemove(target); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: failed to remove worktree: %v\n", err)
		os.Exit(1)
	}

	// Clean up socket file
	os.Remove(sock)

	// Delete branch (best-effort)
	if err := gitDeleteBranch(branch); err != nil {
		slog.Warn("failed to delete branch", "branch", branch, "error", err)
	}

	// Prune stale administrative files
	gitPrune()

	fmt.Printf("Worktree %q removed.\n", branch)
}

// handleWorktreeOpen re-creates the RAM directory and symlink if missing,
// then attaches a tmux session for the worktree.
func handleWorktreeOpen(branch string) {
	requireGitRepo()

	mainDir, err := getMainWorktreeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}

	projectName := filepath.Base(mainDir)
	target := worktreeDir(projectName, branch)

	if _, err := os.Stat(target); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "ERROR: Worktree directory %q not found.\n", target)
		os.Exit(1)
	}

	// Re-create RAM dir and symlink if missing (handles reboot)
	ramPath := ramDir(projectName, branch)
	if _, err := os.Stat(ramPath); os.IsNotExist(err) {
		slog.Info("RAM directory missing, re-creating", "path", ramPath)
		if err := mountRamDir(ramPath); err != nil {
			slog.Warn("failed to re-create RAM directory", "error", err)
		}
	}

	dataPath := filepath.Join(target, "data")
	if _, err := os.Lstat(dataPath); os.IsNotExist(err) {
		slog.Info("data symlink missing, re-creating")
		if err := setupDataSymlink(target, ramPath); err != nil {
			slog.Warn("failed to re-create data symlink", "error", err)
		}
	}

	// Attach tmux session (no setup window)
	if err := setupTmuxSession(projectName, branch, target, 0); err != nil {
		slog.Warn("failed to start tmux session, falling back to bash", "error", err)
		fmt.Printf("Falling back to bash in %s\n", target)
		os.Chdir(target)
		bashCmd := exec.Command("bash")
		bashCmd.Stdin = os.Stdin
		bashCmd.Stdout = os.Stdout
		bashCmd.Stderr = os.Stderr
		bashCmd.Run()
	}
}

// handleWorktreeList lists git worktrees with a marker for the current directory.
func handleWorktreeList() {
	requireCmd("git")
	requireGitRepo()

	current, err := os.Getwd()
	if err != nil {
		current = "."
	}
	// Resolve symlinks for accurate comparison
	current, err = filepath.EvalSymlinks(current)
	if err != nil {
		current = "."
	}

	out, err := exec.Command("git", "worktree", "list").Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: failed to list worktrees: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Git worktrees:")
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		wtPath := parts[0]

		resolved, err := filepath.EvalSymlinks(wtPath)
		if err != nil {
			resolved = wtPath
		}

		if resolved == current || strings.HasPrefix(current, resolved+"/") {
			fmt.Printf("  %s  <-- current\n", line)
		} else {
			fmt.Printf("  %s\n", line)
		}
	}
}
