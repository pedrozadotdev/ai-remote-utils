package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// testProxyDB creates a ProxyDB backed by a temp file, with optional initial entries.
func testProxyDB(t *testing.T, entries map[string]int) *ProxyDB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "proxies.json")

	if entries != nil {
		data, err := json.MarshalIndent(struct {
			Version int            `json:"version"`
			Proxies map[string]int `json:"proxies"`
		}{Version: 1, Proxies: entries}, "", "  ")
		if err != nil {
			t.Fatalf("failed to marshal test entries: %v", err)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}
	}

	db, err := LoadProxyDB(path)
	if err != nil {
		t.Fatalf("LoadProxyDB error = %v", err)
	}
	return db
}

// ---- LoadProxyDB ----

func TestLoadProxyDB_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxies.json")

	data := `{
		"version": 1,
		"proxies": {
			"myapp": 3000,
			"api": 8080
		}
	}`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	db, err := LoadProxyDB(path)
	if err != nil {
		t.Fatalf("LoadProxyDB error = %v", err)
	}

	if port, ok := db.Get("myapp"); !ok || port != 3000 {
		t.Errorf("Get('myapp') = (%d, %v), want (3000, true)", port, ok)
	}
	if port, ok := db.Get("api"); !ok || port != 8080 {
		t.Errorf("Get('api') = (%d, %v), want (8080, true)", port, ok)
	}
}

func TestLoadProxyDB_NonExistent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	db, err := LoadProxyDB(path)
	if err != nil {
		t.Fatalf("LoadProxyDB error for non-existent file: %v", err)
	}

	if db.Len() != 0 {
		t.Errorf("expected empty DB, got %d entries", db.Len())
	}
}

func TestLoadProxyDB_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxies.json")

	if err := os.WriteFile(path, []byte("{invalid json"), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	_, err := LoadProxyDB(path)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("expected 'parse' in error, got %q", err.Error())
	}
}

func TestLoadProxyDB_EmptyProxies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxies.json")

	data := `{"version": 1, "proxies": null}`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	db, err := LoadProxyDB(path)
	if err != nil {
		t.Fatalf("LoadProxyDB error = %v", err)
	}

	if db.Len() != 0 {
		t.Errorf("expected empty DB for null proxies, got %d entries", db.Len())
	}
}

// ---- Add ----

func TestProxyDB_Add_Valid(t *testing.T) {
	db := testProxyDB(t, nil)

	if err := db.Add("myapp", 3000); err != nil {
		t.Fatalf("Add error = %v", err)
	}

	if port, ok := db.Get("myapp"); !ok || port != 3000 {
		t.Errorf("Get('myapp') = (%d, %v), want (3000, true)", port, ok)
	}
}

func TestProxyDB_Add_DuplicateOverwrites(t *testing.T) {
	db := testProxyDB(t, map[string]int{"myapp": 3000})

	if err := db.Add("myapp", 8080); err != nil {
		t.Fatalf("Add duplicate error = %v", err)
	}

	if port, ok := db.Get("myapp"); !ok || port != 8080 {
		t.Errorf("Get('myapp') = (%d, %v), want (8080, true)", port, ok)
	}
}

func TestProxyDB_Add_InvalidName(t *testing.T) {
	tests := []struct {
		name    string
		port    int
		wantErr string
	}{
		{"tmp", 3000, "reserved"},
		{"test", 3000, "reserved"},
		{"3000", 3000, "numeric"},
		{"", 3000, "1-63"},
		{strings.Repeat("a", 64), 3000, "1-63"},
		{"my app", 3000, "char"},
		{"my\tapp", 3000, "char"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := testProxyDB(t, nil)
			err := db.Add(tc.name, tc.port)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestProxyDB_Add_InvalidPort(t *testing.T) {
	tests := []struct {
		name    string
		port    int
		wantErr string
	}{
		{"myapp", 0, "between 1 and 65535"},
		{"myapp", -1, "between 1 and 65535"},
		{"myapp", 65536, "between 1 and 65535"},
		{"myapp", 53, "blocked"},
		{"myapp", 80, "blocked"},
		{"myapp", 443, "blocked"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := testProxyDB(t, nil)
			err := db.Add(tc.name, tc.port)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestProxyDB_Add_WriteThroughFailure(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "proxies")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatalf("MkdirAll error: %v", err)
	}
	path := filepath.Join(subdir, "proxies.json")

	db, err := LoadProxyDB(path)
	if err != nil {
		t.Fatalf("LoadProxyDB error = %v", err)
	}

	// Create an entry to prove the DB works
	if err := db.Add("existing", 1111); err != nil {
		t.Fatalf("initial Add error = %v", err)
	}

	// Now replace the subdir with a regular file to make subsequent writes fail
	if err := os.RemoveAll(subdir); err != nil {
		t.Fatalf("RemoveAll error: %v", err)
	}
	if err := os.WriteFile(subdir, []byte("not a directory"), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	// Attempt to add — should fail because "proxies" is now a file, not a directory
	err = db.Add("myapp", 3000)
	if err == nil {
		t.Fatal("expected error for invalid parent path, got nil")
	}

	// Verify the in-memory map was NOT updated (write-through protection)
	if port, ok := db.Get("myapp"); ok {
		t.Errorf("entry was added despite save failure: myapp=%d", port)
	}
	// Original entry should still exist
	if port, ok := db.Get("existing"); !ok || port != 1111 {
		t.Errorf("existing entry affected: Get('existing') = (%d, %v), want (1111, true)", port, ok)
	}
}

// ---- Delete ----

func TestProxyDB_Delete_Existing(t *testing.T) {
	db := testProxyDB(t, map[string]int{"myapp": 3000, "api": 8080})

	if err := db.Delete("myapp"); err != nil {
		t.Fatalf("Delete error = %v", err)
	}

	if _, ok := db.Get("myapp"); ok {
		t.Error("Get('myapp') returned ok=true after delete")
	}
	// Other entries unaffected
	if port, ok := db.Get("api"); !ok || port != 8080 {
		t.Errorf("Get('api') = (%d, %v), want (8080, true)", port, ok)
	}
}

func TestProxyDB_Delete_NonExistent(t *testing.T) {
	db := testProxyDB(t, map[string]int{"myapp": 3000})

	err := db.Delete("nonexistent")
	if err == nil {
		t.Fatal("expected error for deleting non-existent entry, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want it to contain 'not found'", err.Error())
	}
}

// ---- Get ----

func TestProxyDB_Get_Existing(t *testing.T) {
	db := testProxyDB(t, map[string]int{"myapp": 3000})

	port, ok := db.Get("myapp")
	if !ok {
		t.Fatal("Get('myapp') returned ok=false")
	}
	if port != 3000 {
		t.Errorf("port = %d, want 3000", port)
	}
}

func TestProxyDB_Get_NonExistent(t *testing.T) {
	db := testProxyDB(t, nil)

	port, ok := db.Get("nonexistent")
	if ok {
		t.Fatal("Get('nonexistent') returned ok=true")
	}
	if port != 0 {
		t.Errorf("port = %d, want 0", port)
	}
}

func TestProxyDB_Get_CaseSensitive(t *testing.T) {
	db := testProxyDB(t, map[string]int{"MyApp": 3000})

	// Exact case works
	if port, ok := db.Get("MyApp"); !ok || port != 3000 {
		t.Errorf("Get('MyApp') = (%d, %v), want (3000, true)", port, ok)
	}
	// Wrong case does not match
	if port, ok := db.Get("myapp"); ok {
		t.Errorf("Get('myapp') = (%d, %v), want (0, false)", port, ok)
	}
}

// ---- List ----

func TestProxyDB_List_ReturnsCopy(t *testing.T) {
	db := testProxyDB(t, map[string]int{"myapp": 3000, "api": 8080})

	list := db.List()
	if len(list) != 2 {
		t.Fatalf("List() returned %d entries, want 2", len(list))
	}

	// Modify the returned map
	list["newentry"] = 9999

	// Original DB should be unaffected
	if _, ok := db.Get("newentry"); ok {
		t.Error("modifying List() result modified the DB")
	}
}

func TestProxyDB_List_Empty(t *testing.T) {
	db := testProxyDB(t, nil)

	list := db.List()
	if list == nil {
		t.Fatal("List() returned nil, want empty map")
	}
	if len(list) != 0 {
		t.Errorf("List() returned %d entries, want 0", len(list))
	}
}

// ---- Save / Persistence ----

func TestProxyDB_SaveAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxies.json")

	db, err := LoadProxyDB(path)
	if err != nil {
		t.Fatalf("LoadProxyDB error = %v", err)
	}

	if err := db.Add("myapp", 3000); err != nil {
		t.Fatalf("Add error = %v", err)
	}
	if err := db.Add("api", 8080); err != nil {
		t.Fatalf("Add error = %v", err)
	}

	// Reload from disk
	db2, err := LoadProxyDB(path)
	if err != nil {
		t.Fatalf("LoadProxyDB (reload) error = %v", err)
	}

	if port, ok := db2.Get("myapp"); !ok || port != 3000 {
		t.Errorf("after reload: Get('myapp') = (%d, %v), want (3000, true)", port, ok)
	}
	if port, ok := db2.Get("api"); !ok || port != 8080 {
		t.Errorf("after reload: Get('api') = (%d, %v), want (8080, true)", port, ok)
	}
	if db2.Len() != 2 {
		t.Errorf("after reload: Len = %d, want 2", db2.Len())
	}
}

func TestProxyDB_DeletePersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxies.json")

	db, err := LoadProxyDB(path)
	if err != nil {
		t.Fatalf("LoadProxyDB error = %v", err)
	}
	db.Add("myapp", 3000)
	db.Add("api", 8080)

	if err := db.Delete("myapp"); err != nil {
		t.Fatalf("Delete error = %v", err)
	}

	db2, err := LoadProxyDB(path)
	if err != nil {
		t.Fatalf("LoadProxyDB error = %v", err)
	}

	if db2.Len() != 1 {
		t.Errorf("after delete+reload: Len = %d, want 1", db2.Len())
	}
	if _, ok := db2.Get("myapp"); ok {
		t.Error("deleted entry still present after reload")
	}
}

// ---- Refresh (hot-reload) ----

func TestProxyDB_Refresh_DetectsExternalChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxies.json")

	db, err := LoadProxyDB(path)
	if err != nil {
		t.Fatalf("LoadProxyDB error = %v", err)
	}
	db.Add("original", 1111)

	// Simulate external modification (direct file write)
	// Sleep to ensure mtime differs (filesystem coarseness)
	time.Sleep(50 * time.Millisecond)
	data := `{"version":1,"proxies":{"external":9999}}`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	// Without Refresh, DB should still have old data
	if port, ok := db.Get("original"); !ok || port != 1111 {
		t.Errorf("before refresh: Get('original') = (%d, %v), want (1111, true)", port, ok)
	}

	// Refresh should detect the change
	refreshed := db.Refresh()
	if !refreshed {
		t.Fatal("Refresh() returned false, expected true (file changed)")
	}

	// After refresh, new entry should be visible
	if port, ok := db.Get("external"); !ok || port != 9999 {
		t.Errorf("after refresh: Get('external') = (%d, %v), want (9999, true)", port, ok)
	}
	// Old entry should be gone (file was replaced)
	if _, ok := db.Get("original"); ok {
		t.Error("after refresh: Get('original') returned ok=true, expected gone")
	}
}

func TestProxyDB_Refresh_NoChange(t *testing.T) {
	db := testProxyDB(t, map[string]int{"myapp": 3000})

	// Wait a tiny bit to ensure mtime would be different if there were a change
	time.Sleep(10 * time.Millisecond)

	// Refresh without any file change
	refreshed := db.Refresh()
	if refreshed {
		t.Fatal("Refresh() returned true even though file didn't change")
	}
}

func TestProxyDB_Refresh_NonExistentFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	db, err := LoadProxyDB(path)
	if err != nil {
		t.Fatalf("LoadProxyDB error = %v", err)
	}

	// Refresh when file doesn't exist
	refreshed := db.Refresh()
	if refreshed {
		t.Fatal("Refresh() returned true when file doesn't exist")
	}
}

// ---- Len ----

func TestProxyDB_Len(t *testing.T) {
	tests := []struct {
		name    string
		entries map[string]int
		wantLen int
	}{
		{"empty", nil, 0},
		{"one", map[string]int{"a": 1}, 1},
		{"multiple", map[string]int{"a": 1, "b": 2, "c": 3}, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := testProxyDB(t, tc.entries)
			if db.Len() != tc.wantLen {
				t.Errorf("Len() = %d, want %d", db.Len(), tc.wantLen)
			}
		})
	}
}

// ---- Concurrent safety (race detector) ----

func TestProxyDB_ConcurrentSafety(t *testing.T) {
	db := testProxyDB(t, nil)

	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := string(rune('a' + i))
			db.Add(name, 1000+i)
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db.Get("a")
			db.List()
			db.Len()
		}()
	}

	wg.Wait()

	// All entries should be present
	if db.Len() != 10 {
		t.Errorf("expected 10 entries after concurrent add, got %d", db.Len())
	}
}

func TestProxyDB_ConcurrentAddAndDelete(t *testing.T) {
	db := testProxyDB(t, nil)

	// Pre-populate
	for i := 0; i < 5; i++ {
		name := string(rune('a' + i))
		db.Add(name, 1000+i)
	}

	var wg sync.WaitGroup

	// Concurrent adds and deletes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := string(rune('a' + i))
			db.Delete(name)
		}(i)
	}

	for i := 5; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := string(rune('a' + i))
			db.Add(name, 1000+i)
		}(i)
	}

	wg.Wait()
}

// ---- JSON format verification ----

func TestProxyDB_JSONFormat(t *testing.T) {
	// Verify the written JSON has the expected structure
	dir := t.TempDir()
	path := filepath.Join(dir, "proxies.json")

	db, err := LoadProxyDB(path)
	if err != nil {
		t.Fatalf("LoadProxyDB error = %v", err)
	}
	db.Add("myapp", 3000)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}

	var parsed struct {
		Version int            `json:"version"`
		Proxies map[string]int `json:"proxies"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("JSON unmarshal error: %v", err)
	}

	if parsed.Version != 1 {
		t.Errorf("version = %d, want 1", parsed.Version)
	}
	if port, ok := parsed.Proxies["myapp"]; !ok || port != 3000 {
		t.Errorf("proxies.myapp = (%d, %v), want (3000, true)", port, ok)
	}
}
