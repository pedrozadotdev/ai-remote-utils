package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ── Config types ───────────────────────────────────────────────────────────

// AruConfig represents the aru.json configuration file.
type AruConfig struct {
	Version  int               `json:"version,omitempty"`
	Worktree *WorktreeConfig   `json:"worktree,omitempty"`
	Tmux     []TmuxWindowEntry `json:"tmux,omitempty"`
	RamDir   []RamDirConfig    `json:"ramdir,omitempty"`
	Proxy    []ProxyConfig     `json:"proxy,omitempty"`
}

// WorktreeConfig defines setup and teardown command lists.
type WorktreeConfig struct {
	Setup        []string `json:"setup,omitempty"`
	SetupOneshot bool     `json:"setup_oneshot,omitempty"`
	Teardown     []string `json:"teardown,omitempty"`
}

// TmuxWindowEntry is an ordered tmux window entry with a name field.
type TmuxWindowEntry struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Env     map[string]string `json:"env,omitempty"`
}

// TmuxWindow defines a single tmux window with optional env vars (used internally).
type TmuxWindow struct {
	Command string            `json:"command"`
	Env     map[string]string `json:"env,omitempty"`
}

// RamDirConfig defines a RAM directory entry.
type RamDirConfig struct {
	Path string `json:"path"`
	Size string `json:"size,omitempty"` // e.g. "200M", "1G" — defaults to "200M" when empty
}

// ProxyConfig defines a reverse proxy registration.
type ProxyConfig struct {
	Name string `json:"name"`
	Port string `json:"port"` // e.g. "<PORT1>" or a literal port string
}

// ── Regex ──────────────────────────────────────────────────────────────────

var portPlaceholderRe = regexp.MustCompile(`<PORT(\d+)>`)

// ── Parsing ────────────────────────────────────────────────────────────────

// ParseAruConfig reads and parses aru.json from the given directory.
// If the file does not exist, returns (nil, nil).
// If the file is malformed, returns (nil, error).
func ParseAruConfig(dir string) (*AruConfig, error) {
	path := filepath.Join(dir, "aru.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read aru.json: %w", err)
	}

	var cfg AruConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse aru.json: %w", err)
	}

	return &cfg, nil
}

// ── Placeholder scanning ───────────────────────────────────────────────────

// collectPortPlaceholders finds all unique <PORTn> placeholders in the config
// and returns their numbers as sorted strings (e.g., ["1", "2"]).
//
// Implementation note: we walk the struct fields directly rather than scanning
// marshaled JSON. This avoids re-encoding/decoding round-trips and is robust
// against project/branch names containing JSON metacharacters.
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

	for _, entry := range cfg.Tmux {
		collectPortNumbers(entry.Command, seen)
		for _, v := range entry.Env {
			collectPortNumbers(v, seen)
		}
	}

	for _, p := range cfg.Proxy {
		collectPortNumbers(p.Name, seen)
		collectPortNumbers(p.Port, seen)
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

// collectPortNumbers scans s for <PORTn> placeholders and records each
// unique placeholder number in seen.
func collectPortNumbers(s string, seen map[int]bool) {
	matches := portPlaceholderRe.FindAllStringSubmatch(s, -1)
	for _, m := range matches {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			// Malformed placeholder (non-numeric) — skip. This shouldn't
			// happen since the regex only captures digits, but defensive.
			continue
		}
		seen[n] = true
	}
}

// ── Port allocation ────────────────────────────────────────────────────────

// allocatePorts allocates a port for each placeholder number using findOpenPort.
// Returns a map of placeholder number → port number.
func allocatePorts(placeholderNums []string) map[int]int {
	ports := make(map[int]int, len(placeholderNums))
	for _, s := range placeholderNums {
		num, err := strconv.Atoi(s)
		if err != nil {
			continue
		}
		port, err := findOpenPort()
		if err != nil {
			// Leave unmapped — will remain as <PORTn> literal in commands
			continue
		}
		ports[num] = port
	}
	return ports
}

// ── Placeholder resolution ─────────────────────────────────────────────────

// resolvePlaceholders performs full placeholder resolution: <PROJECT>,
// <BRANCH>, and <PORTn> are all replaced. Ports must be pre-allocated
// and passed via the ports map (placeholder number → port number).
//
// Implementation: we deep-clone the config and walk all string-typed fields
// (Setup/Teardown/Command/Env values/Proxy fields), applying replacements at
// the string level. This is robust against project/branch names containing
// JSON metacharacters (", \, control chars) — the previous marshal-replace-
// unmarshal approach would have corrupted the JSON in those cases.
func resolvePlaceholders(cfg *AruConfig, project, branch string, ports map[int]int) (*AruConfig, error) {
	if cfg == nil {
		return nil, nil
	}

	clone := cloneConfig(cfg)
	applyPlaceholders(clone, project, branch, ports, true)
	return clone, nil
}

// resolveTeardownPlaceholders performs name-only resolution: <PROJECT> and
// <BRANCH> are replaced, but <PORTn> placeholders are left as literals.
func resolveTeardownPlaceholders(cfg *AruConfig, project, branch string) (*AruConfig, error) {
	if cfg == nil {
		return nil, nil
	}

	clone := cloneConfig(cfg)
	applyPlaceholders(clone, project, branch, nil, false)
	return clone, nil
}

// applyPlaceholders walks all string-typed fields in cfg and replaces
// <PROJECT>, <BRANCH>, and (if resolvePorts) <PORTn> placeholders.
// Mutates the passed config in place — caller should pass a clone.
func applyPlaceholders(cfg *AruConfig, project, branch string, ports map[int]int, resolvePorts bool) {
	if cfg.Worktree != nil {
		for i, cmd := range cfg.Worktree.Setup {
			cfg.Worktree.Setup[i] = replaceInString(cmd, project, branch, ports, resolvePorts)
		}
		for i, cmd := range cfg.Worktree.Teardown {
			cfg.Worktree.Teardown[i] = replaceInString(cmd, project, branch, ports, resolvePorts)
		}
	}

	for i, entry := range cfg.Tmux {
		entry.Name = replaceInString(entry.Name, project, branch, ports, resolvePorts)
		entry.Command = replaceInString(entry.Command, project, branch, ports, resolvePorts)
		for k, v := range entry.Env {
			entry.Env[k] = replaceInString(v, project, branch, ports, resolvePorts)
		}
		cfg.Tmux[i] = entry
	}

	for i, p := range cfg.Proxy {
		p.Name = replaceInString(p.Name, project, branch, ports, resolvePorts)
		p.Port = replaceInString(p.Port, project, branch, ports, resolvePorts)
		cfg.Proxy[i] = p
	}
}

// replaceInString replaces <PROJECT>, <BRANCH>, and (optionally) <PORTn>
// placeholders in a single string. The replacement values are treated as
// opaque text — no JSON escaping is needed because we never re-serialize
// the result.
func replaceInString(s, project, branch string, ports map[int]int, resolvePorts bool) string {
	if project != "" {
		s = strings.ReplaceAll(s, "<PROJECT>", project)
	}
	if branch != "" {
		s = strings.ReplaceAll(s, "<BRANCH>", branch)
	}
	if resolvePorts && len(ports) > 0 {
		// Sort port numbers for deterministic output
		nums := make([]int, 0, len(ports))
		for n := range ports {
			nums = append(nums, n)
		}
		sort.Ints(nums)
		for _, n := range nums {
			placeholder := fmt.Sprintf("<PORT%d>", n)
			s = strings.ReplaceAll(s, placeholder, strconv.Itoa(ports[n]))
		}
	}
	return s
}

// cloneConfig returns a deep copy of cfg so that placeholder resolution
// does not mutate the caller's struct.
func cloneConfig(cfg *AruConfig) *AruConfig {
	if cfg == nil {
		return nil
	}
	clone := &AruConfig{
		Version: cfg.Version,
	}

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
		clone.Tmux = make([]TmuxWindowEntry, len(cfg.Tmux))
		for i, entry := range cfg.Tmux {
			newEntry := TmuxWindowEntry{
				Name:    entry.Name,
				Command: entry.Command,
			}
			if entry.Env != nil {
				newEntry.Env = make(map[string]string, len(entry.Env))
				for k, v := range entry.Env {
					newEntry.Env[k] = v
				}
			}
			clone.Tmux[i] = newEntry
		}
	}

	if cfg.RamDir != nil {
		clone.RamDir = make([]RamDirConfig, len(cfg.RamDir))
		copy(clone.RamDir, cfg.RamDir)
	}

	if cfg.Proxy != nil {
		clone.Proxy = make([]ProxyConfig, len(cfg.Proxy))
		copy(clone.Proxy, cfg.Proxy)
	}

	return clone
}
