// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ajent-social/ajent/internal/integrations"
	"github.com/ajent-social/ajent/internal/testutil/fakeserver"
)

// uninstallFakeWriter is a minimal integrations.Writer used only by this
// file's tests. It detects only when detect is true. Apply writes a
// marker config file under the config dir; Remove deletes it and, when
// removed is non-nil, records that it ran.
type uninstallFakeWriter struct {
	name    string
	detect  bool
	removed *bool
}

func (w uninstallFakeWriter) Target() integrations.Target {
	return integrations.Target{Name: w.name}
}

func (w uninstallFakeWriter) Detect(integrations.Params) bool { return w.detect }

func (w uninstallFakeWriter) Paths(p integrations.Params) []string {
	return []string{filepath.Join(p.ConfigDir, w.name+"-config.json")}
}

func (w uninstallFakeWriter) Apply(_ context.Context, p integrations.Params) (integrations.Result, error) {
	path := w.Paths(p)[0]
	if err := os.WriteFile(path, []byte(`{"configured":true}`), 0o644); err != nil {
		return integrations.Result{}, err
	}
	return integrations.Result{Name: w.name, Status: "configured"}, nil
}

func (w uninstallFakeWriter) Remove(_ context.Context, p integrations.Params) (integrations.Result, error) {
	if w.removed != nil {
		*w.removed = true
	}
	path := w.Paths(p)[0]
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return integrations.Result{}, err
	}
	return integrations.Result{Name: w.name, Status: "removed"}, nil
}

// uninstallSetBinaryOverride pins integrations.BinaryOverride for the
// duration of a test and restores the previous value on cleanup, matching
// the pattern TestIntegrationsCommandStatusSyncRollback uses.
func uninstallSetBinaryOverride(t *testing.T) {
	t.Helper()
	prev := integrations.BinaryOverride
	integrations.BinaryOverride = "/usr/local/bin/ajent"
	t.Cleanup(func() { integrations.BinaryOverride = prev })
}

// uninstallBackupLine extracts the backup ID printed by "ajent uninstall"'s
// "Integration backup: <ID>. Roll back with: ajent rollback <ID>" line.
var uninstallBackupLine = regexp.MustCompile(`Integration backup: (\S+)\. Roll back with: ajent rollback (\S+)`)

func TestUninstallRemovesOwnedAndKeepsCredentials(t *testing.T) {
	uninstallSetBinaryOverride(t)
	defer integrations.ResetForTest()()

	var removed bool
	integrations.Register(uninstallFakeWriter{name: "claude", detect: true, removed: &removed})

	srv := fakeserver.New(t)
	env, stdout, stderr, _ := newTestEnv(t, srv)
	writeRoot(t, env, srv.Credential())

	configPath := filepath.Join(env.ConfigDir, "claude-config.json")
	if err := os.WriteFile(configPath, []byte(`{"configured":true}`), 0o644); err != nil {
		t.Fatalf("seed writer config: %v", err)
	}
	examplePath := filepath.Join(env.ConfigDir, "mcp-example.json")
	if err := os.WriteFile(examplePath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatalf("seed mcp-example.json: %v", err)
	}

	if code := run(t, env, "uninstall"); code != 0 {
		t.Fatalf("uninstall exit = %d, stderr = %s", code, stderr.String())
	}

	if !removed {
		t.Fatal("fake writer's Remove was not called")
	}

	out := stdout.String()
	if !strings.Contains(out, "claude: removed") {
		t.Fatalf("output missing claude result: %s", out)
	}
	if uninstallBackupLine.FindStringSubmatch(out) == nil {
		t.Fatalf("output missing backup line: %s", out)
	}

	if _, err := os.Stat(examplePath); !os.IsNotExist(err) {
		t.Fatalf("mcp-example.json still exists, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.ConfigDir, "credential.json")); err != nil {
		t.Fatalf("credential.json should still exist: %v", err)
	}
}

func TestUninstallPurgeNonInteractiveNeedsYes(t *testing.T) {
	uninstallSetBinaryOverride(t)
	defer integrations.ResetForTest()()

	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)
	writeRoot(t, env, srv.Credential())

	if code := run(t, env, "uninstall", "--purge"); code != 2 {
		t.Fatalf("uninstall --purge exit = %d, want 2, stderr = %s", code, stderr.String())
	}

	if _, err := os.Stat(filepath.Join(env.ConfigDir, "credential.json")); err != nil {
		t.Fatalf("credential.json should still exist: %v", err)
	}
}

func TestUninstallPurgeYes(t *testing.T) {
	uninstallSetBinaryOverride(t)
	defer integrations.ResetForTest()()

	srv := fakeserver.New(t)
	env, stdout, stderr, _ := newTestEnv(t, srv)
	writeRoot(t, env, srv.Credential())
	if err := os.MkdirAll(filepath.Join(env.ConfigDir, "profiles", "abc123"), 0o700); err != nil {
		t.Fatalf("seed profiles dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(env.ConfigDir, "config.json"), []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatalf("seed config.json: %v", err)
	}

	if code := run(t, env, "uninstall", "--purge", "--yes"); code != 0 {
		t.Fatalf("uninstall --purge --yes exit = %d, stderr = %s", code, stderr.String())
	}

	out := stdout.String()
	revokeIdx := strings.Index(out, "To revoke this installation on the server")
	deletedIdx := strings.Index(out, "Local credentials deleted.")
	if revokeIdx < 0 || deletedIdx < 0 {
		t.Fatalf("output missing revoke or deleted line: %s", out)
	}
	if revokeIdx > deletedIdx {
		t.Fatalf("revoke line did not come before deleted line: %s", out)
	}

	for _, name := range []string{"credential.json", "profiles", "config.json"} {
		if _, err := os.Stat(filepath.Join(env.ConfigDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s still exists, stat err = %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(env.ConfigDir, "backups")); err != nil {
		t.Errorf("backups directory missing: %v", err)
	}
}

func TestUninstallPurgeDeclined(t *testing.T) {
	uninstallSetBinaryOverride(t)
	defer integrations.ResetForTest()()

	srv := fakeserver.New(t)
	env, stdout, stderr, _ := newTestEnv(t, srv)
	writeRoot(t, env, srv.Credential())
	env.IsTerminal = func(uintptr) bool { return true }
	env.Stdin = strings.NewReader("n\n")

	if code := run(t, env, "uninstall", "--purge"); code != 0 {
		t.Fatalf("uninstall --purge exit = %d, stderr = %s", code, stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "Credentials kept.") {
		t.Fatalf("output missing \"Credentials kept.\": %s", out)
	}
	if strings.Contains(out, "Local credentials deleted.") {
		t.Fatalf("output should not contain the deleted line: %s", out)
	}
	if _, err := os.Stat(filepath.Join(env.ConfigDir, "credential.json")); err != nil {
		t.Fatalf("credential.json should still exist: %v", err)
	}
}

func TestUninstallRollback(t *testing.T) {
	uninstallSetBinaryOverride(t)
	defer integrations.ResetForTest()()

	srv := fakeserver.New(t)
	env, stdout, stderr, _ := newTestEnv(t, srv)
	writeRoot(t, env, srv.Credential())

	examplePath := filepath.Join(env.ConfigDir, "mcp-example.json")
	original := []byte(`{"mcpServers":{"ajent":{}}}`)
	if err := os.WriteFile(examplePath, original, 0o644); err != nil {
		t.Fatalf("seed mcp-example.json: %v", err)
	}

	if code := run(t, env, "uninstall"); code != 0 {
		t.Fatalf("uninstall exit = %d, stderr = %s", code, stderr.String())
	}
	out := stdout.String()
	m := uninstallBackupLine.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("output missing backup line: %s", out)
	}
	backupID := m[1]

	if _, err := os.Stat(examplePath); !os.IsNotExist(err) {
		t.Fatalf("mcp-example.json should be gone after uninstall, stat err = %v", err)
	}

	stdout.Reset()
	if code := run(t, env, "rollback", backupID); code != 0 {
		t.Fatalf("rollback exit = %d, stderr = %s", code, stderr.String())
	}

	restored, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("mcp-example.json not restored: %v", err)
	}
	if string(restored) != string(original) {
		t.Fatalf("restored mcp-example.json = %q, want %q", restored, original)
	}
}

func TestUninstallRefusesHomeAsConfigDir(t *testing.T) {
	uninstallSetBinaryOverride(t)
	defer integrations.ResetForTest()()

	srv := fakeserver.New(t)
	env, stdout, stderr, _ := newTestEnv(t, srv)
	env.ConfigDir = env.Home
	writeRoot(t, env, srv.Credential())

	if code := run(t, env, "uninstall", "--purge", "--yes"); code == 0 {
		t.Fatalf("uninstall --purge --yes with ConfigDir == Home unexpectedly succeeded: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "ajent:") {
		t.Fatalf("stderr missing error: %s", stderr.String())
	}

	if _, err := os.Stat(filepath.Join(env.Home, "credential.json")); err != nil {
		t.Fatalf("credential.json should still exist: %v", err)
	}
}

func TestUninstallPurgeGuard(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	ajentDir := filepath.Join(home, ".aj")
	emptyDir := filepath.Join(base, "unrelated")
	for _, d := range []string{ajentDir, emptyDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(ajentDir, "credential.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(emptyDir, "notes.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		dir     string
		wantErr bool
	}{
		{"ajent config dir", ajentDir, false},
		{"home itself", home, true},
		{"parent of home", base, true},
		{"filesystem root", filepath.VolumeName(base) + string(filepath.Separator), true},
		{"directory without ajent files", emptyDir, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := uninstallPurgeGuard(tt.dir, home)
			if (err != nil) != tt.wantErr {
				t.Fatalf("uninstallPurgeGuard(%q) error = %v, wantErr %v", tt.dir, err, tt.wantErr)
			}
		})
	}
}
