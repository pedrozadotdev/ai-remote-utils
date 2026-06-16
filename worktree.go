package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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
			fmt.Fprintf(os.Stderr, "Usage: aru worktree add <branch>\n")
			os.Exit(1)
		}
		handleWorktreeAdd(args[1])
	case "del", "d":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: aru worktree del <branch>\n")
			os.Exit(1)
		}
		handleWorktreeDel(args[1])
	case "open", "o":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: aru worktree open <branch>\n")
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
	fmt.Fprintf(os.Stderr, `Usage: aru worktree <command> [options]

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

// baseDir returns the base directory for all aru data (~/.aru).
func baseDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".aru"
	}
	return filepath.Join(home, ".aru")
}

// stateDir returns the per-worktree state directory (~/.aru/state/<project>/<branch>).
// Used for persisting runtime data (e.g., allocated ports) so it survives reboots.
func stateDir(project, branch string) string {
	return filepath.Join(baseDir(), "state", project, branch)
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

// ── RAM directory entry helpers ──────────────────────────────────────────

// ramDirSubPath returns the per-entry RAM directory path:
// ~/.aru/wt/<project>/<branch>/<entryPath>.
func ramDirSubPath(project, branch, entryPath string) string {
	return filepath.Join(worktreeDir(project, branch), entryPath)
}

// mountRamDirEntry creates and mounts a RAM directory for a single ramdir entry.
// It creates the directory (inside the worktree at the configured path) and
// mounts tmpfs directly — no symlinks involved.
func mountRamDirEntry(project, branch string, entry RamDirConfig, target string) error {
	subPath := ramDirSubPath(project, branch, entry.Path)
	if err := os.MkdirAll(subPath, 0755); err != nil {
		return fmt.Errorf("worktree: failed to create ramdir directory %s: %w", subPath, err)
	}
	if err := mountRamDir(subPath, entry.Size); err != nil {
		return fmt.Errorf("worktree: failed to mount ramdir entry %s: %w", subPath, err)
	}
	return nil
}

// unmountRamDirEntry unmounts tmpfs and removes the directory for a single
// ramdir entry. Best-effort: continues on partial failure.
func unmountRamDirEntry(project, branch string, entry RamDirConfig, target string) error {
	subPath := ramDirSubPath(project, branch, entry.Path)
	if err := unmountRamDir(subPath); err != nil {
		return fmt.Errorf("worktree: failed to clean up ramdir entry %s: %w", entry.Path, err)
	}
	return nil
}

// ── RAM directory management ─────────────────────────────────────────────

// mountRamDir creates a directory and mounts tmpfs at the given path.
// If tmpfs mount fails (e.g. not root), it falls back to a regular directory.
// size accepts a tmpfs size specifier (e.g. "200M", "1G"). If empty, defaults to "200M".
func mountRamDir(path, size string) error {
	if size == "" {
		size = "200M"
	}

	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("worktree: failed to create RAM directory %s: %w", path, err)
	}

	mountOpts := "size=" + size
	if err := syscall.Mount("tmpfs", path, "tmpfs", 0, mountOpts); err != nil {
		slog.Warn("failed to mount tmpfs, using regular directory", "path", path, "error", err, "size", size)
		// The directory exists already — fallback to regular dir
		return nil
	}

	slog.Info("tmpfs mounted", "path", path, "size", size)
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

// isTmpfs checks if the given path resides on a tmpfs filesystem.
// Uses syscall.Statfs to compare the filesystem type magic number.
func isTmpfs(path string) bool {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return false
	}
	// TMPFS_MAGIC = 0x01021994
	return stat.Type == 0x01021994
}

// ── Config-based lifecycle helpers ───────────────────────────────────────

// TRUST MODEL: All commands in aru.json (setup, teardown, tmux windows) run
// as the user invoking `aru`. The user already has full shell access, so the
// commands are equivalent to typing them in a terminal — no privilege
// escalation occurs. This is the same trust model as Makefile recipes,
// package.json scripts, or Dockerfile RUN instructions.
//
// Implications:
//   - Anyone who can write to aru.json can execute commands as the user.
//   - Review aru.json changes in pull requests with the same care as shell scripts.
//   - If you copy aru.json from an untrusted source, treat it as code execution.
//   - There is no in-process sandbox; commands run with full user permissions.

// runSetupCommands runs each setup command in order with bash -c.
// If a command fails, a warning is logged but execution continues.
//
// Commands are run verbatim from the user's aru.json. See the TRUST MODEL
// comment above for security implications.
func runSetupCommands(target string, commands []string) {
	for _, cmdStr := range commands {
		slog.Info("running setup command", "dir", target, "command", cmdStr)
		cmd := exec.Command("bash", "-c", cmdStr)
		cmd.Dir = target
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			slog.Warn("setup command failed", "command", cmdStr, "error", err)
		}
	}
}

// runTeardownCommands runs each teardown command in order with bash -c.
// If a command fails, a warning is logged but execution continues.
//
// Commands are run verbatim from the user's aru.json. See the TRUST MODEL
// comment above for security implications.
func runTeardownCommands(target string, commands []string) {
	for _, cmdStr := range commands {
		slog.Info("running teardown command", "dir", target, "command", cmdStr)
		cmd := exec.Command("bash", "-c", cmdStr)
		cmd.Dir = target
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			slog.Warn("teardown command failed", "command", cmdStr, "error", err)
		}
	}
}

// runSetupIfNeeded runs setup commands unless setup_oneshot is true and the
// setup-complete marker already exists for this worktree. If setup_oneshot is
// true and the marker is missing, runs setup and writes the marker on success.
//
// Note: runSetupCommands already logs warnings on per-command failure but
// continues. The marker is written after the loop runs (we don't track per-
// command success — the trust model is "if user ran the setup, it succeeded
// from their perspective"). To force re-run, delete the marker file.
func runSetupIfNeeded(project, branch, target string, resolved *AruConfig) {
	if resolved == nil || resolved.Worktree == nil || len(resolved.Worktree.Setup) == 0 {
		return
	}

	if resolved.Worktree.SetupOneshot && isSetupComplete(project, branch) {
		slog.Info("setup already complete, skipping (setup_oneshot=true)", "project", project, "branch", branch)
		return
	}

	runSetupCommands(target, resolved.Worktree.Setup)

	if resolved.Worktree.SetupOneshot {
		if err := markSetupComplete(project, branch); err != nil {
			slog.Warn("failed to mark setup complete, future opens may re-run", "error", err)
		}
	}
}

// buildTmuxCommand constructs a tmux command string from a TmuxWindow config.
// Format: trap ':' INT; export K1=V1; export K2=V2; command; exec bash
//
// SECURITY: Env values are shell-escaped with strconv.Quote to prevent
// injection via crafted env values (e.g., a value containing `; rm -rf /`).
// The `command` field is NOT escaped — by design, per the TRUST MODEL: aru.json
// is treated as trusted code, equivalent to Makefile recipes or npm scripts.
// The user already has full shell access, so sandboxing commands would
// provide no additional security. Review aru.json changes with the same
// care as shell scripts.
func buildTmuxCommand(win TmuxWindow) string {
	var parts []string

	// Install a no-op SIGINT handler so the shell survives Ctrl+C on the
	// command and continues to exec bash (fallback shell). Child processes
	// get the default disposition (SIG_DFL), so the command is still
	// interruptible — only the outer shell has the handler.
	parts = append(parts, "trap ':' INT")

	// Sort env keys for deterministic output
	if len(win.Env) > 0 {
		keys := make([]string, 0, len(win.Env))
		for k := range win.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("export %s=%s", k, strconv.Quote(win.Env[k])))
		}
	}

	if win.Command != "" {
		parts = append(parts, win.Command)
	}

	parts = append(parts, "exec bash")
	return strings.Join(parts, "; ")
}

// ── Port state persistence ───────────────────────────────────────────

// portsStatePath returns the path to the persisted port assignments file.
func portsStatePath(project, branch string) string {
	return filepath.Join(stateDir(project, branch), "ports.json")
}

// portsStateFile is the JSON schema for the persisted port assignments.
// Keys are placeholder numbers as strings (e.g., "1", "2") so the file
// is human-readable and stable across Go map iteration order.
type portsStateFile struct {
	Ports map[string]int `json:"ports"`
}

// persistAllocatedPorts saves the port assignments to the state file so
// subsequent `aru worktree open` calls can re-use the same ports.
// Returns nil (no-op) if the ports map is empty.
func persistAllocatedPorts(project, branch string, ports map[int]int) error {
	if len(ports) == 0 {
		return nil
	}
	path := portsStatePath(project, branch)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("worktree: failed to create state directory: %w", err)
	}

	state := portsStateFile{Ports: make(map[string]int, len(ports))}
	for num, port := range ports {
		state.Ports[strconv.Itoa(num)] = port
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("worktree: failed to marshal ports state: %w", err)
	}
	// 0600 — not secret, but per the principle of least privilege for state files
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("worktree: failed to write ports state file: %w", err)
	}
	return nil
}

// loadAllocatedPorts reads the port assignments from the state file.
// Returns (nil, nil) if the file does not exist (no warning — normal first-time case).
// Returns (nil, error) if the file exists but cannot be parsed.
func loadAllocatedPorts(project, branch string) (map[int]int, error) {
	path := portsStatePath(project, branch)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
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

// removeAllocatedPorts deletes the persisted port assignments file.
// Best-effort: silently ignores "not found" errors.
func removeAllocatedPorts(project, branch string) {
	if err := os.Remove(portsStatePath(project, branch)); err != nil && !os.IsNotExist(err) {
		slog.Debug("failed to remove ports state file", "error", err)
	}
}

// ── Setup idempotency marker ────────────────────────────────────────────
//
// When aru.json sets `setup_oneshot: true`, setup commands run only once per
// worktree session. After successful first-run, a marker file is written;
// subsequent `aru worktree open` calls skip setup if the marker exists.
// The marker is removed when the worktree is deleted.

// setupCompletePath returns the path to the setup-complete marker file.
func setupCompletePath(project, branch string) string {
	return filepath.Join(stateDir(project, branch), "setup-complete")
}

// isSetupComplete reports whether the setup-complete marker exists for the
// given worktree. Returns false on any error (treated as "not complete").
func isSetupComplete(project, branch string) bool {
	if _, err := os.Stat(setupCompletePath(project, branch)); err == nil {
		return true
	}
	return false
}

// markSetupComplete writes the setup-complete marker file. Idempotent.
func markSetupComplete(project, branch string) error {
	path := setupCompletePath(project, branch)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("worktree: failed to create state directory: %w", err)
	}
	// 0600 — not secret, but least-privilege for state files
	if err := os.WriteFile(path, []byte("complete"), 0600); err != nil {
		return fmt.Errorf("worktree: failed to write setup-complete marker: %w", err)
	}
	return nil
}

// clearSetupComplete removes the setup-complete marker (best-effort).
// Used during worktree deletion to clean up state.
func clearSetupComplete(project, branch string) {
	if err := os.Remove(setupCompletePath(project, branch)); err != nil && !os.IsNotExist(err) {
		slog.Debug("failed to remove setup-complete marker", "error", err)
	}
}

// portSource controls how port placeholders are resolved in readConfig.
type portSource int

const (
	portSourceAllocate portSource = iota // allocate fresh ports (for `add`)
	portSourceLoad                       // load from state file (for `open`)
)

// readConfig reads aru.json from the worktree, collects port placeholders,
// and resolves them. Per source:
//   - portSourceAllocate: allocates new ports, persists them to state file
//   - portSourceLoad: loads ports from state file; falls back to allocation
//     (with persistence) if state file is missing
//
// If project or branch is empty, warns and returns nil.
// If aru.json is missing or malformed, warns and returns nil.
func readConfig(target, project, branch string, source portSource) *AruConfig {
	if project == "" || branch == "" {
		slog.Warn("project or branch is empty, skipping config resolution")
		return nil
	}

	cfg, err := ParseAruConfig(target)
	if err != nil {
		slog.Warn("failed to parse aru.json", "error", err)
		return nil
	}
	if cfg == nil {
		slog.Warn("aru.json not found; create one to configure setup commands, tmux windows, and proxy")
		return nil
	}

	placeholderNums := collectPortPlaceholders(cfg)
	ports, warnOnMissing := resolvePortsForSource(project, branch, source, placeholderNums)

	resolved, err := resolvePlaceholders(cfg, project, branch, ports)
	if err != nil {
		slog.Warn("failed to resolve placeholders", "error", err)
		return nil
	}

	if warnOnMissing {
		for _, pn := range placeholderNums {
			var num int
			fmt.Sscanf(pn, "%d", &num)
			if _, ok := ports[num]; !ok {
				slog.Warn("port allocation failed for PORT"+pn+", leaving as literal in commands", "placeholder", "<PORT"+pn+">")
			}
		}
	}

	return resolved
}

// resolvePortsForSource returns the port map to use, based on the portSource.
// When portSourceLoad, it tries to load from state; if missing or empty, falls back
// to fresh allocation (and persists the result so subsequent opens match).
// Returns (ports, warnOnMissing) — warnOnMissing is true if there were placeholders
// the source could not satisfy (so the caller can log a warning).
func resolvePortsForSource(project, branch string, source portSource, placeholderNums []string) (map[int]int, bool) {
	switch source {
	case portSourceAllocate:
		ports := allocatePorts(placeholderNums)
		if err := persistAllocatedPorts(project, branch, ports); err != nil {
			slog.Warn("failed to persist allocated ports", "error", err)
		}
		return ports, true

	case portSourceLoad:
		ports, err := loadAllocatedPorts(project, branch)
		if err != nil {
			slog.Warn("failed to load persisted ports, falling back to fresh allocation", "error", err)
			ports = allocatePorts(placeholderNums)
			if perr := persistAllocatedPorts(project, branch, ports); perr != nil {
				slog.Warn("failed to persist allocated ports", "error", perr)
			}
			return ports, true
		}
		if ports == nil {
			// No state file — first open after add (e.g., state dir was wiped on reboot,
			// or this is a legacy worktree created before persistence was added).
			// Allocate fresh and persist so subsequent opens match.
			slog.Info("no persisted port state, allocating fresh ports", "project", project, "branch", branch)
			ports = allocatePorts(placeholderNums)
			if perr := persistAllocatedPorts(project, branch, ports); perr != nil {
				slog.Warn("failed to persist allocated ports", "error", perr)
			}
			return ports, true
		}
		// Have persisted ports — use them
		return ports, false
	}

	// Unknown source — fall back to allocation
	ports := allocatePorts(placeholderNums)
	return ports, true
}

// readAndResolveConfig is the allocating variant of readConfig, used by `add`.
// Kept as a separate function for backward compatibility and clear call sites.
func readAndResolveConfig(target, project, branch string) *AruConfig {
	return readConfig(target, project, branch, portSourceAllocate)
}

// readAndResolveConfigOpen is the load-from-state variant of readConfig, used by `open`.
// It re-uses ports that were allocated by the original `aru worktree add` call,
// ensuring the proxy and tmux env vars stay consistent across reboots.
func readAndResolveConfigOpen(target, project, branch string) *AruConfig {
	return readConfig(target, project, branch, portSourceLoad)
}

// readTeardownConfig reads aru.json and performs name-only resolution
// (only <PROJECT> and <BRANCH>, no port allocation).
// If project or branch is empty, warns and returns nil.
// If aru.json is missing or malformed, warns and returns nil.
func readTeardownConfig(target, project, branch string) *AruConfig {
	if project == "" || branch == "" {
		slog.Warn("project or branch is empty, skipping teardown config")
		return nil
	}

	cfg, err := ParseAruConfig(target)
	if err != nil {
		slog.Warn("failed to parse aru.json for teardown", "error", err)
		return nil
	}
	if cfg == nil {
		// No aru.json — nothing to teardown
		return nil
	}

	resolved, err := resolveTeardownPlaceholders(cfg, project, branch)
	if err != nil {
		slog.Warn("failed to resolve teardown placeholders", "error", err)
		return nil
	}

	return resolved
}

// setupProxy registers a reverse proxy entry. Loads the proxy DB at dbPath,
// validates the name, and adds the entry. Logs a warning on failure without
// aborting. The dbPath parameter is the path to the proxy DB JSON file — pass
// defaultProxyDBPath() in production, or a temp path in tests.
func setupProxy(dbPath, name string, port int) {
	db, err := LoadProxyDB(dbPath)
	if err != nil {
		slog.Warn("failed to load proxy DB for registration", "error", err)
		return
	}

	if err := validateName(name); err != nil {
		slog.Warn("invalid proxy name after resolution, skipping proxy registration", "name", name, "error", err)
		return
	}

	if err := db.Add(name, port); err != nil {
		slog.Warn("failed to register proxy", "name", name, "port", port, "error", err)
		return
	}

	slog.Info("proxy registered", "name", name, "port", port)
}

// removeProxy removes a reverse proxy entry. Loads the proxy DB at dbPath,
// deletes the entry. If the entry is not found, it is silently ignored
// (debug log only). The dbPath parameter is the path to the proxy DB JSON
// file — pass defaultProxyDBPath() in production, or a temp path in tests.
func removeProxy(dbPath, name string) {
	db, err := LoadProxyDB(dbPath)
	if err != nil {
		slog.Warn("failed to load proxy DB for cleanup", "error", err)
		return
	}

	if err := db.Delete(name); err != nil {
		slog.Debug("proxy cleanup: entry not found or delete failed", "name", name, "error", err)
		return
	}

	slog.Info("proxy removed", "name", name)
}

// ── Tmux session management ─────────────────────────────────────────────

// setupTmuxSession creates or attaches to a tmux session for a worktree.
// tmuxConfig must have at least one entry (defined via aru.json's tmux section).
// The first entry creates the session, subsequent entries create new-windows.
// Blocks until the user detaches.
func setupTmuxSession(project, branch, worktreeDir string, tmuxConfig []TmuxWindowEntry) error {
	requireCmd("tmux")

	sessionName := sanitizeSessionName(branch)
	sock := socketPath(project, branch)

	// Create the sockets directory
	socketDir := filepath.Dir(sock)
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		return fmt.Errorf("worktree: failed to create tmux sockets directory %s: %w", socketDir, err)
	}

	// Check if session already exists
	checkCmd := exec.Command("tmux", "-S", sock, "has-session", "-t", sessionName)
	if checkCmd.Run() != nil {
		// Session does not exist — remove stale socket file if present
		os.Remove(sock)

		slog.Info("creating tmux session", "session", sessionName, "dir", worktreeDir)

		if len(tmuxConfig) == 0 {
			return fmt.Errorf("worktree: aru.json not found in worktree root — create an aru.json with a tmux section to define worktree windows")
		}

		// Config-driven session
		createConfigSession(sock, sessionName, worktreeDir, tmuxConfig)

		// Select the first window (index 0) for new sessions
		exec.Command("tmux", "-S", sock, "select-window", "-t", sessionName+":0").Run()
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

// createConfigSession creates a tmux session with windows defined by the config.
// The first entry in the slice is created via new-session, the rest via new-window.
func createConfigSession(sock, sessionName, worktreeDir string, tmuxConfig []TmuxWindowEntry) {
	for i, entry := range tmuxConfig {
		win := TmuxWindow{Command: entry.Command, Env: entry.Env}
		cmdStr := buildTmuxCommand(win)

		if i == 0 {
			// First window: new-session
			args := buildSessionArgs(sock, sessionName, worktreeDir, entry.Name, cmdStr)
			cmd := exec.Command("tmux", args...)
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				slog.Warn("failed to create tmux session window", "window", entry.Name, "error", err)
			}
		} else {
			// Subsequent windows: new-window
			args := []string{
				"-S", sock,
				"new-window", "-n", entry.Name, "-c", worktreeDir,
				cmdStr,
			}
			cmd := exec.Command("tmux", args...)
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				slog.Warn("failed to create tmux window", "window", entry.Name, "error", err)
			}
		}
	}
}

// buildSessionArgs builds the tmux new-session argument list with common options.
func buildSessionArgs(sock, sessionName, worktreeDir, windowName, cmdStr string) []string {
	return []string{
		"-S", sock,
		"new-session", "-d", "-s", sessionName, "-n", windowName, "-c", worktreeDir,
		cmdStr,
		";", "set-option", "-g", "default-terminal", "xterm-256color",
		";", "set-option", "-ag", "terminal-overrides", "xterm-256color:Tc",
		";", "set-option", "-g", "allow-passthrough", "on",
		";", "set-option", "-g", "mouse", "on",
		";", "set-option", "-s", "escape-time", "10",
		";", "set-option", "-g", "default-command", "bash",
		";", "set-option", "-qs", "extended-keys", "off",
		";", "set-option", "-qg", "extended-keys", "off",
	}
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

	// Read aru.json config and resolve placeholders
	resolved := readAndResolveConfig(target, projectName, branch)

	// Create RAM directory entries from config (if any)
	if resolved != nil {
		for _, entry := range resolved.RamDir {
			if err := mountRamDirEntry(projectName, branch, entry, target); err != nil {
				slog.Warn("failed to set up RAM directory entry", "path", entry.Path, "error", err)
			}
		}
	}

	// Run setup commands if config provides them (respects setup_oneshot)
	runSetupIfNeeded(projectName, branch, target, resolved)

	// Register proxies if configured
	if resolved != nil {
		for _, p := range resolved.Proxy {
			if p.Port == "" {
				continue
			}
			port, err := strconv.Atoi(p.Port)
			if err != nil {
				slog.Warn("invalid proxy port, skipping proxy registration", "port", p.Port, "error", err)
				continue
			}
			setupProxy(defaultProxyDBPath(), p.Name, port)
		}
	}

	// Determine tmux config (nil if no aru.json)
	var tmuxConfig []TmuxWindowEntry
	if resolved != nil {
		tmuxConfig = resolved.Tmux
	}

	// Launch tmux session
	fmt.Printf("\nEntering worktree and starting tmux session...\n")
	if err := setupTmuxSession(projectName, branch, target, tmuxConfig); err != nil {
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

	// Read aru.json for teardown config
	resolved := readTeardownConfig(target, projectName, branch)

	// Run teardown commands if config provides them
	if resolved != nil && resolved.Worktree != nil && len(resolved.Worktree.Teardown) > 0 {
		runTeardownCommands(target, resolved.Worktree.Teardown)
	}

	// Remove proxies if configured
	if resolved != nil {
		for _, p := range resolved.Proxy {
			if p.Name == "" {
				continue
			}
			removeProxy(defaultProxyDBPath(), p.Name)
		}
	}

	// Clean up RAM directory entries (best-effort)
	// Ramdir entries live inside the worktree, which is removed by
	// git worktree remove below — but we unmount tmpfs first so the
	// directory is clean.
	if resolved != nil {
		for _, entry := range resolved.RamDir {
			if err := unmountRamDirEntry(projectName, branch, entry, target); err != nil {
				slog.Warn("failed to clean up ramdir entry", "path", entry.Path, "error", err)
			}
		}
	}

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

	// Remove persisted port assignments
	removeAllocatedPorts(projectName, branch)

	// Remove setup-complete marker (if setup_oneshot was used)
	clearSetupComplete(projectName, branch)

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

	// Read aru.json config; load original port assignments from state so the
	// proxy and tmux env vars stay consistent with the original `aru worktree add`.
	resolved := readAndResolveConfigOpen(target, projectName, branch)

	// Re-create RAM directory entries on every open (handles reboot, etc.).
	// After a reboot the tmpfs is gone, but the mount point directory persists
	// as an empty regular directory. Use isTmpfs (syscall.Statfs) to distinguish:
	//   - tmpfs mount → proper, skip
	//   - not tmpfs (empty leftover dir, or missing) → recreate
	// Safety: if the path is a non-empty regular dir (non-root fallback from
	// a previous run), skip to avoid mounting a fresh empty tmpfs over it.
	if resolved != nil {
		for _, entry := range resolved.RamDir {
			subPath := ramDirSubPath(projectName, branch, entry.Path)

			if isTmpfs(subPath) {
				// Already a proper tmpfs mount — nothing to do
				continue
			}

			// Not a tmpfs. Safety check: if the path is a non-empty regular dir,
			// it may be a fallback from a previous non-root run with user data.
			if fi, err := os.Stat(subPath); err == nil && fi.IsDir() {
				entries, _ := os.ReadDir(subPath)
				if len(entries) > 0 {
					slog.Warn("RAM dir has data but is not tmpfs; skipping to preserve contents", "path", entry.Path)
					continue
				}
			}

			// Missing or empty — recreate
			slog.Info("RAM dir not tmpfs, re-creating", "path", entry.Path)
			os.RemoveAll(subPath) // clean up stale non-tmpfs directory
			if err := mountRamDirEntry(projectName, branch, entry, target); err != nil {
				slog.Warn("failed to re-create RAM directory entry", "path", entry.Path, "error", err)
			}
		}
	}

	// Run setup commands if config provides them (respects setup_oneshot)
	runSetupIfNeeded(projectName, branch, target, resolved)

	// Re-register proxies if configured
	if resolved != nil {
		for _, p := range resolved.Proxy {
			if p.Port == "" {
				continue
			}
			port, err := strconv.Atoi(p.Port)
			if err != nil {
				slog.Warn("invalid proxy port, skipping proxy registration", "port", p.Port, "error", err)
				continue
			}
			setupProxy(defaultProxyDBPath(), p.Name, port)
		}
	}

	// Determine tmux config (nil if no aru.json)
	var tmuxConfig []TmuxWindowEntry
	if resolved != nil {
		tmuxConfig = resolved.Tmux
	}

	// Attach tmux session with config
	if err := setupTmuxSession(projectName, branch, target, tmuxConfig); err != nil {
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
