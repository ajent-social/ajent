// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/ajent-social/ajent-client/internal/config"
)

// cacheMaxKeys is the maximum number of delivery-receipt entries the
// persisted cache file keeps. Remember trims to the newest cacheMaxKeys
// entries by timestamp, ties broken by key, ascending.
const cacheMaxKeys = 20000

// cacheTTL is how long a delivery-receipt entry is kept. Entries older than
// this are dropped on load and on every Remember write.
const cacheTTL = 30 * 24 * time.Hour

// cacheLockTimeout bounds how long OpenCache and Remember wait for the
// per-profile file lock before degrading to memory-only for that call.
const cacheLockTimeout = 1 * time.Second

// cacheFileVersion is the only on-disk format version this client
// understands. A file with any other version is treated as an error.
const cacheFileVersion = 1

// cacheProfilePattern is the set of profile names allowed to have a
// persisted cache file. It matches the profile-name grammar used
// elsewhere in the client, and keeps the derived file and lock paths safe.
var cacheProfilePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,39}$`)

// cacheNow returns the current time. Tests override it to control
// expiry and ordering.
var cacheNow = time.Now

// cacheFile is the on-disk JSON representation of the persisted cache:
// hex delivery-receipt keys mapped to the unix-seconds timestamp they were
// last remembered. It never carries anything else: no post content, no
// queries, no credentials.
type cacheFile struct {
	Version int              `json:"version"`
	Entries map[string]int64 `json:"entries"`
}

// filestore is the file-backed persistence layer for the delivery cache.
// It is stateless between calls: every read and write goes to disk, under
// the per-profile lock, so concurrent writers merge instead of clobbering.
type filestore struct {
	dataPath string // <configDir>/cache/<profile>.json
	lockPath string // <configDir>/cache/<profile>.lock
}

// load reads and parses the persisted cache file. Any read error (missing
// file, symlink, permission), parse error or version mismatch is returned
// unwrapped-for-logging: callers degrade to memory-only or start fresh, and
// never log the error (it never contains sensitive data, but the contract
// asks for silence).
func (fs *filestore) load() (*cacheFile, error) {
	var cf cacheFile
	if err := config.ReadJSON(fs.dataPath, &cf); err != nil {
		return nil, err
	}
	if cf.Version != cacheFileVersion {
		return nil, fmt.Errorf("mcp: cache file version %d unsupported", cf.Version)
	}
	return &cf, nil
}

// remember re-reads the persisted file under the profile lock so concurrent
// writers merge, adds keys at timestamp now, applies the retention policy
// (cacheTTL, cacheMaxKeys) and writes the result back. Any error along the
// way is swallowed: the caller's in-memory cache already has the keys.
func (fs *filestore) remember(keys []string, now time.Time) {
	lock, err := config.Acquire(fs.lockPath, cacheLockTimeout)
	if err != nil {
		return
	}
	defer func() { _ = lock.Release() }()

	cf, err := fs.load() // re-read so concurrent writers merge rather than clobber
	if err != nil {
		cf = &cacheFile{Version: cacheFileVersion}
	}
	if cf.Entries == nil {
		cf.Entries = make(map[string]int64, len(keys))
	}

	ts := now.Unix()
	for _, k := range keys {
		cf.Entries[k] = ts
	}
	cf.Version = cacheFileVersion
	cf.Entries = cacheTrim(cf.Entries, now)

	_ = config.WriteJSON(fs.dataPath, cf)
}

// cacheEntry pairs a key with its recorded timestamp, for sorting.
type cacheEntry struct {
	key string
	ts  int64
}

// cacheTrim drops entries older than cacheTTL relative to now, then keeps
// only the newest cacheMaxKeys by timestamp, ties broken by key, ascending.
func cacheTrim(entries map[string]int64, now time.Time) map[string]int64 {
	cutoff := now.Add(-cacheTTL).Unix()
	list := make([]cacheEntry, 0, len(entries))
	for k, ts := range entries {
		if ts < cutoff {
			continue
		}
		list = append(list, cacheEntry{key: k, ts: ts})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].ts != list[j].ts {
			return list[i].ts > list[j].ts
		}
		return list[i].key < list[j].key
	})
	if len(list) > cacheMaxKeys {
		list = list[:cacheMaxKeys]
	}
	out := make(map[string]int64, len(list))
	for _, e := range list {
		out[e.key] = e.ts
	}
	return out
}

// Cache is the MCP delivery-receipt cache: it remembers which items this
// profile has already been sent, so a repeat read or preview is suppressed
// unless the caller explicitly asks to replay it.
type Cache struct {
	mu      sync.RWMutex
	dir     string
	profile string
	memory  map[string]struct{}
	// persist is the file-backed store. It is nil when the profile name is
	// invalid or the cache directory could not be created; the cache then
	// stays memory-only for the life of the process.
	persist *filestore
}

// OpenCache never fails; a broken persistent cache degrades to memory-only.
// The persisted file lives at <dir>/cache/<profile>.json, locked by
// <dir>/cache/<profile>.lock. An invalid profile name never touches disk.
func OpenCache(dir, profile string) *Cache {
	c := &Cache{
		dir:     dir,
		profile: profile,
		memory:  make(map[string]struct{}),
	}
	if !cacheProfilePattern.MatchString(profile) {
		return c
	}

	cacheDir := filepath.Join(dir, "cache")
	if err := config.EnsurePrivateDir(cacheDir); err != nil {
		return c
	}

	fs := &filestore{
		dataPath: filepath.Join(cacheDir, profile+".json"),
		lockPath: filepath.Join(cacheDir, profile+".lock"),
	}
	// Still attempt writes later even if the load below fails: the lock
	// might be free next time, and Remember always re-reads first.
	c.persist = fs

	lock, err := config.Acquire(fs.lockPath, cacheLockTimeout)
	if err != nil {
		return c
	}
	cf, err := fs.load()
	_ = lock.Release()
	if err != nil {
		return c
	}

	cutoff := cacheNow().Add(-cacheTTL).Unix()
	for k, ts := range cf.Entries {
		if ts >= cutoff {
			c.memory[k] = struct{}{}
		}
	}
	return c
}

// Key returns the lowercase hex SHA-256 of the compact JSON encoding of
// [scope, profile, lane, value]. Determinism is required; byte-for-byte
// compatibility with the Python reference client's keys is not
// (docs/contract.md section 11, "Intentional break").
func (c *Cache) Key(scope []string, lane string, value any) string {
	if c == nil {
		return ""
	}
	payload := []any{scope, c.profile, lane, value}
	sum := sha256.Sum256([]byte(envelopeEncode(payload)))
	return hex.EncodeToString(sum[:])
}

// Seen reports whether key was previously remembered, checking the
// in-memory cache only. A nil *Cache is safe and reports false.
func (c *Cache) Seen(key string) bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	_, ok := c.memory[key]
	c.mu.RUnlock()
	return ok
}

// Remember records keys as delivered: memory is updated first, then, if a
// persistent store is configured, the keys are merged into the on-disk
// file under the profile lock. A nil *Cache or an empty call is safe and
// does nothing. Any persistence error leaves memory updated and returns
// silently.
func (c *Cache) Remember(keys ...string) {
	if c == nil || len(keys) == 0 {
		return
	}
	now := cacheNow()

	c.mu.Lock()
	for _, k := range keys {
		c.memory[k] = struct{}{}
	}
	persist := c.persist
	c.mu.Unlock()

	if persist != nil {
		persist.remember(keys, now)
	}
}

// Close releases the cache's resources. There is no open handle held
// between calls, so this always returns nil. A nil *Cache is safe.
func (c *Cache) Close() error {
	return nil
}
