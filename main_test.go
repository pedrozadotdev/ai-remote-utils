package main

import (
	"os"
	"path/filepath"
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
	expected := filepath.Join(home, ".tmp-file")
	if dir != expected {
		t.Errorf("expected %s, got %s", expected, dir)
	}
}
