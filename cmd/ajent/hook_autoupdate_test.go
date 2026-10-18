// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ajent-social/ajent/internal/cli"
	"github.com/ajent-social/ajent/internal/update"
)

// autoUpdateTestNoEnv stubs cli.Env.Getenv to return "" for every key, so
// autoUpdateDue and runAutoUpdate never read the host's real environment
// (in particular CI, which real CI runners set).
func autoUpdateTestNoEnv() func(string) string {
	return func(string) string { return "" }
}

func TestAutoUpdateDueDefaultOn(t *testing.T) {
	env, _, _, exePath := cmdUpdateTestEnv(t, "0.7.0")
	cmdUpdateTestScriptInstall(t, env, exePath)
	env.Getenv = autoUpdateTestNoEnv()

	if !autoUpdateDue(env, env.ConfigDir, exePath) {
		t.Error("autoUpdateDue = false, want true for a script install with no prior auto-update and no preference set")
	}
}

func TestAutoUpdateOffByEnv(t *testing.T) {
	env, _, _, exePath := cmdUpdateTestEnv(t, "0.7.0")
	cmdUpdateTestScriptInstall(t, env, exePath)
	env.Getenv = func(k string) string {
		if k == "AJENT_AUTO_UPDATE" {
			return "0"
		}
		return ""
	}

	if autoUpdateDue(env, env.ConfigDir, exePath) {
		t.Error("autoUpdateDue = true, want false when AJENT_AUTO_UPDATE=0")
	}
}

func TestAutoUpdateOffInCI(t *testing.T) {
	env, _, _, exePath := cmdUpdateTestEnv(t, "0.7.0")
	cmdUpdateTestScriptInstall(t, env, exePath)
	env.Getenv = func(k string) string {
		if k == "CI" {
			return "1"
		}
		return ""
	}

	if autoUpdateDue(env, env.ConfigDir, exePath) {
		t.Error("autoUpdateDue = true, want false when CI is set")
	}
}

func TestAutoUpdateOffByPreference(t *testing.T) {
	env, _, _, exePath := cmdUpdateTestEnv(t, "0.7.0")
	cmdUpdateTestScriptInstall(t, env, exePath)
	env.Getenv = autoUpdateTestNoEnv()

	off := false
	if err := update.SaveState(env.ConfigDir, update.State{Auto: &off}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	if autoUpdateDue(env, env.ConfigDir, exePath) {
		t.Error("autoUpdateDue = true, want false when State.Auto is off")
	}
}

func TestAutoUpdateOffForHomebrew(t *testing.T) {
	env, _, _, _ := cmdUpdateTestEnv(t, "0.7.0")
	env.Getenv = autoUpdateTestNoEnv()

	dir := filepath.Join(t.TempDir(), "Cellar", "ajent", "0.7.0", "bin")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	exePath := filepath.Join(dir, cmdUpdateTestExeName())
	if err := os.WriteFile(exePath, []byte("brew-managed-placeholder"), 0o755); err != nil { //nolint:gosec // G306: a real executable needs this mode; the path is under t.TempDir().
		t.Fatalf("seeding exe: %v", err)
	}

	if autoUpdateDue(env, env.ConfigDir, exePath) {
		t.Error("autoUpdateDue = true, want false for a Homebrew install")
	}
}

func TestAutoUpdateRateLimited(t *testing.T) {
	env, _, _, exePath := cmdUpdateTestEnv(t, "0.7.0")
	cmdUpdateTestScriptInstall(t, env, exePath)
	env.Getenv = autoUpdateTestNoEnv()

	var calls int
	original := startDetached
	startDetached = func(string, []string, string) error {
		calls++
		return nil
	}
	t.Cleanup(func() { startDetached = original })

	autoUpdateSessionStart(context.Background(), env)
	if calls != 1 {
		t.Fatalf("calls after first session-start = %d, want 1", calls)
	}

	// env.Now() is fixed (testenv_test.go), so a second call immediately
	// afterward is still within the 24-hour window and must not spawn again.
	autoUpdateSessionStart(context.Background(), env)
	if calls != 1 {
		t.Fatalf("calls after second session-start within 24h = %d, want 1 (rate limited)", calls)
	}
}

func TestAutoUpdateSkipsDevVersion(t *testing.T) {
	cases := []struct {
		name    string
		version string
	}{
		{"dev-build", "dev"},
		{"snapshot-build", "0.7.1-SNAPSHOT-abc1234"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, _, _, exePath := cmdUpdateTestEnv(t, tc.version)
			cmdUpdateTestScriptInstall(t, env, exePath)
			env.Getenv = autoUpdateTestNoEnv()

			if autoUpdateDue(env, env.ConfigDir, exePath) {
				t.Errorf("autoUpdateDue(version.Version=%q) = true, want false", tc.version)
			}
		})
	}
}

func TestHookSpawnsAutoRun(t *testing.T) {
	// sessionStartExtras is a shared, package-level slice that every hook
	// extra (T6.4, T6.6, ...) appends to once from init(). Other test files
	// mutate it too (for example cmd_hook_test.go's
	// TestHookExtrasRecoverPanics replaces it and resets it to nil on
	// cleanup), so this test must not depend on whatever init() left there:
	// it sets up exactly the extra it needs and restores the original
	// value afterward, independent of test execution order.
	originalExtras := sessionStartExtras
	sessionStartExtras = []func(ctx context.Context, env *cli.Env){autoUpdateSessionStart}
	t.Cleanup(func() { sessionStartExtras = originalExtras })

	env, stdout, stderr, exePath := cmdUpdateTestEnv(t, "0.7.0")
	cmdUpdateTestScriptInstall(t, env, exePath)
	env.Getenv = autoUpdateTestNoEnv()

	var gotExe string
	var gotArgs []string
	var gotLog string
	original := startDetached
	startDetached = func(executable string, args []string, logPath string) error {
		gotExe = executable
		gotArgs = append([]string{}, args...)
		gotLog = logPath
		return nil
	}
	t.Cleanup(func() { startDetached = original })

	code := run(t, env, "hook", "session-start")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}

	if want := sessionStartText + "\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want only the hook text %q", stdout.String(), want)
	}

	if gotExe != exePath {
		t.Errorf("startDetached executable = %q, want %q", gotExe, exePath)
	}
	wantArgs := []string{"update", "--auto-run"}
	if len(gotArgs) != len(wantArgs) || gotArgs[0] != wantArgs[0] || gotArgs[1] != wantArgs[1] {
		t.Errorf("startDetached args = %v, want %v", gotArgs, wantArgs)
	}
	if want := filepath.Join(env.ConfigDir, "update.log"); gotLog != want {
		t.Errorf("startDetached logPath = %q, want %q", gotLog, want)
	}
}
