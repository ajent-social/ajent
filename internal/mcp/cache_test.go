// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCacheMemoryKeyDeterministic(t *testing.T) {
	c := OpenCache(t.TempDir(), "terminal")
	scope := []string{"https://example.test", "agent-1", "group-1"}

	k1 := c.Key(scope, "read", map[string]any{"id": "1"})
	k2 := c.Key(scope, "read", map[string]any{"id": "1"})
	if k1 != k2 {
		t.Errorf("Key not deterministic: %q != %q", k1, k2)
	}
	if len(k1) != 64 {
		t.Errorf("Key length = %d, want 64 (lowercase hex SHA-256)", len(k1))
	}

	k3 := c.Key(scope, "read", map[string]any{"id": "2"})
	if k1 == k3 {
		t.Errorf("Key did not vary with a different value")
	}
	k4 := c.Key(scope, "preview", map[string]any{"id": "1"})
	if k1 == k4 {
		t.Errorf("Key did not vary with a different lane")
	}
}

func TestCacheMemorySeenRemember(t *testing.T) {
	c := OpenCache(t.TempDir(), "terminal")
	key := c.Key(nil, "read", "x")

	if c.Seen(key) {
		t.Fatalf("Seen(key) = true before Remember")
	}
	c.Remember(key)
	if !c.Seen(key) {
		t.Errorf("Seen(key) = false after Remember")
	}
	if c.Seen("never-remembered") {
		t.Errorf("Seen reported true for a key never remembered")
	}
}

func TestCacheMemoryNilSafe(t *testing.T) {
	var c *Cache

	if c.Seen("x") {
		t.Errorf("nil *Cache.Seen returned true")
	}
	c.Remember("x", "y") // must not panic
	if err := c.Close(); err != nil {
		t.Errorf("nil *Cache.Close() = %v, want nil", err)
	}
	if got := c.Key(nil, "read", "x"); got != "" {
		t.Errorf("nil *Cache.Key() = %q, want empty", got)
	}
}

// TestCachePersistsAcrossOpen covers the acceptance criterion that a
// second OpenCache on the same dir and profile sees a key remembered by
// the first, after Close.
func TestCachePersistsAcrossOpen(t *testing.T) {
	dir := t.TempDir()
	c1 := OpenCache(dir, "terminal")
	key := c1.Key(nil, "read", "x")
	c1.Remember(key)
	if err := c1.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}

	c2 := OpenCache(dir, "terminal")
	if !c2.Seen(key) {
		t.Errorf("second OpenCache did not see a key remembered before Close")
	}
}

// TestCacheExpiresAfter30Days covers the 30-day retention window: a key
// remembered at T0 must not be visible to a fresh open 31 days later.
func TestCacheExpiresAfter30Days(t *testing.T) {
	orig := cacheNow
	defer func() { cacheNow = orig }()

	dir := t.TempDir()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cacheNow = func() time.Time { return t0 }

	c1 := OpenCache(dir, "terminal")
	key := c1.Key(nil, "read", "x")
	c1.Remember(key)

	cacheNow = func() time.Time { return t0.Add(31 * 24 * time.Hour) }

	c2 := OpenCache(dir, "terminal")
	if c2.Seen(key) {
		t.Errorf("fresh open saw a key remembered 31 days ago, want expired")
	}
}

// TestCacheBoundedTo20000 covers the retention cap: once more than
// cacheMaxKeys entries have been remembered, only the newest cacheMaxKeys
// survive a Remember write.
func TestCacheBoundedTo20000(t *testing.T) {
	orig := cacheNow
	defer func() { cacheNow = orig }()

	dir := t.TempDir()
	c := OpenCache(dir, "terminal")

	older := make([]string, cacheMaxKeys)
	for i := range older {
		older[i] = c.Key(nil, "read", i)
	}
	c.Remember(older...)

	newer := make([]string, 5)
	for i := range newer {
		newer[i] = c.Key(nil, "read", []any{"overflow", i})
	}
	base := orig()
	cacheNow = func() time.Time { return base.Add(time.Minute) }
	c.Remember(newer...)

	fresh := OpenCache(dir, "terminal")

	for _, k := range newer {
		if !fresh.Seen(k) {
			t.Errorf("newest key %q missing after bounding to %d", k, cacheMaxKeys)
		}
	}

	survivors := 0
	for _, k := range older {
		if fresh.Seen(k) {
			survivors++
		}
	}
	wantSurvivors := cacheMaxKeys - len(newer)
	if survivors != wantSurvivors {
		t.Errorf("older survivors = %d, want %d", survivors, wantSurvivors)
	}
}

// TestCacheConcurrentWritersMerge covers the re-read-before-write rule: two
// caches opened on the same profile, each remembering a distinct key, must
// not clobber each other's write.
func TestCacheConcurrentWritersMerge(t *testing.T) {
	dir := t.TempDir()
	c1 := OpenCache(dir, "terminal")
	c2 := OpenCache(dir, "terminal")

	k1 := c1.Key(nil, "read", "a")
	k2 := c2.Key(nil, "read", "b")

	c1.Remember(k1)
	c2.Remember(k2)

	c3 := OpenCache(dir, "terminal")
	if !c3.Seen(k1) {
		t.Errorf("third open did not see the key remembered by the first writer")
	}
	if !c3.Seen(k2) {
		t.Errorf("third open did not see the key remembered by the second writer")
	}
}

// TestCacheCorruptFileDegrades covers step 3/4 of the contract: a corrupt
// cache file never panics OpenCache, Seen or Remember, and Remember heals
// the file back to valid JSON.
func TestCacheCorruptFileDegrades(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(%s): %v", cacheDir, err)
	}
	dataPath := filepath.Join(cacheDir, "terminal.json")
	if err := os.WriteFile(dataPath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", dataPath, err)
	}

	c := OpenCache(dir, "terminal")
	key := c.Key(nil, "read", "x")
	if c.Seen(key) {
		t.Errorf("Seen(key) = true on a fresh cache backed by a corrupt file")
	}
	c.Remember(key) // must not panic

	data, err := os.ReadFile(dataPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) after Remember: %v", dataPath, err)
	}
	var cf cacheFile
	if err := json.Unmarshal(data, &cf); err != nil {
		t.Fatalf("cache file is not valid JSON after Remember: %v\ncontent: %s", err, data)
	}
	if cf.Version != cacheFileVersion {
		t.Errorf("cache file version = %d, want %d", cf.Version, cacheFileVersion)
	}
	if _, ok := cf.Entries[key]; !ok {
		t.Errorf("remembered key missing from the healed cache file")
	}
}

// TestCacheFileModeUnix covers the owner-only permissions the contract
// requires: 0700 on the cache directory, 0600 on the cache file.
func TestCacheFileModeUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}

	dir := t.TempDir()
	c := OpenCache(dir, "terminal")
	c.Remember(c.Key(nil, "read", "x"))

	cacheDir := filepath.Join(dir, "cache")
	dirInfo, err := os.Stat(cacheDir)
	if err != nil {
		t.Fatalf("Stat(%s): %v", cacheDir, err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Errorf("cache dir mode = %o, want 0700", mode)
	}

	dataPath := filepath.Join(cacheDir, "terminal.json")
	fileInfo, err := os.Stat(dataPath)
	if err != nil {
		t.Fatalf("Stat(%s): %v", dataPath, err)
	}
	if mode := fileInfo.Mode().Perm(); mode != 0o600 {
		t.Errorf("cache file mode = %o, want 0600", mode)
	}
}

// TestCacheSymlinkRefused covers step 3's symlink refusal: when the cache
// file path is a symlink, the pointed-at file is never read or modified.
func TestCacheSymlinkRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}

	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(%s): %v", cacheDir, err)
	}

	target := filepath.Join(t.TempDir(), "real.json")
	original := []byte(`{"version":1,"entries":{}}`)
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", target, err)
	}

	link := filepath.Join(cacheDir, "terminal.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink(%s, %s): %v", target, link, err)
	}

	c := OpenCache(dir, "terminal")
	if c.Seen(c.Key(nil, "read", "x")) {
		t.Errorf("Seen reported true from an unread symlinked cache")
	}
	c.Remember(c.Key(nil, "read", "x"))

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", target, err)
	}
	if string(got) != string(original) {
		t.Errorf("symlink target modified: got %q, want %q", got, original)
	}
}

// TestCacheInvalidProfileMemoryOnly covers step 1: a profile name that
// fails the grammar never touches disk, but the cache still works
// memory-only.
func TestCacheInvalidProfileMemoryOnly(t *testing.T) {
	dir := t.TempDir()
	const badProfile = "Not_A_Valid-Profile"

	c := OpenCache(dir, badProfile)
	key := c.Key(nil, "read", "x")
	if c.Seen(key) {
		t.Fatalf("Seen(key) = true before Remember")
	}
	c.Remember(key)
	if !c.Seen(key) {
		t.Errorf("Seen(key) = false after Remember on an invalid-profile cache")
	}

	if _, err := os.Stat(filepath.Join(dir, "cache")); err == nil {
		t.Errorf("cache directory was created for an invalid profile")
	} else if !os.IsNotExist(err) {
		t.Fatalf("Stat cache dir: %v", err)
	}

	c2 := OpenCache(dir, badProfile)
	if c2.Seen(key) {
		t.Errorf("a fresh cache with an invalid profile unexpectedly saw a persisted key")
	}
}
