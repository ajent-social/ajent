// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/ajent-social/ajent-client/internal/config"
)

// Backup identifies one snapshot written under <configDir>/backups/<ID>/
// (docs/adr/007-harness-integration-model.md).
type Backup struct {
	ID  string
	Dir string
}

// backupManifestEntry is one target file recorded in manifest.json, in
// either the Go format (nested under "entries") or the Python-era legacy
// format (a bare array of these). Saved and Mode are omitted for an entry
// whose target did not exist at snapshot time (docs/tasks/T4.6.md step 1).
type backupManifestEntry struct {
	Target string `json:"target"`
	Exists bool   `json:"exists"`
	Saved  string `json:"saved,omitempty"`
	Mode   int    `json:"mode,omitempty"`
}

// backupManifest is the JSON shape of a Go-format manifest.json.
type backupManifest struct {
	Version int                   `json:"version"`
	Created string                `json:"created"`
	Entries []backupManifestEntry `json:"entries"`
}

// backupIDPattern validates a Rollback id before it ever touches the
// filesystem: either the Go format (20060102T150405Z-<6 hex bytes>) or a
// Python-era all-digit nanosecond timestamp (docs/tasks/T4.6.md step 3).
var backupIDPattern = regexp.MustCompile(`^(\d{8}T\d{6}Z-[0-9a-f]{6}|\d{1,20})$`)

// backupNewIDPattern matches only the Go backup ID format. Snapshot uses it
// to decide which backups/ entries are eligible for pruning; a Python-era
// numeric ID never matches and is therefore never pruned.
var backupNewIDPattern = regexp.MustCompile(`^\d{8}T\d{6}Z-[0-9a-f]{6}$`)

// backupKeepCount is how many Go-format backups Snapshot keeps under
// backups/ after each run (docs/tasks/T4.6.md step 2).
const backupKeepCount = 20

// errBackupUnknownID is returned by Rollback when id does not match
// backupIDPattern.
var errBackupUnknownID = errors.New("integrations: unknown backup id")

// newBackupID returns a fresh Go-format backup ID: a UTC timestamp plus 3
// random hex bytes (docs/tasks/T4.6.md step 1).
func newBackupID() (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("integrations: generating backup id: %w", err)
	}
	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:]), nil
}

// Snapshot backs up paths (deduplicated and sorted) into a fresh
// <configDir>/backups/<ID>/ directory before any of them is modified. A
// symlinked path refuses with config.ErrSymlink and leaves no backup
// directory behind. A missing path records exists:false. After writing the
// manifest, Snapshot prunes backups/ to the newest backupKeepCount
// Go-format IDs; Python-era (and any other non-matching) IDs are never
// pruned (docs/tasks/T4.6.md steps 1 and 2).
func Snapshot(configDir string, paths []string) (Backup, error) {
	id, err := newBackupID()
	if err != nil {
		return Backup{}, err
	}
	dir := filepath.Join(configDir, "backups", id)

	if err := config.EnsurePrivateDir(dir); err != nil {
		return Backup{}, err
	}

	manifest := backupManifest{
		Version: 1,
		Created: time.Now().UTC().Format(time.RFC3339),
	}

	index := 0
	for _, p := range backupDedupSort(paths) {
		info, statErr := os.Lstat(p)
		switch {
		case statErr == nil && info.Mode()&os.ModeSymlink != 0:
			_ = os.RemoveAll(dir)
			return Backup{}, fmt.Errorf("integrations: %s: %w", p, config.ErrSymlink)
		case statErr == nil:
			data, readErr := os.ReadFile(p) //nolint:gosec // G304: p is a caller-supplied Params.Paths entry (harness config file), not attacker input; the symlink case above already refused.
			if readErr != nil {
				_ = os.RemoveAll(dir)
				return Backup{}, fmt.Errorf("integrations: reading %s: %w", p, readErr)
			}
			saved := strconv.Itoa(index)
			index++
			if err := config.WritePrivate(filepath.Join(dir, saved), data); err != nil {
				_ = os.RemoveAll(dir)
				return Backup{}, err
			}
			manifest.Entries = append(manifest.Entries, backupManifestEntry{
				Target: p,
				Exists: true,
				Saved:  saved,
				Mode:   int(info.Mode().Perm()),
			})
		case errors.Is(statErr, fs.ErrNotExist):
			manifest.Entries = append(manifest.Entries, backupManifestEntry{
				Target: p,
				Exists: false,
			})
		default:
			_ = os.RemoveAll(dir)
			return Backup{}, fmt.Errorf("integrations: stat %s: %w", p, statErr)
		}
	}

	if err := config.WriteJSON(filepath.Join(dir, "manifest.json"), manifest); err != nil {
		_ = os.RemoveAll(dir)
		return Backup{}, err
	}

	if err := backupPrune(configDir); err != nil {
		return Backup{}, err
	}

	return Backup{ID: id, Dir: dir}, nil
}

// Rollback restores the backup id under configDir/backups/: every saved
// file's bytes and mode, and removes every file that did not exist at
// snapshot time. id is validated first, so a malformed or path-traversing
// value never reaches the filesystem. It also reads manifests written by
// the Python client, whose manifest.json is a bare JSON array of the same
// entry shape (docs/contract.md section 4; docs/tasks/T4.6.md step 3).
func Rollback(configDir, id string) error {
	if !backupIDPattern.MatchString(id) {
		return errBackupUnknownID
	}

	dir := filepath.Join(configDir, "backups", id)
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("integrations: backup %s: %w", id, err)
	}
	if dirInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("integrations: backup %s: %w", id, config.ErrSymlink)
	}

	entries, err := backupReadManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return err
	}

	for _, e := range entries {
		targetInfo, statErr := os.Lstat(e.Target)
		switch {
		case statErr == nil && targetInfo.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("integrations: target %s: %w", e.Target, config.ErrSymlink)
		case statErr != nil && !errors.Is(statErr, fs.ErrNotExist):
			return fmt.Errorf("integrations: stat %s: %w", e.Target, statErr)
		}

		if !e.Exists {
			if statErr == nil {
				if err := os.Remove(e.Target); err != nil {
					return fmt.Errorf("integrations: removing %s: %w", e.Target, err)
				}
			}
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, e.Saved)) //nolint:gosec // G304: dir/e.Saved is this backup directory's own saved-copy index, read from the manifest this function already loaded from it.
		if err != nil {
			return fmt.Errorf("integrations: reading saved %s: %w", e.Saved, err)
		}
		if err := os.MkdirAll(filepath.Dir(e.Target), 0o700); err != nil {
			return fmt.Errorf("integrations: mkdir %s: %w", filepath.Dir(e.Target), err)
		}
		if err := config.WritePrivate(e.Target, data); err != nil {
			return err
		}
		if e.Mode != 0 {
			if err := os.Chmod(e.Target, fs.FileMode(e.Mode)); err != nil { //nolint:gosec // G302: e.Mode is the exact permission bits Snapshot recorded for this same target; restoring them is Rollback's contract.
				return fmt.Errorf("integrations: chmod %s: %w", e.Target, err)
			}
		}
	}

	return nil
}

// backupReadManifest reads manifest.json at path and returns its entries.
// A JSON object uses the Go format's "entries" array; a bare JSON array is
// the Python-era legacy format, whose items share the same entry shape.
func backupReadManifest(path string) ([]backupManifestEntry, error) {
	data, err := config.ReadPrivate(path)
	if err != nil {
		return nil, fmt.Errorf("integrations: reading manifest %s: %w", path, err)
	}

	trimmed := bytes.TrimLeft(data, " \t\r\n")
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var entries []backupManifestEntry
		if err := json.Unmarshal(data, &entries); err != nil {
			return nil, fmt.Errorf("integrations: parsing legacy manifest %s: %w", path, err)
		}
		return entries, nil
	}

	var manifest backupManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("integrations: parsing manifest %s: %w", path, err)
	}
	return manifest.Entries, nil
}

// backupDedupSort returns paths deduplicated and sorted (docs/tasks/T4.6.md
// step 2).
func backupDedupSort(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// backupPrune removes the oldest Go-format backup directories under
// <configDir>/backups/ so that at most backupKeepCount remain. Entries
// whose name does not match the Go ID format (for example a Python-era
// all-digit ID) are left untouched, however many there are.
func backupPrune(configDir string) error {
	root := filepath.Join(configDir, "backups")
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("integrations: reading %s: %w", root, err)
	}

	var ids []string
	for _, e := range entries {
		if e.IsDir() && backupNewIDPattern.MatchString(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	if len(ids) <= backupKeepCount {
		return nil
	}

	sort.Strings(ids)
	for _, id := range ids[:len(ids)-backupKeepCount] {
		if err := os.RemoveAll(filepath.Join(root, id)); err != nil {
			return fmt.Errorf("integrations: pruning backup %s: %w", id, err)
		}
	}
	return nil
}
