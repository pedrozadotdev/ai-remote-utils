package main

import (
	"io/fs"
	"strings"
	"testing"
)

func TestStaticFS_LoadsIndexHTML(t *testing.T) {
	data, err := fs.ReadFile(staticFS, "index.html")
	if err != nil {
		t.Fatalf("failed to read index.html from embedded FS: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("index.html is empty")
	}
}

func TestStaticFS_ContainsDropzone(t *testing.T) {
	data, err := fs.ReadFile(staticFS, "index.html")
	if err != nil {
		t.Fatalf("failed to read index.html: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "dropzone") {
		t.Error("index.html missing 'dropzone' class or id")
	}
}

func TestStaticFS_ContainsUploadButton(t *testing.T) {
	data, err := fs.ReadFile(staticFS, "index.html")
	if err != nil {
		t.Fatalf("failed to read index.html: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "upload") && !strings.Contains(content, "Upload") {
		t.Error("index.html missing upload button reference")
	}
}

func TestStaticFS_ContainsClipboardAPI(t *testing.T) {
	data, err := fs.ReadFile(staticFS, "index.html")
	if err != nil {
		t.Fatalf("failed to read index.html: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "navigator.clipboard") {
		t.Error("index.html missing navigator.clipboard API call")
	}
}

func TestStaticFS_IsHTML(t *testing.T) {
	data, err := fs.ReadFile(staticFS, "index.html")
	if err != nil {
		t.Fatalf("failed to read index.html: %v", err)
	}
	content := string(data)

	if !strings.HasPrefix(strings.TrimSpace(content), "<") {
		t.Error("index.html does not appear to be HTML (doesn't start with <)")
	}
	if !strings.Contains(content, "</html>") {
		t.Error("index.html missing closing html tag")
	}
}

func TestStaticFS_IsDirectory(t *testing.T) {
	_, err := staticFS.Open(".")
	if err != nil {
		t.Fatalf("failed to open static root: %v", err)
	}
}

func TestStaticFS_NoSubDirs(t *testing.T) {
	err := fs.WalkDir(staticFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != "." {
			t.Errorf("unexpected subdirectory: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir error: %v", err)
	}
}
