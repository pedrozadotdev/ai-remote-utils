package main

import (
	"os"
	"path/filepath"
	"testing"
)

// ── ParseAruConfig tests ───────────────────────────────────────────────────

func TestParseAruConfig_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	jsonContent := `{
		"worktree": {
			"setup": ["npm install", "npm run build"],
			"teardown": ["rm -rf /tmp/data"]
		},
		"tmux": {
			"misc": {"command": "bash"},
			"dev": {"command": "npm run dev", "env": {"PORT": "<PORT1>"}}
		},
		"proxy": {
			"name": "myapp.<BRANCH>.<PROJECT>",
			"port": "<PORT1>"
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "aru.json"), []byte(jsonContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := ParseAruConfig(dir)
	if err != nil {
		t.Fatalf("ParseAruConfig() returned error: %v", err)
	}
	if cfg == nil {
		t.Fatal("ParseAruConfig() returned nil")
	}

	// Worktree
	if cfg.Worktree == nil {
		t.Fatal("Worktree is nil")
	}
	if len(cfg.Worktree.Setup) != 2 || cfg.Worktree.Setup[0] != "npm install" {
		t.Errorf("Setup = %v, want [npm install npm run build]", cfg.Worktree.Setup)
	}
	if len(cfg.Worktree.Teardown) != 1 || cfg.Worktree.Teardown[0] != "rm -rf /tmp/data" {
		t.Errorf("Teardown = %v, want [rm -rf /tmp/data]", cfg.Worktree.Teardown)
	}

	// Tmux
	if len(cfg.Tmux) != 2 {
		t.Fatalf("Tmux has %d entries, want 2", len(cfg.Tmux))
	}
	misc, ok := cfg.Tmux["misc"]
	if !ok {
		t.Fatal("Tmux missing 'misc' key")
	}
	if misc.Command != "bash" {
		t.Errorf("misc.Command = %q, want 'bash'", misc.Command)
	}
	dev, ok := cfg.Tmux["dev"]
	if !ok {
		t.Fatal("Tmux missing 'dev' key")
	}
	if dev.Command != "npm run dev" {
		t.Errorf("dev.Command = %q, want 'npm run dev'", dev.Command)
	}
	if dev.Env["PORT"] != "<PORT1>" {
		t.Errorf("dev.Env[PORT] = %q, want '<PORT1>'", dev.Env["PORT"])
	}

	// Proxy
	if cfg.Proxy == nil {
		t.Fatal("Proxy is nil")
	}
	if cfg.Proxy.Name != "myapp.<BRANCH>.<PROJECT>" {
		t.Errorf("Proxy.Name = %q, want 'myapp.<BRANCH>.<PROJECT>'", cfg.Proxy.Name)
	}
	if cfg.Proxy.Port != "<PORT1>" {
		t.Errorf("Proxy.Port = %q, want '<PORT1>'", cfg.Proxy.Port)
	}
}

func TestParseAruConfig_MissingFile(t *testing.T) {
	dir := t.TempDir()
	// No aru.json in dir

	cfg, err := ParseAruConfig(dir)
	if err != nil {
		t.Fatalf("ParseAruConfig() for missing file returned error: %v", err)
	}
	if cfg != nil {
		t.Fatal("ParseAruConfig() should return nil for missing file")
	}
}

func TestParseAruConfig_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "aru.json"), []byte("{invalid json}"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := ParseAruConfig(dir)
	if err == nil {
		t.Fatal("ParseAruConfig() should return error for malformed JSON")
	}
	if cfg != nil {
		t.Fatal("ParseAruConfig() should return nil on error")
	}
}

func TestParseAruConfig_EmptyConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "aru.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := ParseAruConfig(dir)
	if err != nil {
		t.Fatalf("ParseAruConfig() returned error: %v", err)
	}
	if cfg == nil {
		t.Fatal("ParseAruConfig() returned nil for empty config")
	}

	if cfg.Worktree != nil {
		t.Error("Worktree should be nil for empty config")
	}
	if cfg.Tmux != nil {
		t.Error("Tmux should be nil for empty config")
	}
	if cfg.Proxy != nil {
		t.Error("Proxy should be nil for empty config")
	}
}

func TestParseAruConfig_PartialConfig(t *testing.T) {
	dir := t.TempDir()
	jsonContent := `{"worktree": {"setup": ["echo hello"]}}`
	if err := os.WriteFile(filepath.Join(dir, "aru.json"), []byte(jsonContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := ParseAruConfig(dir)
	if err != nil {
		t.Fatalf("ParseAruConfig() returned error: %v", err)
	}
	if cfg == nil {
		t.Fatal("ParseAruConfig() returned nil")
	}

	if cfg.Worktree == nil {
		t.Fatal("Worktree should not be nil")
	}
	if len(cfg.Worktree.Setup) != 1 || cfg.Worktree.Setup[0] != "echo hello" {
		t.Errorf("Setup = %v, want [echo hello]", cfg.Worktree.Setup)
	}
	if len(cfg.Worktree.Teardown) != 0 {
		t.Errorf("Teardown = %v, want empty", cfg.Worktree.Teardown)
	}
	if cfg.Tmux != nil {
		t.Error("Tmux should be nil")
	}
	if cfg.Proxy != nil {
		t.Error("Proxy should be nil")
	}
}

// ── collectPortPlaceholders tests ──────────────────────────────────────────

func TestCollectPortPlaceholders_None(t *testing.T) {
	cfg := &AruConfig{
		Worktree: &WorktreeConfig{
			Setup: []string{"echo hello"},
		},
	}
	nums := collectPortPlaceholders(cfg)
	if len(nums) != 0 {
		t.Errorf("got %v, want empty slice", nums)
	}
}

func TestCollectPortPlaceholders_NilConfig(t *testing.T) {
	nums := collectPortPlaceholders(nil)
	if len(nums) != 0 {
		t.Errorf("got %v, want nil for nil config", nums)
	}
}

func TestCollectPortPlaceholders_Single(t *testing.T) {
	cfg := &AruConfig{
		Proxy: &ProxyConfig{
			Port: "<PORT1>",
		},
	}
	nums := collectPortPlaceholders(cfg)
	if len(nums) != 1 || nums[0] != "1" {
		t.Errorf("got %v, want [\"1\"]", nums)
	}
}

func TestCollectPortPlaceholders_Multiple(t *testing.T) {
	cfg := &AruConfig{
		Tmux: TmuxConfig{
			"dev":  {Command: "npm run dev", Env: map[string]string{"PORT": "<PORT1>"}},
			"api":  {Command: "node server.js", Env: map[string]string{"PORT": "<PORT2>"}},
			"misc": {Command: "bash"},
		},
		Proxy: &ProxyConfig{
			Port: "<PORT1>",
		},
	}
	nums := collectPortPlaceholders(cfg)
	if len(nums) != 2 || nums[0] != "1" || nums[1] != "2" {
		t.Errorf("got %v, want [\"1\" \"2\"]", nums)
	}
}

// ── resolvePlaceholders tests ──────────────────────────────────────────────

func TestResolvePlaceholders_NilConfig(t *testing.T) {
	resolved, err := resolvePlaceholders(nil, "p", "b", map[int]int{})
	if err != nil {
		t.Fatalf("resolvePlaceholders(nil) returned error: %v", err)
	}
	if resolved != nil {
		t.Fatal("resolvePlaceholders(nil) should return nil")
	}
}

func TestResolvePlaceholders_ProjectBranch(t *testing.T) {
	cfg := &AruConfig{
		Worktree: &WorktreeConfig{
			Setup: []string{"echo <PROJECT>/<BRANCH>"},
		},
		Proxy: &ProxyConfig{
			Name: "app.<BRANCH>.<PROJECT>",
			Port: "3000",
		},
	}
	ports := map[int]int{}

	resolved, err := resolvePlaceholders(cfg, "myproject", "feature-x", ports)
	if err != nil {
		t.Fatalf("resolvePlaceholders() returned error: %v", err)
	}
	if resolved == nil {
		t.Fatal("resolvePlaceholders() returned nil")
	}

	if len(resolved.Setup) != 1 || resolved.Setup[0] != "echo myproject/feature-x" {
		t.Errorf("Setup[0] = %q, want 'echo myproject/feature-x'", resolved.Setup[0])
	}
	if resolved.Proxy.Name != "app.feature-x.myproject" {
		t.Errorf("Proxy.Name = %q, want 'app.feature-x.myproject'", resolved.Proxy.Name)
	}
}

func TestResolvePlaceholders_PortAllocation(t *testing.T) {
	cfg := &AruConfig{
		Tmux: TmuxConfig{
			"dev": {Command: "npm run dev", Env: map[string]string{"PORT": "<PORT1>"}},
		},
		Proxy: &ProxyConfig{
			Name: "myapp",
			Port: "<PORT1>",
		},
	}
	ports := map[int]int{1: 3001}

	resolved, err := resolvePlaceholders(cfg, "proj", "branch", ports)
	if err != nil {
		t.Fatalf("resolvePlaceholders() returned error: %v", err)
	}

	// Both PORT1 references should resolve to 3001
	if resolved.Tmux["dev"].Env["PORT"] != "3001" {
		t.Errorf("dev PORT = %q, want '3001'", resolved.Tmux["dev"].Env["PORT"])
	}
	if resolved.Proxy.Port != "3001" {
		t.Errorf("Proxy.Port = %q, want '3001'", resolved.Proxy.Port)
	}
}

func TestResolvePlaceholders_MultipleSamePort(t *testing.T) {
	cfg := &AruConfig{
		Tmux: TmuxConfig{
			"dev": {Command: "npm run dev", Env: map[string]string{"PORT": "<PORT1>"}},
			"api": {Command: "node api.js", Env: map[string]string{"PORT": "<PORT1>"}},
		},
	}
	ports := map[int]int{1: 4000}

	resolved, err := resolvePlaceholders(cfg, "proj", "branch", ports)
	if err != nil {
		t.Fatalf("resolvePlaceholders() returned error: %v", err)
	}

	if resolved.Tmux["dev"].Env["PORT"] != "4000" {
		t.Errorf("dev PORT = %q, want '4000'", resolved.Tmux["dev"].Env["PORT"])
	}
	if resolved.Tmux["api"].Env["PORT"] != "4000" {
		t.Errorf("api PORT = %q, want '4000'", resolved.Tmux["api"].Env["PORT"])
	}
}

func TestResolvePlaceholders_PreservesNonPlaceholders(t *testing.T) {
	cfg := &AruConfig{
		Worktree: &WorktreeConfig{
			Setup: []string{"echo hello world", "npm install"},
		},
	}
	ports := map[int]int{}

	resolved, err := resolvePlaceholders(cfg, "proj", "branch", ports)
	if err != nil {
		t.Fatalf("resolvePlaceholders() returned error: %v", err)
	}

	if len(resolved.Setup) != 2 {
		t.Fatalf("got %d setup commands, want 2", len(resolved.Setup))
	}
	if resolved.Setup[0] != "echo hello world" {
		t.Errorf("Setup[0] = %q, want 'echo hello world'", resolved.Setup[0])
	}
	if resolved.Setup[1] != "npm install" {
		t.Errorf("Setup[1] = %q, want 'npm install'", resolved.Setup[1])
	}
}

func TestResolvePlaceholders_PortInCommand(t *testing.T) {
	cfg := &AruConfig{
		Tmux: TmuxConfig{
			"dev": {Command: "npm run dev -- --port <PORT1>"},
		},
	}
	ports := map[int]int{1: 3001}

	resolved, err := resolvePlaceholders(cfg, "proj", "branch", ports)
	if err != nil {
		t.Fatalf("resolvePlaceholders() returned error: %v", err)
	}

	if resolved.Tmux["dev"].Command != "npm run dev -- --port 3001" {
		t.Errorf("Command = %q, want 'npm run dev -- --port 3001'", resolved.Tmux["dev"].Command)
	}
}

// ── F2 regression: JSON metacharacters in project/branch names ────────────

// TestResolvePlaceholders_JSONMetacharsInProject is the regression test for F2.
// With the old marshal-replace-unmarshal approach, a project name containing
// a JSON metacharacter like `"` would corrupt the marshaled JSON and cause
// json.Unmarshal to fail. With the struct-walking approach, the project name
// is substituted directly into Go string fields, bypassing JSON escaping.
func TestResolvePlaceholders_JSONMetacharsInProject(t *testing.T) {
	cfg := &AruConfig{
		Worktree: &WorktreeConfig{
			Setup: []string{"echo <PROJECT>/<BRANCH>"},
		},
	}

	// Project name with a literal double-quote (would break JSON round-trip)
	project := `my"project`
	branch := `feature\x`

	resolved, err := resolvePlaceholders(cfg, project, branch, map[int]int{})
	if err != nil {
		t.Fatalf("resolvePlaceholders() returned error for metachar name: %v", err)
	}
	if resolved == nil {
		t.Fatal("resolvePlaceholders() returned nil")
	}
	if len(resolved.Setup) != 1 {
		t.Fatalf("got %d setup commands, want 1", len(resolved.Setup))
	}
	want := `echo my"project/feature\x`
	if resolved.Setup[0] != want {
		t.Errorf("Setup[0] = %q, want %q", resolved.Setup[0], want)
	}
}

func TestResolvePlaceholders_BackslashInProject(t *testing.T) {
	cfg := &AruConfig{
		Worktree: &WorktreeConfig{
			Setup: []string{"echo <PROJECT>"},
		},
	}

	// Project name with a backslash (would break JSON round-trip escaping)
	project := `path\to\project`

	resolved, err := resolvePlaceholders(cfg, project, "main", map[int]int{})
	if err != nil {
		t.Fatalf("resolvePlaceholders() returned error for backslash name: %v", err)
	}
	if resolved.Setup[0] != `echo path\to\project` {
		t.Errorf("Setup[0] = %q, want %q", resolved.Setup[0], `echo path\to\project`)
	}
}

func TestResolvePlaceholders_UnicodeInProject(t *testing.T) {
	cfg := &AruConfig{
		Worktree: &WorktreeConfig{
			Setup: []string{"echo <PROJECT>-<BRANCH>"},
		},
	}

	project := "プロジェクト" // Japanese
	branch := "ветка"   // Russian

	resolved, err := resolvePlaceholders(cfg, project, branch, map[int]int{})
	if err != nil {
		t.Fatalf("resolvePlaceholders() returned error for unicode name: %v", err)
	}
	want := "echo " + project + "-" + branch
	if resolved.Setup[0] != want {
		t.Errorf("Setup[0] = %q, want %q", resolved.Setup[0], want)
	}
}

func TestResolvePlaceholders_NewlineInProject(t *testing.T) {
	cfg := &AruConfig{
		Worktree: &WorktreeConfig{
			Setup: []string{"echo <PROJECT>"},
		},
	}

	// Project with newline (would break JSON output and confuse shell)
	project := "line1\nline2"

	resolved, err := resolvePlaceholders(cfg, project, "b", map[int]int{})
	if err != nil {
		t.Fatalf("resolvePlaceholders() returned error for newline name: %v", err)
	}
	if resolved.Setup[0] != "echo line1\nline2" {
		t.Errorf("Setup[0] = %q, want %q", resolved.Setup[0], "echo line1\nline2")
	}
}

func TestResolveTeardownPlaceholders_JSONMetacharsInProject(t *testing.T) {
	cfg := &AruConfig{
		Worktree: &WorktreeConfig{
			Teardown: []string{"echo <PROJECT>"},
		},
	}

	project := `evil"; rm -rf /`
	resolved, err := resolveTeardownPlaceholders(cfg, project, "b")
	if err != nil {
		t.Fatalf("resolveTeardownPlaceholders() returned error: %v", err)
	}
	// The project name is substituted verbatim. Note: this is by design
	// — the command itself runs in bash, so this is a shell-injection risk
	// in `aru.json` commands regardless of placeholder escaping. The F2 fix
	// is about JSON parsing, not shell safety (that's F4's concern).
	if resolved.Teardown[0] != `echo evil"; rm -rf /` {
		t.Errorf("Teardown[0] = %q, want %q", resolved.Teardown[0], `echo evil"; rm -rf /`)
	}
}

func TestCollectPortPlaceholders_DoesNotPanicOnMetachars(t *testing.T) {
	cfg := &AruConfig{
		Worktree: &WorktreeConfig{
			Setup: []string{`echo <PROJECT>:<PORT1>`},
		},
		Proxy: &ProxyConfig{
			Name: `app-<PROJECT>`,
			Port: `<PORT1>`,
		},
	}

	// Project/branch aren't passed to collectPortPlaceholders, but the
	// struct fields contain literal `<` and `>` from the placeholders.
	// The walker should find <PORT1> regardless of metachars elsewhere.
	nums := collectPortPlaceholders(cfg)
	if len(nums) != 1 || nums[0] != "1" {
		t.Errorf("collectPortPlaceholders = %v, want [\"1\"]", nums)
	}
}

func TestResolvePlaceholders_DoesNotMutateOriginal(t *testing.T) {
	original := &AruConfig{
		Worktree: &WorktreeConfig{
			Setup: []string{"echo <PROJECT>"},
		},
		Tmux: TmuxConfig{
			"dev": {Command: "npm run dev", Env: map[string]string{"PORT": "<PORT1>"}},
		},
	}
	originalSetupSnapshot := original.Worktree.Setup[0]
	originalCommandSnapshot := original.Tmux["dev"].Command
	originalEnvSnapshot := original.Tmux["dev"].Env["PORT"]

	_, err := resolvePlaceholders(original, "myproject", "feature-x", map[int]int{1: 3000})
	if err != nil {
		t.Fatalf("resolvePlaceholders() returned error: %v", err)
	}

	// Original should be unchanged
	if original.Worktree.Setup[0] != originalSetupSnapshot {
		t.Errorf("original Setup[0] was mutated: got %q, want %q", original.Worktree.Setup[0], originalSetupSnapshot)
	}
	if original.Tmux["dev"].Command != originalCommandSnapshot {
		t.Errorf("original Command was mutated: got %q, want %q", original.Tmux["dev"].Command, originalCommandSnapshot)
	}
	if original.Tmux["dev"].Env["PORT"] != originalEnvSnapshot {
		t.Errorf("original Env[PORT] was mutated: got %q, want %q", original.Tmux["dev"].Env["PORT"], originalEnvSnapshot)
	}
}

// TestCloneConfig_PreservesAllFields is a regression test for a bug where
// cloneConfig forgot to copy SetupOneshot, causing setup_oneshot=true to be
// silently dropped during placeholder resolution.
func TestCloneConfig_PreservesAllFields(t *testing.T) {
	original := &AruConfig{
		Worktree: &WorktreeConfig{
			Setup:        []string{"echo <PROJECT>"},
			SetupOneshot: true,
			Teardown:     []string{"rm -rf"},
		},
	}

	clone := cloneConfig(original)
	if clone == nil {
		t.Fatal("cloneConfig returned nil")
	}
	if !clone.Worktree.SetupOneshot {
		t.Error("cloneConfig did not preserve SetupOneshot=true")
	}
	if len(clone.Worktree.Setup) != 1 || clone.Worktree.Setup[0] != "echo <PROJECT>" {
		t.Errorf("cloneConfig did not preserve Setup: %v", clone.Worktree.Setup)
	}
	if len(clone.Worktree.Teardown) != 1 || clone.Worktree.Teardown[0] != "rm -rf" {
		t.Errorf("cloneConfig did not preserve Teardown: %v", clone.Worktree.Teardown)
	}
}

func TestResolvePlaceholders_PreservesSetupOneshot(t *testing.T) {
	// End-to-end: parse aru.json with setup_oneshot=true, resolve, and verify
	// the flag survives the resolution.
	jsonContent := `{
		"worktree": {
			"setup": ["npm install"],
			"setup_oneshot": true
		}
	}`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "aru.json"), []byte(jsonContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := ParseAruConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Worktree.SetupOneshot {
		t.Fatal("ParseAruConfig did not read setup_oneshot=true")
	}

	resolved, err := resolvePlaceholders(cfg, "p", "b", map[int]int{})
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.SetupOneshot {
		t.Error("resolvePlaceholders dropped SetupOneshot — cloneConfig bug regression")
	}
}

// ── resolveTeardownPlaceholders tests ──────────────────────────────────────

func TestResolveTeardownPlaceholders_NameOnly(t *testing.T) {
	cfg := &AruConfig{
		Worktree: &WorktreeConfig{
			Teardown: []string{"echo tearing down <PROJECT>/<BRANCH>"},
		},
		Proxy: &ProxyConfig{
			Name: "app.<BRANCH>.<PROJECT>",
			Port: "<PORT1>",
		},
	}

	resolved, err := resolveTeardownPlaceholders(cfg, "myproject", "feature-x")
	if err != nil {
		t.Fatalf("resolveTeardownPlaceholders() returned error: %v", err)
	}
	if resolved == nil {
		t.Fatal("resolveTeardownPlaceholders() returned nil")
	}

	// PROJECT and BRANCH resolved
	if len(resolved.Teardown) != 1 || resolved.Teardown[0] != "echo tearing down myproject/feature-x" {
		t.Errorf("Teardown[0] = %q, want 'echo tearing down myproject/feature-x'", resolved.Teardown[0])
	}

	// PORTn left as literal (not resolved)
	if resolved.Proxy.Port != "<PORT1>" {
		t.Errorf("Proxy.Port = %q, want '<PORT1>' (should be literal)", resolved.Proxy.Port)
	}
}

func TestResolveTeardownPlaceholders_NilConfig(t *testing.T) {
	resolved, err := resolveTeardownPlaceholders(nil, "p", "b")
	if err != nil {
		t.Fatalf("resolveTeardownPlaceholders(nil) returned error: %v", err)
	}
	if resolved != nil {
		t.Fatal("resolveTeardownPlaceholders(nil) should return nil")
	}
}

// ── allocatePorts tests ───────────────────────────────────────────────────

func TestAllocatePorts(t *testing.T) {
	ports := allocatePorts([]string{"1", "2", "3"})
	if len(ports) != 3 {
		t.Fatalf("got %d ports, want 3", len(ports))
	}

	for _, num := range []int{1, 2, 3} {
		p, ok := ports[num]
		if !ok {
			t.Errorf("missing port for placeholder %d", num)
		}
		if p < 1024 || p >= 10000 {
			t.Errorf("port %d for placeholder %d is out of range", p, num)
		}
	}
}

func TestAllocatePorts_Empty(t *testing.T) {
	ports := allocatePorts(nil)
	if len(ports) != 0 {
		t.Errorf("got %d ports, want 0", len(ports))
	}
}

func TestAllocatePorts_AllInRange(t *testing.T) {
	ports := allocatePorts([]string{"1", "2", "3"})
	if len(ports) == 0 {
		t.Fatal("allocatePorts returned no ports")
	}

	// Verify all ports are in the valid range (best-effort uniqueness)
	for num, port := range ports {
		if port < 1024 || port >= 10000 {
			t.Errorf("port %d for placeholder %d is out of range", port, num)
		}
	}
}
