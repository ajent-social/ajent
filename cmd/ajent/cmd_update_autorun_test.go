// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ajent-social/ajent/internal/testutil/fakerelease"
	"github.com/ajent-social/ajent/internal/update"
)

// TestAutoRunAppliesStable drives "ajent update --auto-run" (runAutoUpdate,
// hook_autoupdate.go) against a fakerelease server that has both a newer
// stable release and a newer prerelease, and asserts it applies the stable
// one only: automatic runs never use prereleases (ADR 005).
func TestAutoRunAppliesStable(t *testing.T) {
	env, _, stderr, exePath := cmdUpdateTestEnv(t, "0.7.0")
	cmdUpdateTestScriptInstall(t, env, exePath)
	env.Getenv = autoUpdateTestNoEnv()

	priv := cmdUpdateTestKey(t)
	stableBinary := cmdUpdateTestBuildBinary(t, "0.7.1")
	stableAssets := fakerelease.BuildAssets(t, "0.7.1", runtime.GOOS, runtime.GOARCH, stableBinary, priv)
	// The prerelease also has a real, installable archive: if runAutoUpdate
	// ever regressed to consider prereleases (docs/tasks/T6.6.md's
	// genuine-red check flips src.Latest(ctx, false) to
	// src.Latest(ctx, true)), it must actually install and report
	// 0.8.0-rc.1, not merely fail for lack of an asset.
	preBinary := cmdUpdateTestBuildBinary(t, "0.8.0-rc.1")
	preAssets := fakerelease.BuildAssets(t, "0.8.0-rc.1", runtime.GOOS, runtime.GOARCH, preBinary, priv)

	srv := cmdUpdateTestFakeRelease(t)
	srv.AddRelease("0.7.1", false, stableAssets)
	srv.AddRelease("0.8.0-rc.1", true, preAssets)

	code := run(t, env, "update", "--auto-run")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}

	if want := "auto-run: updated 0.7.0 -> 0.7.1\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}

	// #nosec G204 -- exePath is the file this test's own auto-run just installed.
	out, err := exec.Command(exePath, "version", "--json").Output()
	if err != nil {
		t.Fatalf("running updated binary: %v", err)
	}
	var got struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decoding %s: %v", out, err)
	}
	if got.Version != "0.7.1" {
		t.Errorf("updated binary reports version %q, want 0.7.1 (stable, ignoring the 0.8.0-rc.1 prerelease)", got.Version)
	}

	st, err := update.LoadState(env.ConfigDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st.Latest != "0.7.1" {
		t.Errorf("state.Latest = %q, want 0.7.1", st.Latest)
	}
}

func TestAutoRunUpToDate(t *testing.T) {
	env, _, stderr, exePath := cmdUpdateTestEnv(t, "0.8.0")
	cmdUpdateTestScriptInstall(t, env, exePath)
	env.Getenv = autoUpdateTestNoEnv()

	srv := cmdUpdateTestFakeRelease(t)
	srv.AddRelease("0.8.0", false, map[string][]byte{})

	before, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("reading exe: %v", err)
	}

	code := run(t, env, "update", "--auto-run")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}

	if want := "auto-run: up to date 0.8.0\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}

	after, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("reading exe: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("exe changed on an up-to-date auto-run")
	}
}

func TestAutoRunDisabledByPreference(t *testing.T) {
	env, _, stderr, exePath := cmdUpdateTestEnv(t, "0.7.0")
	cmdUpdateTestScriptInstall(t, env, exePath)
	env.Getenv = autoUpdateTestNoEnv()

	off := false
	if err := update.SaveState(env.ConfigDir, update.State{Auto: &off}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	srv := cmdUpdateTestFakeRelease(t)
	srv.AddRelease("0.8.0", false, map[string][]byte{})

	before, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("reading exe: %v", err)
	}

	code := run(t, env, "update", "--auto-run")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if want := "auto-run: disabled\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	if got := srv.Requests(); len(got) != 0 {
		t.Errorf("srv.Requests() = %v, want none: a disabled auto-run must not query the release source", got)
	}

	after, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("reading exe: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("exe changed on a disabled auto-run")
	}
}

func TestAutoRunIgnoresManaged(t *testing.T) {
	env, _, stderr, _ := cmdUpdateTestEnv(t, "0.7.0")
	env.Getenv = autoUpdateTestNoEnv()

	dir := filepath.Join(t.TempDir(), "Cellar", "ajent", "0.7.0", "bin")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	exePath := filepath.Join(dir, cmdUpdateTestExeName())
	before := []byte("brew-managed-placeholder")
	if err := os.WriteFile(exePath, before, 0o755); err != nil { //nolint:gosec // G306: a real executable needs this mode; the path is under t.TempDir().
		t.Fatalf("seeding exe: %v", err)
	}
	cmdUpdateTestSetExecutablePath(t, exePath)

	srv := cmdUpdateTestFakeRelease(t)
	srv.AddRelease("0.8.0", false, map[string][]byte{})

	code := run(t, env, "update", "--auto-run")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if want := "auto-run: disabled\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	if got := srv.Requests(); len(got) != 0 {
		t.Errorf("srv.Requests() = %v, want none", got)
	}

	after, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("reading exe: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("a managed install's exe changed under auto-run")
	}
}
