// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ajent-social/ajent/internal/config"
)

func TestSnapshotRollbackRestoresBytesAndModes(t *testing.T) {
	configDir := t.TempDir()
	targetDir := t.TempDir()

	present644 := filepath.Join(targetDir, "present644.json")
	present600 := filepath.Join(targetDir, "present600.json")
	absent := filepath.Join(targetDir, "absent.json")

	if err := os.WriteFile(present644, []byte("original 644"), 0o644); err != nil {
		t.Fatalf("write present644: %v", err)
	}
	if err := os.WriteFile(present600, []byte("original 600"), 0o600); err != nil {
		t.Fatalf("write present600: %v", err)
	}

	backup, err := Snapshot(configDir, []string{present644, present600, absent})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// Mutate both existing files and create the file that was absent.
	if err := os.WriteFile(present644, []byte("mutated"), 0o644); err != nil {
		t.Fatalf("mutate present644: %v", err)
	}
	if err := os.WriteFile(present600, []byte("mutated"), 0o600); err != nil {
		t.Fatalf("mutate present600: %v", err)
	}
	if err := os.WriteFile(absent, []byte("newly created"), 0o644); err != nil {
		t.Fatalf("create absent: %v", err)
	}

	if err := Rollback(configDir, backup.ID); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	got644, err := os.ReadFile(present644)
	if err != nil {
		t.Fatalf("read present644: %v", err)
	}
	if string(got644) != "original 644" {
		t.Fatalf("present644 content = %q, want %q", got644, "original 644")
	}

	got600, err := os.ReadFile(present600)
	if err != nil {
		t.Fatalf("read present600: %v", err)
	}
	if string(got600) != "original 600" {
		t.Fatalf("present600 content = %q, want %q", got600, "original 600")
	}

	if runtime.GOOS != "windows" {
		info644, err := os.Stat(present644)
		if err != nil {
			t.Fatalf("stat present644: %v", err)
		}
		if info644.Mode().Perm() != 0o644 {
			t.Fatalf("present644 mode = %v, want 0644", info644.Mode().Perm())
		}

		info600, err := os.Stat(present600)
		if err != nil {
			t.Fatalf("stat present600: %v", err)
		}
		if info600.Mode().Perm() != 0o600 {
			t.Fatalf("present600 mode = %v, want 0600", info600.Mode().Perm())
		}
	}

	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatalf("absent file still exists after rollback, stat err = %v", err)
	}
}

func TestSnapshotRefusesSymlink(t *testing.T) {
	configDir := t.TempDir()
	targetDir := t.TempDir()

	realFile := filepath.Join(targetDir, "real.json")
	if err := os.WriteFile(realFile, []byte("data"), 0o644); err != nil {
		t.Fatalf("write real: %v", err)
	}
	link := filepath.Join(targetDir, "link.json")
	if err := os.Symlink(realFile, link); err != nil {
		t.Skipf("symlink not supported in this environment: %v", err)
	}

	if _, err := Snapshot(configDir, []string{realFile, link}); !errors.Is(err, config.ErrSymlink) {
		t.Fatalf("Snapshot error = %v, want config.ErrSymlink", err)
	}

	entries, err := os.ReadDir(filepath.Join(configDir, "backups"))
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("ReadDir backups: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("backups dir not empty after refused symlink: %v", entries)
	}
}

func TestRollbackLegacyPythonManifest(t *testing.T) {
	configDir := t.TempDir()
	targetDir := t.TempDir()

	target := filepath.Join(targetDir, "legacy.json")
	id := "1726000000000000000"
	backupDir := filepath.Join(configDir, "backups", id)
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatalf("mkdir backupDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "0"), []byte("legacy content"), 0o600); err != nil {
		t.Fatalf("write saved file: %v", err)
	}

	manifest := []map[string]any{
		{"target": target, "exists": true, "saved": "0", "mode": 420},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "manifest.json"), data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	// target does not currently exist; Rollback must (re)create it.
	if err := Rollback(configDir, id); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "legacy content" {
		t.Fatalf("target content = %q, want %q", got, "legacy content")
	}
}

func TestRollbackRejectsBadID(t *testing.T) {
	configDir := t.TempDir()

	for _, id := range []string{"../x", "", "abc"} {
		if err := Rollback(configDir, id); !errors.Is(err, errBackupUnknownID) {
			t.Fatalf("Rollback(%q) error = %v, want errBackupUnknownID", id, err)
		}
	}
}

func TestSnapshotPrunesTo20(t *testing.T) {
	configDir := t.TempDir()
	backupsDir := filepath.Join(configDir, "backups")
	if err := os.MkdirAll(backupsDir, 0o700); err != nil {
		t.Fatalf("mkdir backups: %v", err)
	}

	// 25 pre-existing Go-format backups, oldest first, dated well before
	// "today" so the backup Snapshot creates below always sorts newest.
	var preexisting []string
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("20260101T%06dZ-%06x", i, i)
		preexisting = append(preexisting, id)
		dir := filepath.Join(backupsDir, id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", id, err)
		}
		manifest := map[string]any{"version": 1, "created": "2026-01-01T00:00:00Z", "entries": []any{}}
		data, err := json.Marshal(manifest)
		if err != nil {
			t.Fatalf("marshal manifest %s: %v", id, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o600); err != nil {
			t.Fatalf("write manifest %s: %v", id, err)
		}
	}

	// A Python-era numeric backup directory, which must never be pruned.
	legacyID := "1726000000000000000"
	if err := os.MkdirAll(filepath.Join(backupsDir, legacyID), 0o700); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}

	if _, err := Snapshot(configDir, nil); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	entries, err := os.ReadDir(backupsDir)
	if err != nil {
		t.Fatalf("ReadDir backups: %v", err)
	}

	var newFormatCount int
	var haveLegacy bool
	for _, e := range entries {
		if e.Name() == legacyID {
			haveLegacy = true
			continue
		}
		if backupNewIDPattern.MatchString(e.Name()) {
			newFormatCount++
		}
	}

	if !haveLegacy {
		t.Fatal("legacy Python-era backup was pruned; it must never be")
	}
	if newFormatCount != backupKeepCount {
		t.Fatalf("new-format backup count = %d, want %d", newFormatCount, backupKeepCount)
	}

	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(backupsDir, preexisting[i])); !os.IsNotExist(err) {
			t.Fatalf("stale backup %s was not pruned, stat err = %v", preexisting[i], err)
		}
	}
	for i := 6; i < 25; i++ {
		if _, err := os.Stat(filepath.Join(backupsDir, preexisting[i])); err != nil {
			t.Fatalf("recent backup %s was pruned: %v", preexisting[i], err)
		}
	}
}
