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
		"tmux": [
			{"name": "misc", "command": "bash"},
			{"name": "dev", "command": "npm run dev", "env": {"PORT": "<PORT1>"}}
		],
		"proxy": [
			{"name": "myapp.<BRANCH>.<PROJECT>", "port": "<PORT1>"}
		]
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

	// Tmux (slice order preserved)
	if len(cfg.Tmux) != 2 {
		t.Fatalf("Tmux has %d entries, want 2", len(cfg.Tmux))
	}
	if cfg.Tmux[0].Name != "misc" {
		t.Errorf("Tmux[0].Name = %q, want 'misc'", cfg.Tmux[0].Name)
	}
	if cfg.Tmux[0].Command != "bash" {
		t.Errorf("Tmux[0].Command = %q, want 'bash'", cfg.Tmux[0].Command)
	}
	if cfg.Tmux[1].Name != "dev" {
		t.Errorf("Tmux[1].Name = %q, want 'dev'", cfg.Tmux[1].Name)
	}
	if cfg.Tmux[1].Command != "npm run dev" {
		t.Errorf("Tmux[1].Command = %q, want 'npm run dev'", cfg.Tmux[1].Command)
	}
	if cfg.Tmux[1].Env["PORT"] != "<PORT1>" {
		t.Errorf("Tmux[1].Env[PORT] = %q, want '<PORT1>'", cfg.Tmux[1].Env["PORT"])
	}

	// Proxy
	if len(cfg.Proxy) != 1 {
		t.Fatalf("Proxy has %d entries, want 1", len(cfg.Proxy))
	}
	if cfg.Proxy[0].Name != "myapp.<BRANCH>.<PROJECT>" {
		t.Errorf("Proxy[0].Name = %q, want 'myapp.<BRANCH>.<PROJECT>'", cfg.Proxy[0].Name)
	}
	if cfg.Proxy[0].Port != "<PORT1>" {
		t.Errorf("Proxy[0].Port = %q, want '<PORT1>'", cfg.Proxy[0].Port)
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
	if len(cfg.Tmux) != 0 {
		t.Error("Tmux should be nil/empty for empty config")
	}
	if len(cfg.Proxy) != 0 {
		t.Error("Proxy should be nil/empty for empty config")
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
	if len(cfg.Tmux) != 0 {
		t.Error("Tmux should be nil/empty")
	}
	if len(cfg.Proxy) != 0 {
		t.Error("Proxy should be nil/empty")
	}
}

// ── New test: Tmux entry order preserved ──────────────────────────────────

func TestParseAruConfig_TmuxEntryOrderPreserved(t *testing.T) {
	dir := t.TempDir()
	jsonContent := `{
		"tmux": [
			{"name": "editor", "command": "vim"},
			{"name": "server", "command": "npm run dev"},
			{"name": "logs", "command": "tail -f"}
		]
	}`
	if err := os.WriteFile(filepath.Join(dir, "aru.json"), []byte(jsonContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := ParseAruConfig(dir)
	if err != nil {
		t.Fatalf("ParseAruConfig() returned error: %v", err)
	}

	if len(cfg.Tmux) != 3 {
		t.Fatalf("Tmux has %d entries, want 3", len(cfg.Tmux))
	}
	if cfg.Tmux[0].Name != "editor" {
		t.Errorf("Tmux[0].Name = %q, want 'editor'", cfg.Tmux[0].Name)
	}
	if cfg.Tmux[1].Name != "server" {
		t.Errorf("Tmux[1].Name = %q, want 'server'", cfg.Tmux[1].Name)
	}
	if cfg.Tmux[2].Name != "logs" {
		t.Errorf("Tmux[2].Name = %q, want 'logs'", cfg.Tmux[2].Name)
	}
}

// ── New test: Multiple proxies ────────────────────────────────────────────

func TestParseAruConfig_MultipleProxies(t *testing.T) {
	dir := t.TempDir()
	jsonContent := `{
		"proxy": [
			{"name": "frontend", "port": "3000"},
			{"name": "api", "port": "4000"}
		]
	}`
	if err := os.WriteFile(filepath.Join(dir, "aru.json"), []byte(jsonContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := ParseAruConfig(dir)
	if err != nil {
		t.Fatalf("ParseAruConfig() returned error: %v", err)
	}
	if len(cfg.Proxy) != 2 {
		t.Fatalf("Proxy has %d entries, want 2", len(cfg.Proxy))
	}
	if cfg.Proxy[0].Name != "frontend" || cfg.Proxy[0].Port != "3000" {
		t.Errorf("Proxy[0] = %+v, want {Name: frontend, Port: 3000}", cfg.Proxy[0])
	}
	if cfg.Proxy[1].Name != "api" || cfg.Proxy[1].Port != "4000" {
		t.Errorf("Proxy[1] = %+v, want {Name: api, Port: 4000}", cfg.Proxy[1])
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
		Proxy: []ProxyConfig{
			{Port: "<PORT1>"},
		},
	}
	nums := collectPortPlaceholders(cfg)
	if len(nums) != 1 || nums[0] != "1" {
		t.Errorf("got %v, want [\"1\"]", nums)
	}
}

func TestCollectPortPlaceholders_Multiple(t *testing.T) {
	cfg := &AruConfig{
		Tmux: []TmuxWindowEntry{
			{Command: "npm run dev", Env: map[string]string{"PORT": "<PORT1>"}},
			{Command: "node server.js", Env: map[string]string{"PORT": "<PORT2>"}},
			{Command: "bash"},
		},
		Proxy: []ProxyConfig{
			{Port: "<PORT1>"},
		},
	}
	nums := collectPortPlaceholders(cfg)
	if len(nums) != 2 || nums[0] != "1" || nums[1] != "2" {
		t.Errorf("got %v, want [\"1\" \"2\"]", nums)
	}
}

func TestCollectPortPlaceholders_TmuxAndProxyCombined(t *testing.T) {
	cfg := &AruConfig{
		Tmux: []TmuxWindowEntry{
			{Command: "npm run dev -- --port <PORT1>"},
			{Command: "echo <PORT2>"},
		},
		Proxy: []ProxyConfig{
			{Name: "app1", Port: "<PORT1>"},
			{Name: "app2", Port: "<PORT3>"},
		},
	}
	nums := collectPortPlaceholders(cfg)
	if len(nums) != 3 || nums[0] != "1" || nums[1] != "2" || nums[2] != "3" {
		t.Errorf("got %v, want [\"1\" \"2\" \"3\"]", nums)
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
		Proxy: []ProxyConfig{
			{Name: "app.<BRANCH>.<PROJECT>", Port: "3000"},
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

	if resolved.Worktree == nil {
		t.Fatal("resolved.Worktree is nil")
	}
	if len(resolved.Worktree.Setup) != 1 || resolved.Worktree.Setup[0] != "echo myproject/feature-x" {
		t.Errorf("Worktree.Setup[0] = %q, want 'echo myproject/feature-x'", resolved.Worktree.Setup[0])
	}
	if len(resolved.Proxy) != 1 || resolved.Proxy[0].Name != "app.feature-x.myproject" {
		t.Errorf("Proxy[0].Name = %q, want 'app.feature-x.myproject'", resolved.Proxy[0].Name)
	}
}

func TestResolvePlaceholders_PortAllocation(t *testing.T) {
	cfg := &AruConfig{
		Tmux: []TmuxWindowEntry{
			{Name: "dev", Command: "npm run dev", Env: map[string]string{"PORT": "<PORT1>"}},
		},
		Proxy: []ProxyConfig{
			{Name: "myapp", Port: "<PORT1>"},
		},
	}
	ports := map[int]int{1: 3001}

	resolved, err := resolvePlaceholders(cfg, "proj", "branch", ports)
	if err != nil {
		t.Fatalf("resolvePlaceholders() returned error: %v", err)
	}

	// Both PORT1 references should resolve to 3001
	if len(resolved.Tmux) != 1 || resolved.Tmux[0].Env["PORT"] != "3001" {
		t.Errorf("Tmux[0].Env[PORT] = %q, want '3001'", resolved.Tmux[0].Env["PORT"])
	}
	if len(resolved.Proxy) != 1 || resolved.Proxy[0].Port != "3001" {
		t.Errorf("Proxy[0].Port = %q, want '3001'", resolved.Proxy[0].Port)
	}
}

func TestResolvePlaceholders_MultipleSamePort(t *testing.T) {
	cfg := &AruConfig{
		Tmux: []TmuxWindowEntry{
			{Name: "dev", Command: "npm run dev", Env: map[string]string{"PORT": "<PORT1>"}},
			{Name: "api", Command: "node api.js", Env: map[string]string{"PORT": "<PORT1>"}},
		},
	}
	ports := map[int]int{1: 4000}

	resolved, err := resolvePlaceholders(cfg, "proj", "branch", ports)
	if err != nil {
		t.Fatalf("resolvePlaceholders() returned error: %v", err)
	}

	if len(resolved.Tmux) != 2 {
		t.Fatalf("Tmux has %d entries, want 2", len(resolved.Tmux))
	}
	if resolved.Tmux[0].Env["PORT"] != "4000" {
		t.Errorf("Tmux[0] PORT = %q, want '4000'", resolved.Tmux[0].Env["PORT"])
	}
	if resolved.Tmux[1].Env["PORT"] != "4000" {
		t.Errorf("Tmux[1] PORT = %q, want '4000'", resolved.Tmux[1].Env["PORT"])
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
	if resolved.Worktree == nil {
		t.Fatal("resolved.Worktree is nil")
	}

	if len(resolved.Worktree.Setup) != 2 {
		t.Fatalf("got %d setup commands, want 2", len(resolved.Worktree.Setup))
	}
	if resolved.Worktree.Setup[0] != "echo hello world" {
		t.Errorf("Setup[0] = %q, want 'echo hello world'", resolved.Worktree.Setup[0])
	}
	if resolved.Worktree.Setup[1] != "npm install" {
		t.Errorf("Setup[1] = %q, want 'npm install'", resolved.Worktree.Setup[1])
	}
}

func TestResolvePlaceholders_PortInCommand(t *testing.T) {
	cfg := &AruConfig{
		Tmux: []TmuxWindowEntry{
			{Name: "dev", Command: "npm run dev -- --port <PORT1>"},
		},
	}
	ports := map[int]int{1: 3001}

	resolved, err := resolvePlaceholders(cfg, "proj", "branch", ports)
	if err != nil {
		t.Fatalf("resolvePlaceholders() returned error: %v", err)
	}

	if len(resolved.Tmux) != 1 || resolved.Tmux[0].Command != "npm run dev -- --port 3001" {
		t.Errorf("Command = %q, want 'npm run dev -- --port 3001'", resolved.Tmux[0].Command)
	}
}

// ── F2 regression: JSON metacharacters in project/branch names ────────────

func TestResolvePlaceholders_JSONMetacharsInProject(t *testing.T) {
	cfg := &AruConfig{
		Worktree: &WorktreeConfig{
			Setup: []string{"echo <PROJECT>/<BRANCH>"},
		},
	}

	project := `my"project`
	branch := `feature\x`

	resolved, err := resolvePlaceholders(cfg, project, branch, map[int]int{})
	if err != nil {
		t.Fatalf("resolvePlaceholders() returned error for metachar name: %v", err)
	}
	if resolved == nil {
		t.Fatal("resolvePlaceholders() returned nil")
	}
	if resolved.Worktree == nil {
		t.Fatal("resolved.Worktree is nil")
	}
	if len(resolved.Worktree.Setup) != 1 {
		t.Fatalf("got %d setup commands, want 1", len(resolved.Worktree.Setup))
	}
	want := `echo my"project/feature\x`
	if resolved.Worktree.Setup[0] != want {
		t.Errorf("Setup[0] = %q, want %q", resolved.Worktree.Setup[0], want)
	}
}

func TestResolvePlaceholders_BackslashInProject(t *testing.T) {
	cfg := &AruConfig{
		Worktree: &WorktreeConfig{
			Setup: []string{"echo <PROJECT>"},
		},
	}

	project := `path\to\project`

	resolved, err := resolvePlaceholders(cfg, project, "main", map[int]int{})
	if err != nil {
		t.Fatalf("resolvePlaceholders() returned error for backslash name: %v", err)
	}
	if resolved.Worktree.Setup[0] != `echo path\to\project` {
		t.Errorf("Setup[0] = %q, want %q", resolved.Worktree.Setup[0], `echo path\to\project`)
	}
}

func TestResolvePlaceholders_UnicodeInProject(t *testing.T) {
	cfg := &AruConfig{
		Worktree: &WorktreeConfig{
			Setup: []string{"echo <PROJECT>-<BRANCH>"},
		},
	}

	project := "プロジェクト"
	branch := "ветка"

	resolved, err := resolvePlaceholders(cfg, project, branch, map[int]int{})
	if err != nil {
		t.Fatalf("resolvePlaceholders() returned error for unicode name: %v", err)
	}
	if resolved.Worktree == nil {
		t.Fatal("resolved.Worktree is nil")
	}
	want := "echo " + project + "-" + branch
	if resolved.Worktree.Setup[0] != want {
		t.Errorf("Setup[0] = %q, want %q", resolved.Worktree.Setup[0], want)
	}
}

func TestResolvePlaceholders_NewlineInProject(t *testing.T) {
	cfg := &AruConfig{
		Worktree: &WorktreeConfig{
			Setup: []string{"echo <PROJECT>"},
		},
	}

	project := "line1\nline2"

	resolved, err := resolvePlaceholders(cfg, project, "b", map[int]int{})
	if err != nil {
		t.Fatalf("resolvePlaceholders() returned error for newline name: %v", err)
	}
	if resolved.Worktree.Setup[0] != "echo line1\nline2" {
		t.Errorf("Setup[0] = %q, want %q", resolved.Worktree.Setup[0], "echo line1\nline2")
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
	if resolved.Worktree.Teardown[0] != `echo evil"; rm -rf /` {
		t.Errorf("Teardown[0] = %q, want %q", resolved.Worktree.Teardown[0], `echo evil"; rm -rf /`)
	}
}

func TestCollectPortPlaceholders_DoesNotPanicOnMetachars(t *testing.T) {
	cfg := &AruConfig{
		Worktree: &WorktreeConfig{
			Setup: []string{`echo <PROJECT>:<PORT1>`},
		},
		Proxy: []ProxyConfig{
			{Name: `app-<PROJECT>`, Port: `<PORT1>`},
		},
	}

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
		Tmux: []TmuxWindowEntry{
			{Name: "dev", Command: "npm run dev", Env: map[string]string{"PORT": "<PORT1>"}},
		},
	}
	originalSetupSnapshot := original.Worktree.Setup[0]
	originalCommandSnapshot := original.Tmux[0].Command
	originalEnvSnapshot := original.Tmux[0].Env["PORT"]

	_, err := resolvePlaceholders(original, "myproject", "feature-x", map[int]int{1: 3000})
	if err != nil {
		t.Fatalf("resolvePlaceholders() returned error: %v", err)
	}

	// Original should be unchanged
	if original.Worktree.Setup[0] != originalSetupSnapshot {
		t.Errorf("original Setup[0] was mutated: got %q, want %q", original.Worktree.Setup[0], originalSetupSnapshot)
	}
	if original.Tmux[0].Command != originalCommandSnapshot {
		t.Errorf("original Command was mutated: got %q, want %q", original.Tmux[0].Command, originalCommandSnapshot)
	}
	if original.Tmux[0].Env["PORT"] != originalEnvSnapshot {
		t.Errorf("original Env[PORT] was mutated: got %q, want %q", original.Tmux[0].Env["PORT"], originalEnvSnapshot)
	}
}

// ── RamDirConfig tests ────────────────────────────────────────────────────

func TestParseAruConfig_RamDir_SingleEntry(t *testing.T) {
	dir := t.TempDir()
	jsonContent := `{
		"ramdir": [{"path": "data", "size": "100M"}]
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

	if len(cfg.RamDir) != 1 {
		t.Fatalf("RamDir has %d entries, want 1", len(cfg.RamDir))
	}
	if cfg.RamDir[0].Path != "data" {
		t.Errorf("RamDir[0].Path = %q, want 'data'", cfg.RamDir[0].Path)
	}
	if cfg.RamDir[0].Size != "100M" {
		t.Errorf("RamDir[0].Size = %q, want '100M'", cfg.RamDir[0].Size)
	}
}

func TestParseAruConfig_RamDir_MultipleEntries(t *testing.T) {
	dir := t.TempDir()
	jsonContent := `{
		"ramdir": [
			{"path": "data", "size": "100M"},
			{"path": "cache/build", "size": "500M"},
			{"path": "logs"}
		]
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

	if len(cfg.RamDir) != 3 {
		t.Fatalf("RamDir has %d entries, want 3", len(cfg.RamDir))
	}
	if cfg.RamDir[0].Path != "data" || cfg.RamDir[0].Size != "100M" {
		t.Errorf("RamDir[0] = %+v, want {Path: data, Size: 100M}", cfg.RamDir[0])
	}
	if cfg.RamDir[1].Path != "cache/build" || cfg.RamDir[1].Size != "500M" {
		t.Errorf("RamDir[1] = %+v, want {Path: cache/build, Size: 500M}", cfg.RamDir[1])
	}
	if cfg.RamDir[2].Path != "logs" || cfg.RamDir[2].Size != "" {
		t.Errorf("RamDir[2] = %+v, want {Path: logs, Size: ''}", cfg.RamDir[2])
	}
}

func TestParseAruConfig_RamDir_Absent(t *testing.T) {
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

	if cfg.RamDir != nil {
		t.Errorf("RamDir should be nil when key is absent, got %+v", cfg.RamDir)
	}
}

func TestCloneConfig_PreservesRamDir(t *testing.T) {
	original := &AruConfig{
		RamDir: []RamDirConfig{
			{Path: "data", Size: "100M"},
			{Path: "cache", Size: "500M"},
		},
	}

	clone := cloneConfig(original)
	if clone == nil {
		t.Fatal("cloneConfig returned nil")
	}

	if len(clone.RamDir) != 2 {
		t.Fatalf("cloneConfig.RamDir has %d entries, want 2", len(clone.RamDir))
	}
	if clone.RamDir[0].Path != "data" || clone.RamDir[0].Size != "100M" {
		t.Errorf("RamDir[0] = %+v, want {Path: data, Size: 100M}", clone.RamDir[0])
	}
	if clone.RamDir[1].Path != "cache" || clone.RamDir[1].Size != "500M" {
		t.Errorf("RamDir[1] = %+v, want {Path: cache, Size: 500M}", clone.RamDir[1])
	}

	// Verify mutation isolation: modifying clone should not affect original
	clone.RamDir[0].Size = "200M"
	if original.RamDir[0].Size != "100M" {
		t.Error("cloneConfig mutation affected original — not a deep copy")
	}
}

func TestCloneConfig_RamDir_Nil(t *testing.T) {
	original := &AruConfig{}

	clone := cloneConfig(original)
	if clone == nil {
		t.Fatal("cloneConfig returned nil")
	}

	if clone.RamDir != nil {
		t.Errorf("cloneConfig.RamDir should be nil when original is nil, got %+v", clone.RamDir)
	}
}

func TestRamDirNotCollected(t *testing.T) {
	cfg := &AruConfig{
		RamDir: []RamDirConfig{
			{Path: "<PORT1>", Size: "<PORT2>"},
		},
		Tmux: []TmuxWindowEntry{
			{Command: "echo <PORT1>"},
		},
	}

	nums := collectPortPlaceholders(cfg)
	if len(nums) != 1 || nums[0] != "1" {
		t.Errorf("collectPortPlaceholders = %v, want [\"1\"] — RamDir placeholders should not be collected", nums)
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

// TestCloneConfig_PreservesArrayOrder tests that cloneConfig preserves
// slice order for Tmux and Proxy slices.
func TestCloneConfig_PreservesArrayOrder(t *testing.T) {
	original := &AruConfig{
		Tmux: []TmuxWindowEntry{
			{Name: "first", Command: "echo 1"},
			{Name: "second", Command: "echo 2"},
			{Name: "third", Command: "echo 3"},
		},
		Proxy: []ProxyConfig{
			{Name: "proxy-a", Port: "3000"},
			{Name: "proxy-b", Port: "4000"},
		},
	}

	clone := cloneConfig(original)
	if clone == nil {
		t.Fatal("cloneConfig returned nil")
	}

	// Verify Tmux order preserved
	if len(clone.Tmux) != 3 {
		t.Fatalf("Tmux has %d entries, want 3", len(clone.Tmux))
	}
	if clone.Tmux[0].Name != "first" || clone.Tmux[1].Name != "second" || clone.Tmux[2].Name != "third" {
		t.Errorf("Tmux order not preserved: %+v", clone.Tmux)
	}

	// Verify Proxy order preserved
	if len(clone.Proxy) != 2 {
		t.Fatalf("Proxy has %d entries, want 2", len(clone.Proxy))
	}
	if clone.Proxy[0].Name != "proxy-a" || clone.Proxy[1].Name != "proxy-b" {
		t.Errorf("Proxy order not preserved: %+v", clone.Proxy)
	}
}

func TestResolvePlaceholders_PreservesSetupOneshot(t *testing.T) {
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
	if resolved.Worktree == nil {
		t.Fatal("resolved.Worktree is nil")
	}
	if !resolved.Worktree.SetupOneshot {
		t.Error("resolvePlaceholders dropped SetupOneshot — cloneConfig bug regression")
	}
}

// ── New test: ApplyPlaceholders for multiple tmux entries ──────────────────

func TestApplyPlaceholders_MultipleTmuxEntries(t *testing.T) {
	cfg := &AruConfig{
		Tmux: []TmuxWindowEntry{
			{Name: "dev", Command: "echo <PROJECT>:dev", Env: map[string]string{"PORT": "<PORT1>"}},
			{Name: "api", Command: "echo <PROJECT>:api", Env: map[string]string{"PORT": "<PORT2>"}},
		},
	}
	ports := map[int]int{1: 3000, 2: 4000}

	applyPlaceholders(cfg, "myapp", "main", ports, true)

	if len(cfg.Tmux) != 2 {
		t.Fatalf("Tmux has %d entries, want 2", len(cfg.Tmux))
	}
	if cfg.Tmux[0].Command != "echo myapp:dev" {
		t.Errorf("Tmux[0].Command = %q, want 'echo myapp:dev'", cfg.Tmux[0].Command)
	}
	if cfg.Tmux[0].Env["PORT"] != "3000" {
		t.Errorf("Tmux[0].Env[PORT] = %q, want '3000'", cfg.Tmux[0].Env["PORT"])
	}
	if cfg.Tmux[1].Command != "echo myapp:api" {
		t.Errorf("Tmux[1].Command = %q, want 'echo myapp:api'", cfg.Tmux[1].Command)
	}
	if cfg.Tmux[1].Env["PORT"] != "4000" {
		t.Errorf("Tmux[1].Env[PORT] = %q, want '4000'", cfg.Tmux[1].Env["PORT"])
	}
}

// ── New test: ApplyPlaceholders for multiple proxy entries ─────────────────

func TestApplyPlaceholders_MultiProxy(t *testing.T) {
	cfg := &AruConfig{
		Proxy: []ProxyConfig{
			{Name: "<PROJECT>-frontend", Port: "<PORT1>"},
			{Name: "<PROJECT>-api", Port: "<PORT2>"},
		},
	}
	ports := map[int]int{1: 3000, 2: 4000}

	applyPlaceholders(cfg, "myapp", "main", ports, true)

	if len(cfg.Proxy) != 2 {
		t.Fatalf("Proxy has %d entries, want 2", len(cfg.Proxy))
	}
	if cfg.Proxy[0].Name != "myapp-frontend" || cfg.Proxy[0].Port != "3000" {
		t.Errorf("Proxy[0] = %+v, want {Name: myapp-frontend, Port: 3000}", cfg.Proxy[0])
	}
	if cfg.Proxy[1].Name != "myapp-api" || cfg.Proxy[1].Port != "4000" {
		t.Errorf("Proxy[1] = %+v, want {Name: myapp-api, Port: 4000}", cfg.Proxy[1])
	}
}

func TestApplyPlaceholders_TmuxNameField(t *testing.T) {
	cfg := &AruConfig{
		Tmux: []TmuxWindowEntry{
			{Name: "<PROJECT>-<BRANCH>", Command: "bash"},
		},
	}
	ports := map[int]int{}

	applyPlaceholders(cfg, "myapp", "feature-x", ports, true)

	if len(cfg.Tmux) != 1 {
		t.Fatalf("Tmux has %d entries, want 1", len(cfg.Tmux))
	}
	if cfg.Tmux[0].Name != "myapp-feature-x" {
		t.Errorf("Tmux[0].Name = %q, want 'myapp-feature-x'", cfg.Tmux[0].Name)
	}
}

// ── resolveTeardownPlaceholders tests ──────────────────────────────────────

func TestResolveTeardownPlaceholders_NameOnly(t *testing.T) {
	cfg := &AruConfig{
		Worktree: &WorktreeConfig{
			Teardown: []string{"echo tearing down <PROJECT>/<BRANCH>"},
		},
		Proxy: []ProxyConfig{
			{Name: "app.<BRANCH>.<PROJECT>", Port: "<PORT1>"},
		},
	}

	resolved, err := resolveTeardownPlaceholders(cfg, "myproject", "feature-x")
	if err != nil {
		t.Fatalf("resolveTeardownPlaceholders() returned error: %v", err)
	}
	if resolved == nil {
		t.Fatal("resolveTeardownPlaceholders() returned nil")
	}

	if resolved.Worktree == nil {
		t.Fatal("resolved.Worktree is nil")
	}

	// PROJECT and BRANCH resolved
	if len(resolved.Worktree.Teardown) != 1 || resolved.Worktree.Teardown[0] != "echo tearing down myproject/feature-x" {
		t.Errorf("Teardown[0] = %q, want 'echo tearing down myproject/feature-x'", resolved.Worktree.Teardown[0])
	}

	// PORTn left as literal (not resolved)
	if len(resolved.Proxy) != 1 || resolved.Proxy[0].Port != "<PORT1>" {
		t.Errorf("Proxy[0].Port = %q, want '<PORT1>' (should be literal)", resolved.Proxy[0].Port)
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

	for num, port := range ports {
		if port < 1024 || port >= 10000 {
			t.Errorf("port %d for placeholder %d is out of range", port, num)
		}
	}
}
