package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Character set for random alphanumeric names
const alphanumChars = "abcdefghijklmnopqrstuvwxyz0123456789"

// uploadResponse is the JSON response returned by the upload handler.
type uploadResponse struct {
	Path  string   `json:"path,omitempty"`
	Paths []string `json:"paths,omitempty"`
	Error string   `json:"error,omitempty"`
}

// NameGenerator generates unique random filenames in a directory.
// It is safe for concurrent use.
type NameGenerator struct {
	mu sync.Mutex
}

// Generate creates a new file at dir with a random 4-char alphanumeric name
// and the given extension. Returns the full path. Retries up to 10 times
// on collision. The file is created empty on disk to claim the name.
func (ng *NameGenerator) Generate(dir string, ext string) (string, error) {
	ng.mu.Lock()
	defer ng.mu.Unlock()

	for i := 0; i < 10; i++ {
		name := RandomAlphanum(4) + ext
		path := filepath.Join(dir, name)

		// Check if file exists
		if _, err := os.Stat(path); err == nil {
			continue // collision, retry
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("failed to stat %s: %w", path, err)
		}

		// Create the file to claim the name
		f, err := os.Create(path)
		if err != nil {
			return "", fmt.Errorf("failed to create %s: %w", path, err)
		}
		f.Close()
		return path, nil
	}

	return "", fmt.Errorf("failed to generate unique name after 10 retries")
}

// RandomAlphanum returns a random string of length n using lowercase
// alphanumeric characters (a-z, 0-9).
func RandomAlphanum(n int) string {
	result := make([]byte, n)
	for i := range result {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphanumChars))))
		if err != nil {
			// Fallback to a simple non-crypto approach if crypto/rand fails
			idx = big.NewInt(int64(i * 7 % len(alphanumChars)))
		}
		result[i] = alphanumChars[idx.Int64()]
	}
	return string(result)
}

// UploadHandler handles file upload requests.
type UploadHandler struct {
	BaseDir string
	MaxSize int64
	NameGen *NameGenerator
}

// ServeHTTP handles POST /upload requests.
func (h *UploadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, uploadResponse{Error: "method not allowed"})
		return
	}

	// Enforce max file size
	r.Body = http.MaxBytesReader(w, r.Body, h.MaxSize)

	// Parse multipart form
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			writeJSON(w, http.StatusRequestEntityTooLarge, uploadResponse{Error: "file too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, uploadResponse{Error: fmt.Sprintf("failed to parse form: %v", err)})
		return
	}

	// Collect all file headers from both "file" and "files" fields
	var fileHeaders []*multipart.FileHeader
	fileHeaders = append(fileHeaders, r.MultipartForm.File["file"]...)
	fileHeaders = append(fileHeaders, r.MultipartForm.File["files"]...)

	if len(fileHeaders) == 0 {
		writeJSON(w, http.StatusBadRequest, uploadResponse{Error: "no file provided"})
		return
	}

	// Process each file
	var paths []string
	for _, fh := range fileHeaders {
		file, err := fh.Open()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, uploadResponse{Error: fmt.Sprintf("failed to open uploaded file: %v", err)})
			return
		}

		// Determine extension from original filename
		ext := filepath.Ext(fh.Filename)
		if ext == "" {
			ext = ".bin"
		}

		// Generate unique filename
		path, err := h.NameGen.Generate(h.BaseDir, ext)
		if err != nil {
			file.Close()
			writeJSON(w, http.StatusInternalServerError, uploadResponse{Error: fmt.Sprintf("could not generate unique filename: %v", err)})
			return
		}

		// Write file content
		dst, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			file.Close()
			writeJSON(w, http.StatusInternalServerError, uploadResponse{Error: fmt.Sprintf("failed to open file: %v", err)})
			return
		}

		written, err := io.Copy(dst, file)
		dst.Close()
		file.Close()
		if err != nil {
			os.Remove(path) // cleanup partial file
			writeJSON(w, http.StatusInternalServerError, uploadResponse{Error: fmt.Sprintf("failed to write file: %v", err)})
			return
		}

		slog.Info("file uploaded",
			"path", path,
			"size", written,
			"original_name", fh.Filename,
		)

		paths = append(paths, "@"+path)
	}

	// Return single path for one file, paths array for multiple
	if len(paths) == 1 {
		writeJSON(w, http.StatusOK, uploadResponse{Path: paths[0]})
	} else {
		writeJSON(w, http.StatusOK, uploadResponse{Paths: paths})
	}
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
