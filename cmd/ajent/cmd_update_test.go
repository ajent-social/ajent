// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/ajent-social/ajent-client/internal/cli"
	"github.com/ajent-social/ajent-client/internal/testutil/fakerelease"
	"github.com/ajent-social/ajent-client/internal/testutil/fakeserver"
	"github.com/ajent-social/ajent-client/internal/update"
	"github.com/ajent-social/ajent-client/internal/version"
)

// cmdUpdateTestExeName returns the file name a target executable would
// have on the current platform.
func cmdUpdateTestExeName() string {
	if runtime.GOOS == "windows" {
		return "ajent.exe"
	}
	return "ajent"
}

// cmdUpdateTestSetVersion sets version.Version to v for the duration of
// the test, restoring the original value on cleanup. runUpdate reads
// version.Version as "current".
func cmdUpdateTestSetVersion(t *testing.T, v string) {
	t.Helper()
	original := version.Version
	version.Version = v
	t.Cleanup(func() { version.Version = original })
}

// cmdUpdateTestEnv builds a command-test *cli.Env (newTestEnv,
// cmd/ajent/testenv_test.go), sets version.Version to currentVersion, and
// overrides the executablePath seam to point at a fresh placeholder
// executable file under its own temp directory. It returns the env and
// buffers alongside that executable's path, which the caller may further
// customize (for example writing install.json, or relocating the exe
// under a package-manager-shaped directory).
func cmdUpdateTestEnv(t *testing.T, currentVersion string) (env *cli.Env, stdout, stderr *bytes.Buffer, exePath string) {
	t.Helper()

	srv := fakeserver.New(t)
	env, stdout, stderr, _ = newTestEnv(t, srv)

	cmdUpdateTestSetVersion(t, currentVersion)

	exePath = filepath.Join(t.TempDir(), cmdUpdateTestExeName())
	if err := os.WriteFile(exePath, []byte("placeholder-exe-"+currentVersion), 0o755); err != nil { //nolint:gosec // G306: a real executable needs this mode; the path is under t.TempDir().
		t.Fatalf("seeding placeholder exe: %v", err)
	}

	cmdUpdateTestSetExecutablePath(t, exePath)

	return env, stdout, stderr, exePath
}

// cmdUpdateTestSetExecutablePath overrides the executablePath seam to
// return path, restoring the original function on cleanup.
func cmdUpdateTestSetExecutablePath(t *testing.T, path string) {
	t.Helper()
	original := executablePath
	executablePath = func() (string, error) { return path, nil }
	t.Cleanup(func() { executablePath = original })
}

// cmdUpdateTestScriptInstall writes env.ConfigDir/install.json so
// update.Detect classifies exePath as a script install.
func cmdUpdateTestScriptInstall(t *testing.T, env *cli.Env, exePath string) {
	t.Helper()

	rec := struct {
		Method string `json:"method"`
		Path   string `json:"path"`
	}{Method: "script", Path: exePath}

	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshaling install.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(env.ConfigDir, "install.json"), data, 0o600); err != nil {
		t.Fatalf("writing install.json: %v", err)
	}
}

// cmdUpdateTestFakeRelease starts a fakerelease server and overrides the
// newSource seam so runUpdate queries it instead of the real GitHub API,
// restoring the original function on cleanup. The caller registers
// releases on the returned server before running the command.
func cmdUpdateTestFakeRelease(t *testing.T) *fakerelease.Server {
	t.Helper()

	srv := fakerelease.New(t)

	original := newSource
	newSource = func(configDir string) *update.Source {
		return &update.Source{
			HTTP:        http.DefaultClient,
			GitHubAPI:   srv.API(),
			FallbackURL: srv.FallbackURL(),
			CacheDir:    configDir,
		}
	}
	t.Cleanup(func() { newSource = original })

	return srv
}

// cmdUpdateTestKey generates a fresh Ed25519 key pair, appends its public
// key to update.PublicKeys for the duration of the test (restored by
// t.Cleanup, mirroring cmd_verify_release_test.go's verifyReleaseTestKey),
// and returns the private key for signing fakerelease assets.
func cmdUpdateTestKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}

	original := update.PublicKeys
	update.PublicKeys = append(append([]ed25519.PublicKey{}, original...), pub)
	t.Cleanup(func() { update.PublicKeys = original })

	return priv
}

var (
	cmdUpdateTestBinMu    sync.Mutex
	cmdUpdateTestBinCache = map[string][]byte{}
)

// cmdUpdateTestBuildBinary builds the real ajent binary at version v (see
// internal/update/apply_test.go's applyTestBuildBinary, which this
// mirrors for package main's own tests), caching by version. It skips the
// test in -short mode.
func cmdUpdateTestBuildBinary(t *testing.T, v string) []byte {
	t.Helper()
	if testing.Short() {
		t.Skip("cmdUpdateTestBuildBinary: skipping go build in -short mode")
	}

	cmdUpdateTestBinMu.Lock()
	defer cmdUpdateTestBinMu.Unlock()

	if cached, ok := cmdUpdateTestBinCache[v]; ok {
		return cached
	}

	out := filepath.Join(t.TempDir(), "ajent-helper-"+v)
	if runtime.GOOS == "windows" {
		out += ".exe"
	}

	// #nosec G204 -- "go" and every argument are fixed or test-derived (v is a literal version string this test file passes), never attacker input.
	cmd := exec.Command("go", "build",
		"-ldflags", "-X github.com/ajent-social/ajent-client/internal/version.Version="+v,
		"-o", out,
		"github.com/ajent-social/ajent-client/cmd/ajent",
	)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building helper binary for version %s: %v\n%s", v, err, combined)
	}

	data, err := os.ReadFile(out) //nolint:gosec // G304: out is a path this same helper just built under t.TempDir().
	if err != nil {
		t.Fatalf("reading helper binary: %v", err)
	}

	cmdUpdateTestBinCache[v] = data
	return data
}

func TestAutoRunFlagWithoutHook(t *testing.T) {
	// T6.6 assigns autoRun from init(); clear it to exercise the unwired path.
	prevAutoRun := autoRun
	autoRun = nil
	t.Cleanup(func() { autoRun = prevAutoRun })

	env, _, stderr, _ := cmdUpdateTestEnv(t, "0.7.0")

	code := run(t, env, "update", "--auto-run")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "not available in this build") {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), "not available in this build")
	}
}

func TestUpdateCheckJSON(t *testing.T) {
	env, stdout, stderr, exePath := cmdUpdateTestEnv(t, "0.7.0")
	cmdUpdateTestScriptInstall(t, env, exePath)

	srv := cmdUpdateTestFakeRelease(t)
	srv.AddRelease("0.8.0", false, map[string][]byte{})

	before, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("reading exe: %v", err)
	}

	code := run(t, env, "update", "--check")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}

	var got struct {
		Current         string `json:"current"`
		Latest          string `json:"latest"`
		UpdateAvailable bool   `json:"update_available"`
		Method          string `json:"method"`
		Command         string `json:"command"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decoding stdout %q: %v", stdout.String(), err)
	}
	if got.Current != "0.7.0" || got.Latest != "0.8.0" || !got.UpdateAvailable || got.Method != "script" || got.Command != "" {
		t.Errorf("update --check = %+v, want current=0.7.0 latest=0.8.0 update_available=true method=script command=\"\"", got)
	}

	after, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("reading exe: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("--check modified the exe")
	}

	st, err := update.LoadState(env.ConfigDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st.Latest != "0.8.0" {
		t.Errorf("state.Latest = %q, want 0.8.0", st.Latest)
	}
}

func TestUpdateUpToDate(t *testing.T) {
	env, stdout, stderr, exePath := cmdUpdateTestEnv(t, "0.8.0")
	cmdUpdateTestScriptInstall(t, env, exePath)

	srv := cmdUpdateTestFakeRelease(t)
	srv.AddRelease("0.8.0", false, map[string][]byte{})

	code := run(t, env, "update")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if want := "ajent 0.8.0 is up to date\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestUpdateScriptApplies(t *testing.T) {
	env, stdout, stderr, exePath := cmdUpdateTestEnv(t, "0.7.0")
	cmdUpdateTestScriptInstall(t, env, exePath)

	priv := cmdUpdateTestKey(t)
	binary := cmdUpdateTestBuildBinary(t, "0.7.1")
	assets := fakerelease.BuildAssets(t, "0.7.1", runtime.GOOS, runtime.GOARCH, binary, priv)

	srv := cmdUpdateTestFakeRelease(t)
	srv.AddRelease("0.7.1", false, assets)

	code := run(t, env, "update")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if want := "updated ajent 0.7.0 -> 0.7.1\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}

	// #nosec G204 -- exePath is the file this test's own update run just installed.
	out, err := exec.Command(exePath, "version", "--json").Output()
	if err != nil {
		t.Fatalf("running updated binary: %v", err)
	}
	var updatedInfo struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(out, &updatedInfo); err != nil {
		t.Fatalf("decoding %s: %v", out, err)
	}
	if updatedInfo.Version != "0.7.1" {
		t.Errorf("updated binary reports version %q, want 0.7.1", updatedInfo.Version)
	}

	st, err := update.LoadState(env.ConfigDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st.Latest != "0.7.1" {
		t.Errorf("state.Latest = %q, want 0.7.1", st.Latest)
	}
}

func TestUpdateManagedPrintsCommand(t *testing.T) {
	env, stdout, stderr, _ := cmdUpdateTestEnv(t, "0.7.0")

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

	code := run(t, env, "update")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if want := "ajent was installed with Homebrew. Upgrade with: brew upgrade ajent\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}

	after, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("reading exe: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("a managed install's exe changed")
	}
}

func TestUpdateTamperedExit1(t *testing.T) {
	env, _, stderr, exePath := cmdUpdateTestEnv(t, "0.7.0")
	cmdUpdateTestScriptInstall(t, env, exePath)

	priv := cmdUpdateTestKey(t)
	assets := fakerelease.BuildAssets(t, "0.7.1", runtime.GOOS, runtime.GOARCH, []byte("fake-binary"), priv)
	assets["checksums.txt"][0] ^= 0xFF

	srv := cmdUpdateTestFakeRelease(t)
	srv.AddRelease("0.7.1", false, assets)

	before, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("reading exe: %v", err)
	}

	code := run(t, env, "update")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (stderr: %s)", code, stderr.String())
	}

	after, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("reading exe: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("exe changed after a tampered update")
	}
}

func TestUpdateAutoOnOffStatus(t *testing.T) {
	env, stdout, stderr, exePath := cmdUpdateTestEnv(t, "0.7.0")
	cmdUpdateTestScriptInstall(t, env, exePath)

	if code := run(t, env, "update", "--auto", "on"); code != 0 {
		t.Fatalf("--auto on: exit=%d stderr=%s", code, stderr.String())
	}
	if got, want := stdout.String(), "automatic updates: on (script install)\n"; got != want {
		t.Errorf("--auto on stdout = %q, want %q", got, want)
	}
	stdout.Reset()

	if code := run(t, env, "update", "--auto", "off"); code != 0 {
		t.Fatalf("--auto off: exit=%d stderr=%s", code, stderr.String())
	}
	if got, want := stdout.String(), "automatic updates: off (script install)\n"; got != want {
		t.Errorf("--auto off stdout = %q, want %q", got, want)
	}
	stdout.Reset()

	if code := run(t, env, "update", "--auto", "status"); code != 0 {
		t.Fatalf("--auto status: exit=%d stderr=%s", code, stderr.String())
	}
	if got, want := stdout.String(), "automatic updates: off (script install)\n"; got != want {
		t.Errorf("--auto status stdout = %q, want %q", got, want)
	}
	stdout.Reset()

	env.Getenv = func(k string) string {
		if k == "AJENT_AUTO_UPDATE" {
			return "0"
		}
		return ""
	}
	if code := run(t, env, "update", "--auto", "status"); code != 0 {
		t.Fatalf("--auto status with override: exit=%d stderr=%s", code, stderr.String())
	}
	want := "automatic updates: off (script install)\nenv AJENT_AUTO_UPDATE=0 overrides this\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestUpdateBadAutoValueUsage(t *testing.T) {
	env, _, stderr, exePath := cmdUpdateTestEnv(t, "0.7.0")
	cmdUpdateTestScriptInstall(t, env, exePath)

	code := run(t, env, "update", "--auto", "bogus")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %s)", code, stderr.String())
	}
	if stderr.String() == "" {
		t.Error("stderr is empty, want a usage message")
	}
}

func TestUpdatePinnedVersion(t *testing.T) {
	env, stdout, stderr, exePath := cmdUpdateTestEnv(t, "0.7.0")
	cmdUpdateTestScriptInstall(t, env, exePath)

	srv := cmdUpdateTestFakeRelease(t)
	srv.AddRelease("0.6.0", false, map[string][]byte{})

	code := run(t, env, "update", "--check", "--version", "0.6.0")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}

	var got struct {
		Current         string `json:"current"`
		Latest          string `json:"latest"`
		UpdateAvailable bool   `json:"update_available"`
		Method          string `json:"method"`
		Command         string `json:"command"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decoding stdout %q: %v", stdout.String(), err)
	}
	// 0.6.0 is older than current (0.7.0), but pinning it still counts as
	// "available" (docs/tasks/T6.3.md step 3.4): the user explicitly asked
	// for a different version.
	if got.Current != "0.7.0" || got.Latest != "0.6.0" || !got.UpdateAvailable {
		t.Errorf("update --check --version 0.6.0 = %+v, want current=0.7.0 latest=0.6.0 update_available=true", got)
	}
}
