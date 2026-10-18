// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// syncFakeWriter is a minimal, configurable Writer used only by the Sync
// tests in this file. Tests clear and restore the real registry with
// defer ResetForTest()() before registering one.
type syncFakeWriter struct {
	name       string
	detect     bool
	paths      []string
	applyFn    func(p Params) (Result, error)
	applyCalls *int
}

func (w syncFakeWriter) Target() Target        { return Target{Name: w.name} }
func (w syncFakeWriter) Detect(Params) bool    { return w.detect }
func (w syncFakeWriter) Paths(Params) []string { return w.paths }

func (w syncFakeWriter) Apply(_ context.Context, p Params) (Result, error) {
	if w.applyCalls != nil {
		*w.applyCalls++
	}
	if w.applyFn != nil {
		return w.applyFn(p)
	}
	return Result{Name: w.name, Status: "configured"}, nil
}

func (w syncFakeWriter) Remove(context.Context, Params) (Result, error) {
	return Result{Name: w.name, Status: "removed"}, nil
}

// syncTestParams builds a minimal Params for the Sync tests in this file.
func syncTestParams(t *testing.T, configDir string) Params {
	t.Helper()
	return Params{
		Home:      t.TempDir(),
		Binary:    "/usr/local/bin/ajent",
		ConfigDir: configDir,
		GOOS:      "linux",
		Getenv:    func(string) string { return "" },
	}
}

// syncManifestHasTarget reports whether backup's manifest.json records
// target.
func syncManifestHasTarget(t *testing.T, backupDir, target string) bool {
	t.Helper()
	entries, err := backupReadManifest(filepath.Join(backupDir, "manifest.json"))
	if err != nil {
		t.Fatalf("backupReadManifest: %v", err)
	}
	for _, e := range entries {
		if e.Target == target {
			return true
		}
	}
	return false
}

func TestSyncSnapshotsBeforeApply(t *testing.T) {
	defer ResetForTest()()

	configDir := t.TempDir()
	targetDir := t.TempDir()
	target := filepath.Join(targetDir, "sync-target.json")
	if err := os.WriteFile(target, []byte("before"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	var sawBackup bool
	Register(syncFakeWriter{
		name:   "claude",
		detect: true,
		paths:  []string{target},
		applyFn: func(p Params) (Result, error) {
			// The backup dir already contains this writer's path by the
			// time Apply runs: Snapshot must have run first.
			backupsDir := filepath.Join(p.ConfigDir, "backups")
			dirs, err := os.ReadDir(backupsDir)
			if err != nil || len(dirs) != 1 {
				t.Fatalf("backups dir = %v, err = %v, want exactly one backup", dirs, err)
			}
			sawBackup = syncManifestHasTarget(t, filepath.Join(backupsDir, dirs[0].Name()), target)
			return Result{Name: "claude", Status: "configured"}, nil
		},
	})

	p := syncTestParams(t, configDir)
	if _, _, err := Sync(context.Background(), p); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if !sawBackup {
		t.Fatal("Apply ran before Snapshot recorded its path; Sync must snapshot before applying")
	}
}

func TestSyncWritesStatusAndExample(t *testing.T) {
	defer ResetForTest()()

	configDir := t.TempDir()
	Register(syncFakeWriter{name: "claude", detect: true})

	p := syncTestParams(t, configDir)
	results, backup, err := Sync(context.Background(), p)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(results) != 1 || results[0].Name != "claude" || results[0].Status != "configured" {
		t.Fatalf("results = %+v", results)
	}

	exampleData, err := os.ReadFile(filepath.Join(configDir, "mcp-example.json"))
	if err != nil {
		t.Fatalf("read mcp-example.json: %v", err)
	}
	var example struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(exampleData, &example); err != nil {
		t.Fatalf("unmarshal mcp-example.json: %v", err)
	}
	ajentEntry, ok := example.MCPServers["ajent"]
	if !ok {
		t.Fatal("mcp-example.json missing mcpServers.ajent")
	}
	var entry map[string]any
	if err := json.Unmarshal(ajentEntry, &entry); err != nil {
		t.Fatalf("unmarshal ajent entry: %v", err)
	}
	args, _ := entry["args"].([]any)
	if len(args) < 3 || args[2] != "custom-agent" {
		t.Fatalf("mcp-example.json args = %v, want profile custom-agent in position 2", args)
	}

	statusData, err := os.ReadFile(filepath.Join(configDir, "integration-status.json"))
	if err != nil {
		t.Fatalf("read integration-status.json: %v", err)
	}
	var status struct {
		Updated string   `json:"updated"`
		Backup  string   `json:"backup"`
		Results []Result `json:"results"`
	}
	if err := json.Unmarshal(statusData, &status); err != nil {
		t.Fatalf("unmarshal integration-status.json: %v", err)
	}
	if status.Backup != backup.ID {
		t.Fatalf("status.Backup = %q, want %q", status.Backup, backup.ID)
	}
	if _, err := time.Parse(time.RFC3339, status.Updated); err != nil {
		t.Fatalf("status.Updated = %q not RFC3339: %v", status.Updated, err)
	}
	if len(status.Results) != 1 || status.Results[0].Name != "claude" {
		t.Fatalf("status.Results = %+v", status.Results)
	}
}

func TestSyncWriterErrorBecomesIncomplete(t *testing.T) {
	defer ResetForTest()()

	configDir := t.TempDir()
	Register(syncFakeWriter{
		name:   "claude",
		detect: true,
		applyFn: func(Params) (Result, error) {
			return Result{}, errors.New("boom")
		},
	})

	p := syncTestParams(t, configDir)
	results, _, err := Sync(context.Background(), p)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v, want 1 entry", results)
	}
	if results[0].Name != "claude" || results[0].Status != "incomplete" || results[0].Detail != "boom" {
		t.Fatalf("results[0] = %+v", results[0])
	}
}

func TestSyncSkipsUndetected(t *testing.T) {
	defer ResetForTest()()

	configDir := t.TempDir()
	targetDir := t.TempDir()
	target := filepath.Join(targetDir, "undetected.json")
	if err := os.WriteFile(target, []byte("data"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	var applyCalls int
	Register(syncFakeWriter{
		name:       "cursor",
		detect:     false,
		paths:      []string{target},
		applyCalls: &applyCalls,
	})

	p := syncTestParams(t, configDir)
	results, backup, err := Sync(context.Background(), p)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %+v, want none", results)
	}
	if applyCalls != 0 {
		t.Fatalf("Apply called %d times, want 0", applyCalls)
	}
	if syncManifestHasTarget(t, backup.Dir, target) {
		t.Fatalf("undetected writer's path %s present in backup manifest", target)
	}
}
