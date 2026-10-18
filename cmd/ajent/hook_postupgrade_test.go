// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ajent-social/ajent/internal/cli"
	"github.com/ajent-social/ajent/internal/config"
	"github.com/ajent-social/ajent/internal/integrations"
	"github.com/ajent-social/ajent/internal/testutil/fakeserver"
	"github.com/ajent-social/ajent/internal/update"
	"github.com/ajent-social/ajent/internal/version"
)

// hookPostUpgradeSetVersion sets version.Version to v for the duration of
// the test, restoring the original value on cleanup. needsPostUpgrade and
// runPostUpgrade both read version.Version as the running version.
func hookPostUpgradeSetVersion(t *testing.T, v string) {
	t.Helper()
	original := version.Version
	version.Version = v
	t.Cleanup(func() { version.Version = original })
}

// hookPostUpgradeWriteStatus marks configDir as an installation that has
// already set up coding-tool integrations, which is one of
// needsPostUpgrade's preconditions: the presence of
// integration-status.json, not its content.
func hookPostUpgradeWriteStatus(t *testing.T, configDir string) {
	t.Helper()
	path := filepath.Join(configDir, "integration-status.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("seeding %s: %v", path, err)
	}
}

// hookPostUpgradeFakeWriter is a minimal integrations.Writer used only by
// this file's tests. It always detects, and its Apply rewrites
// "<Home>/.cursor/mcp.json" through integrations.MergeJSONServer and
// integrations.Entry (docs/tasks/T6.4.md step 4), mirroring the real
// cursor writer closely enough to exercise the merge. When failErr is set,
// Apply returns it instead, so tests can force an "incomplete" result.
type hookPostUpgradeFakeWriter struct {
	failErr error
}

func (hookPostUpgradeFakeWriter) Target() integrations.Target {
	return integrations.Target{Name: "cursor", Profile: "cursor"}
}

func (hookPostUpgradeFakeWriter) Detect(integrations.Params) bool { return true }

func (hookPostUpgradeFakeWriter) configPath(p integrations.Params) string {
	return filepath.Join(p.Home, ".cursor", "mcp.json")
}

func (w hookPostUpgradeFakeWriter) Paths(p integrations.Params) []string {
	return []string{w.configPath(p)}
}

func (w hookPostUpgradeFakeWriter) Apply(_ context.Context, p integrations.Params) (integrations.Result, error) {
	if w.failErr != nil {
		return integrations.Result{}, w.failErr
	}
	if _, err := integrations.MergeJSONServer(w.configPath(p), "mcpServers", integrations.Entry(p, "cursor")); err != nil {
		return integrations.Result{}, err
	}
	return integrations.Result{Name: "cursor", Status: "configured"}, nil
}

func (hookPostUpgradeFakeWriter) Remove(context.Context, integrations.Params) (integrations.Result, error) {
	return integrations.Result{Name: "cursor", Status: "removed"}, nil
}

// hookPostUpgradeCursorCommand reads back the "command" field of the
// "ajent" entry in "<Home>/.cursor/mcp.json".
func hookPostUpgradeCursorCommand(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".cursor", "mcp.json"))
	if err != nil {
		t.Fatalf("reading cursor mcp.json: %v", err)
	}
	var doc struct {
		MCPServers map[string]struct {
			Command string `json:"command"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decoding cursor mcp.json: %v", err)
	}
	return doc.MCPServers["ajent"].Command
}

func TestNeedsPostUpgradeFalseForDev(t *testing.T) {
	tests := []struct {
		name    string
		version string
	}{
		{"dev", "dev"},
		{"snapshot", "0.9.0-SNAPSHOT-abc1234"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hookPostUpgradeSetVersion(t, tt.version)
			dir := t.TempDir()
			hookPostUpgradeWriteStatus(t, dir)

			if needsPostUpgrade(dir) {
				t.Errorf("needsPostUpgrade(%q) = true, want false", tt.version)
			}
		})
	}
}

func TestNeedsPostUpgradeFalseWithoutStatus(t *testing.T) {
	hookPostUpgradeSetVersion(t, "0.9.3")
	dir := t.TempDir()

	if needsPostUpgrade(dir) {
		t.Error("needsPostUpgrade = true without integration-status.json, want false")
	}
}

func TestNeedsPostUpgradeTrueOnNewVersion(t *testing.T) {
	hookPostUpgradeSetVersion(t, "0.9.4")
	dir := t.TempDir()
	hookPostUpgradeWriteStatus(t, dir)

	if !needsPostUpgrade(dir) {
		t.Error("needsPostUpgrade = false for a new version with no recorded state, want true")
	}

	if err := update.SaveState(dir, update.State{SeenVersion: "0.9.4"}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	if needsPostUpgrade(dir) {
		t.Error("needsPostUpgrade = true once SeenVersion matches the running version, want false")
	}
}

func TestRunPostUpgradeRewritesStalePath(t *testing.T) {
	defer integrations.ResetForTest()()
	integrations.Register(hookPostUpgradeFakeWriter{})

	prevBinary := integrations.BinaryOverride
	stableBinary := filepath.Join(t.TempDir(), "ajent")
	integrations.BinaryOverride = stableBinary
	t.Cleanup(func() { integrations.BinaryOverride = prevBinary })

	hookPostUpgradeSetVersion(t, "0.8.0")

	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)
	hookPostUpgradeWriteStatus(t, env.ConfigDir)

	cursorPath := filepath.Join(env.Home, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(cursorPath), 0o700); err != nil {
		t.Fatalf("mkdir cursor dir: %v", err)
	}
	stale := `{"mcpServers":{"ajent":{"command":"/opt/homebrew/Cellar/ajent/0.7.0/bin/ajent","args":["mcp","--profile","cursor","--project-profile"]}}}`
	if err := os.WriteFile(cursorPath, []byte(stale), 0o600); err != nil {
		t.Fatalf("seeding stale cursor mcp.json: %v", err)
	}

	if code := run(t, env, "post-upgrade"); code != 0 {
		t.Fatalf("post-upgrade exit = %d, stderr = %s", code, stderr.String())
	}

	if got := hookPostUpgradeCursorCommand(t, env.Home); got != stableBinary {
		t.Errorf("cursor command = %q, want %q", got, stableBinary)
	}

	st, err := update.LoadState(env.ConfigDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st.SeenVersion != "0.8.0" {
		t.Errorf("state.json seen_version = %q, want %q", st.SeenVersion, "0.8.0")
	}
}

func TestRunPostUpgradeOnce(t *testing.T) {
	defer integrations.ResetForTest()()
	integrations.Register(hookPostUpgradeFakeWriter{})

	prevBinary := integrations.BinaryOverride
	integrations.BinaryOverride = filepath.Join(t.TempDir(), "ajent")
	t.Cleanup(func() { integrations.BinaryOverride = prevBinary })

	hookPostUpgradeSetVersion(t, "0.9.5")

	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)
	hookPostUpgradeWriteStatus(t, env.ConfigDir)

	if code := run(t, env, "post-upgrade"); code != 0 {
		t.Fatalf("first post-upgrade exit = %d, stderr = %s", code, stderr.String())
	}

	statusPath := filepath.Join(env.ConfigDir, "integration-status.json")
	info1, err := os.Stat(statusPath)
	if err != nil {
		t.Fatalf("stat integration-status.json after first run: %v", err)
	}
	data1, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("read integration-status.json after first run: %v", err)
	}

	if code := run(t, env, "post-upgrade"); code != 0 {
		t.Fatalf("second post-upgrade exit = %d, stderr = %s", code, stderr.String())
	}

	info2, err := os.Stat(statusPath)
	if err != nil {
		t.Fatalf("stat integration-status.json after second run: %v", err)
	}
	data2, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("read integration-status.json after second run: %v", err)
	}

	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Errorf("integration-status.json mtime changed on a second run: %v -> %v", info1.ModTime(), info2.ModTime())
	}
	if !bytes.Equal(data1, data2) {
		t.Errorf("integration-status.json changed on a second run:\n%s\n->\n%s", data1, data2)
	}
}

func TestRunPostUpgradeConcurrent(t *testing.T) {
	defer integrations.ResetForTest()()
	integrations.Register(hookPostUpgradeFakeWriter{})

	hookPostUpgradeSetVersion(t, "0.9.6")

	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)
	hookPostUpgradeWriteStatus(t, env.ConfigDir)

	dir, err := env.Dir()
	if err != nil {
		t.Fatalf("env.Dir: %v", err)
	}

	// Simulate a concurrently running "ajent post-upgrade" by holding its
	// lock ourselves before calling runPostUpgrade directly.
	lock, err := config.Acquire(filepath.Join(dir, "upgrade.lock"), time.Second)
	if err != nil {
		t.Fatalf("acquiring the upgrade lock: %v", err)
	}
	defer func() { _ = lock.Release() }()

	if err := runPostUpgrade(context.Background(), env, nil); err != nil {
		t.Fatalf("runPostUpgrade returned %v, want nil", err)
	}
	if !strings.Contains(stderr.String(), "post-upgrade: another sync is running") {
		t.Errorf("stderr = %q, want the another-sync message", stderr.String())
	}
}

func TestRunPostUpgradeIncompleteDoesNotMarkSeen(t *testing.T) {
	defer integrations.ResetForTest()()
	integrations.Register(hookPostUpgradeFakeWriter{failErr: errors.New("hookPostUpgradeFakeWriter: simulated writer failure")})

	prevBinary := integrations.BinaryOverride
	integrations.BinaryOverride = filepath.Join(t.TempDir(), "ajent")
	t.Cleanup(func() { integrations.BinaryOverride = prevBinary })

	hookPostUpgradeSetVersion(t, "0.9.7")

	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)
	hookPostUpgradeWriteStatus(t, env.ConfigDir)

	if code := run(t, env, "post-upgrade"); code == 0 {
		t.Fatalf("post-upgrade exit = 0, want non-zero (incomplete writer)")
	}
	if !strings.Contains(stderr.String(), "post-upgrade: incomplete; will retry at next session start") {
		t.Errorf("stderr = %q, want the incomplete message", stderr.String())
	}

	st, err := update.LoadState(env.ConfigDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st.SeenVersion != "" {
		t.Errorf("state.json seen_version = %q, want empty (not marked seen)", st.SeenVersion)
	}
}

func TestHookExtraNoStdout(t *testing.T) {
	prevExtras := sessionStartExtras
	sessionStartExtras = []func(context.Context, *cli.Env){hookPostUpgradeSessionStartExtra}
	t.Cleanup(func() { sessionStartExtras = prevExtras })

	prevStart := startDetached
	var calledExe string
	var calledArgs []string
	var calledLog string
	startDetached = func(executable string, args []string, logPath string) error {
		calledExe = executable
		calledArgs = args
		calledLog = logPath
		return nil
	}
	t.Cleanup(func() { startDetached = prevStart })

	hookPostUpgradeSetVersion(t, "0.9.8")

	srv := fakeserver.New(t)
	env, stdout, stderr, _ := newTestEnv(t, srv)
	hookPostUpgradeWriteStatus(t, env.ConfigDir)

	if code := run(t, env, "hook", "session-start"); code != 0 {
		t.Fatalf("hook session-start exit = %d, stderr = %s", code, stderr.String())
	}
	if stdout.String() != sessionStartText+"\n" {
		t.Errorf("stdout = %q, want only the hook text", stdout.String())
	}
	if calledExe == "" {
		t.Fatal("startDetached was not called")
	}
	if len(calledArgs) != 1 || calledArgs[0] != "post-upgrade" {
		t.Errorf("startDetached args = %v, want [post-upgrade]", calledArgs)
	}
	if !strings.HasSuffix(calledLog, "update.log") {
		t.Errorf("startDetached log path = %q, want it to end in update.log", calledLog)
	}
}
