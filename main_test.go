package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLookupEnvInt_Default(t *testing.T) {
	// Ensure env is unset
	os.Unsetenv("TEST_VAR")
	v := lookupEnvInt("TEST_VAR", 42)
	if v != 42 {
		t.Errorf("expected 42, got %d", v)
	}
}

func TestLookupEnvInt_FromEnv(t *testing.T) {
	os.Setenv("TEST_VAR_INT", "99")
	defer os.Unsetenv("TEST_VAR_INT")

	v := lookupEnvInt("TEST_VAR_INT", 42)
	if v != 99 {
		t.Errorf("expected 99, got %d", v)
	}
}

func TestLookupEnvInt_InvalidEnv(t *testing.T) {
	os.Setenv("TEST_VAR_INVALID", "not-a-number")
	defer os.Unsetenv("TEST_VAR_INVALID")

	v := lookupEnvInt("TEST_VAR_INVALID", 42)
	if v != 42 {
		t.Errorf("expected 42 (fallback), got %d", v)
	}
}

func TestLookupEnvStr_Default(t *testing.T) {
	os.Unsetenv("TEST_STR")
	v := lookupEnvStr("TEST_STR", "default")
	if v != "default" {
		t.Errorf("expected 'default', got '%s'", v)
	}
}

func TestLookupEnvStr_FromEnv(t *testing.T) {
	os.Setenv("TEST_STR_VAL", "from-env")
	defer os.Unsetenv("TEST_STR_VAL")

	v := lookupEnvStr("TEST_STR_VAL", "default")
	if v != "from-env" {
		t.Errorf("expected 'from-env', got '%s'", v)
	}
}

func TestLookupEnvStr_EmptyEnv(t *testing.T) {
	os.Setenv("TEST_STR_EMPTY", "")
	defer os.Unsetenv("TEST_STR_EMPTY")

	v := lookupEnvStr("TEST_STR_EMPTY", "fallback")
	if v != "fallback" {
		t.Errorf("expected 'fallback', got '%s'", v)
	}
}

func TestDefaultCertDir(t *testing.T) {
	dir := defaultCertDir()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(home, ".aru")
	if dir != expected {
		t.Errorf("expected %s, got %s", expected, dir)
	}
}

func TestGenerateServiceFile_ContainsBinPath(t *testing.T) {
	content := GenerateServiceFile("/usr/local/bin/ai-remote-utils", "/usr/local/share/ai-remote-utils")
	if !strings.Contains(content, "/usr/local/bin/ai-remote-utils") {
		t.Error("service file missing ExecStart path")
	}
	if !strings.Contains(content, "/usr/local/share/ai-remote-utils") {
		t.Error("service file missing WorkingDirectory")
	}
}

func TestGenerateServiceFile_HasRequiredKeys(t *testing.T) {
	content := GenerateServiceFile("/usr/local/bin/ai-remote-utils", "/usr/local/share/ai-remote-utils")
	required := []string{"[Unit]", "Description=", "After=network.target", "[Service]",
		"ExecStart=", "WorkingDirectory=", "Restart=on-failure", "[Install]", "WantedBy="}
	for _, key := range required {
		if !strings.Contains(content, key) {
			t.Errorf("service file missing %q", key)
		}
	}
}

func TestInstallService_WritesToTargetDir(t *testing.T) {
	targetDir := t.TempDir()
	err := InstallService("/usr/local/bin/ai-remote-utils", "/usr/local/share/ai-remote-utils", targetDir)
	if err != nil {
		t.Fatalf("InstallService error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(targetDir, "ai-remote-utils.service"))
	if err != nil {
		t.Fatalf("failed to read service file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "/usr/local/bin/ai-remote-utils") {
		t.Error("written file missing ExecStart")
	}
}

func TestInstallService_CreatesDir(t *testing.T) {
	// Target parent dir doesn't exist — should be created
	targetDir := filepath.Join(t.TempDir(), "nonexistent", "subdir")
	err := InstallService("/test/bin", "/test/share", targetDir)
	if err != nil {
		t.Fatalf("InstallService error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(targetDir, "ai-remote-utils.service")); os.IsNotExist(err) {
		t.Error("service file was not created")
	}
}
