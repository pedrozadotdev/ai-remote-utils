package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ProxyDB manages a collection of named proxy entries persisted as JSON.
// Thread-safe via sync.RWMutex.
type ProxyDB struct {
	mu      sync.RWMutex
	path    string
	proxies map[string]int
	mtime   time.Time
}

// proxyFile is the on-disk JSON structure.
type proxyFile struct {
	Version int            `json:"version"`
	Proxies map[string]int `json:"proxies"`
}

// LoadProxyDB reads proxy entries from a JSON file.
// If the file does not exist, an empty ProxyDB is returned with no error.
func LoadProxyDB(path string) (*ProxyDB, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ProxyDB{
				path:    path,
				proxies: make(map[string]int),
			}, nil
		}
		return nil, fmt.Errorf("failed to read proxy DB: %w", err)
	}

	var pf proxyFile
	if err := json.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("failed to parse proxy DB: %w", err)
	}

	if pf.Proxies == nil {
		pf.Proxies = make(map[string]int)
	}

	// Validate entries — silently skip invalid port ranges and blocked ports
	validProxies := make(map[string]int, len(pf.Proxies))
	for name, port := range pf.Proxies {
		if err := validatePort(port); err != nil {
			slog.Warn("skipping invalid proxy entry on load", "name", name, "port", port, "error", err)
			continue
		}
		validProxies[name] = port
	}

	fi, err := os.Stat(path)
	var mtime time.Time
	if err == nil {
		mtime = fi.ModTime()
	}

	return &ProxyDB{
		path:    path,
		proxies: validProxies,
		mtime:   mtime,
	}, nil
}

// saveLocked writes the current proxy state to disk.
// Must be called with db.mu already held (write lock).
func (db *ProxyDB) saveLocked(proxies map[string]int) error {
	pf := proxyFile{
		Version: 1,
		Proxies: proxies,
	}

	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal proxy DB: %w", err)
	}

	// Ensure parent directory exists (important for CLI subcommands)
	if err := os.MkdirAll(filepath.Dir(db.path), 0755); err != nil {
		return fmt.Errorf("failed to create proxy DB directory: %w", err)
	}

	if err := os.WriteFile(db.path, data, 0644); err != nil {
		return fmt.Errorf("failed to write proxy DB: %w", err)
	}

	// Update mtime after successful write
	fi, err := os.Stat(db.path)
	if err == nil {
		db.mtime = fi.ModTime()
	}

	return nil
}

// Save writes the current proxy entries to disk.
func (db *ProxyDB) Save() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.saveLocked(db.proxies)
}

// Add adds or overwrites a proxy entry. Validates name and port.
// Write-through: saves to disk before updating in-memory state.
// If the save fails, the in-memory map is NOT updated.
func (db *ProxyDB) Add(name string, port int) error {
	if err := validateName(name); err != nil {
		return err
	}
	if err := validatePort(port); err != nil {
		return err
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	// Build new map with the added entry
	newProxies := make(map[string]int, len(db.proxies)+1)
	for k, v := range db.proxies {
		newProxies[k] = v
	}
	newProxies[name] = port

	// Write-through: save to disk first
	if err := db.saveLocked(newProxies); err != nil {
		return err
	}

	// Only update in-memory map after successful disk write
	db.proxies = newProxies
	return nil
}

// Delete removes a proxy entry. Returns error if the entry does not exist.
// Write-through: saves to disk before updating in-memory state.
func (db *ProxyDB) Delete(name string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if _, ok := db.proxies[name]; !ok {
		return fmt.Errorf("proxy entry %q not found", name)
	}

	// Build new map without the deleted entry
	newProxies := make(map[string]int, len(db.proxies)-1)
	for k, v := range db.proxies {
		if k != name {
			newProxies[k] = v
		}
	}

	// Write-through: save to disk first
	if err := db.saveLocked(newProxies); err != nil {
		return err
	}

	db.proxies = newProxies
	return nil
}

// Get looks up a port by name. Returns (port, true) if found, (0, false) otherwise.
// Thread-safe.
func (db *ProxyDB) Get(name string) (int, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	port, ok := db.proxies[name]
	return port, ok
}

// List returns a copy of all proxy entries. Modifying the returned map
// does not affect the DB.
func (db *ProxyDB) List() map[string]int {
	db.mu.RLock()
	defer db.mu.RUnlock()

	result := make(map[string]int, len(db.proxies))
	for k, v := range db.proxies {
		result[k] = v
	}
	return result
}

// Len returns the number of proxy entries.
func (db *ProxyDB) Len() int {
	db.mu.RLock()
	defer db.mu.RUnlock()

	return len(db.proxies)
}

// Refresh checks if the proxy DB file has been modified externally
// (e.g., by the CLI subcommand or manual edit). If so, it reloads
// the file from disk and replaces the in-memory state.
// Returns true if the data was reloaded.
func (db *ProxyDB) Refresh() bool {
	db.mu.Lock()
	defer db.mu.Unlock()

	fi, err := os.Stat(db.path)
	if err != nil {
		return false
	}

	if fi.ModTime().IsZero() || !fi.ModTime().After(db.mtime) {
		return false
	}

	data, err := os.ReadFile(db.path)
	if err != nil {
		slog.Warn("proxy DB: failed to read file on refresh", "path", db.path, "error", err)
		return false
	}

	var pf proxyFile
	if err := json.Unmarshal(data, &pf); err != nil {
		slog.Warn("proxy DB: failed to parse JSON on refresh", "path", db.path, "error", err)
		return false
	}

	if pf.Proxies == nil {
		pf.Proxies = make(map[string]int)
	}

	// Validate entries — silently skip invalid port ranges and blocked ports
	validProxies := make(map[string]int, len(pf.Proxies))
	for name, port := range pf.Proxies {
		if err := validatePort(port); err != nil {
			slog.Warn("skipping invalid proxy entry on refresh", "name", name, "port", port, "error", err)
			continue
		}
		validProxies[name] = port
	}

	db.proxies = validProxies
	db.mtime = fi.ModTime()
	return true
}

// ---- Validation ----

var (
	errReservedName   = fmt.Errorf("proxy name is reserved")
	errNumericName    = fmt.Errorf("proxy name must not be purely numeric")
	errInvalidChars   = fmt.Errorf("proxy name contains invalid characters")
	errNameLength     = fmt.Errorf("proxy name must be 1-63 characters")
	errPortOutOfRange = fmt.Errorf("port must be between 1 and 65535")
)

func validateName(name string) error {
	if len(name) < 1 || len(name) > 63 {
		return errNameLength
	}
	if name == "tmp" || name == "test" {
		return errReservedName
	}
	for _, r := range name {
		if !isValidNameChar(r) {
			return errInvalidChars
		}
	}
	if isNumeric(name) {
		return errNumericName
	}
	return nil
}

func isValidNameChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.'
}

func isNumeric(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validatePort(port int) error {
	if port < 1 || port > 65535 {
		return errPortOutOfRange
	}
	if blockedProxyPorts[port] {
		return fmt.Errorf("port %d is blocked (53, 80, 443)", port)
	}
	return nil
}
