// SPDX-License-Identifier: Apache-2.0

//go:build e2e

// This file provides the shared plumbing TestUpgrade (upgrade_test.go)
// uses to build the three fixture binaries, drive them as an isolated
// script install, and inspect the files they write (docs/tasks/T6.8.md).
// Every declaration here is prefixed "upgrade" so it never collides with a
// helper test/e2e/main_test.go or mcp_test.go (T3.7) declares, whichever
// lands first in this package (docs/tasks/OWNERSHIP.md). It never declares
// TestMain: T3.7's TestMain (main_test.go) runs for the whole package.
package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// upgradeModuleRoot locates the repository root (the directory holding
// go.mod) by walking up from the current working directory. It duplicates
// e2eModuleRoot (main_test.go, T3.7) on purpose, so this file has no
// dependency on which lane's file happens to land in the package first.
func upgradeModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("upgrade: getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("upgrade: go.mod not found above %s", dir)
		}
		dir = parent
	}
}

// upgradeExeName appends ".exe" to name on windows (docs/tasks/T6.8.md
// step 2.2).
func upgradeExeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// upgradeBuild builds github.com/ajent-social/ajent-client/cmd/ajent into
// outPath, stamping it with version and pubB64 (the throwaway release
// public key TestUpgrade generates for the whole run), so the built binary
// trusts only that key (docs/tasks/T6.8.md step 2.2). It never inherits an
// extra environment beyond the host's, since building a Go program is not
// part of the host-safety boundary the built binaries themselves respect.
func upgradeBuild(t *testing.T, root, outPath, version, pubB64 string) {
	t.Helper()

	ldflags := fmt.Sprintf(
		"-X github.com/ajent-social/ajent-client/internal/version.Version=%s -X github.com/ajent-social/ajent-client/internal/update.extraKeyB64=%s",
		version, pubB64,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "build", "-o", outPath, "-ldflags", ldflags, "github.com/ajent-social/ajent-client/cmd/ajent")
	cmd.Dir = root
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("upgrade: building ajent %s: %v\n%s", version, err, out)
	}
}

// upgradeSystemPathDirs returns the minimal, always-present system
// directories for goos, so a subprocess's PATH resolves ordinary system
// utilities but never a real coding-tool binary a package manager or shell
// profile installed on the host (docs/tasks/T6.8.md step 2.5: "PATH=<dir
// of bin> plus the OS system dirs (so no real claude or codex binary is
// found)").
func upgradeSystemPathDirs(goos string) []string {
	if goos == "windows" {
		win := os.Getenv("SystemRoot")
		if win == "" {
			win = `C:\Windows`
		}
		return []string{win, filepath.Join(win, "System32")}
	}
	return []string{"/usr/bin", "/bin"}
}

// upgradeEnv builds the clean environment list every subprocess in
// TestUpgrade runs with (docs/tasks/T6.8.md step 2.5): an isolated home,
// config directory and PATH, and the fakerelease server's API and
// fallback URLs. It never inherits this test process's own environment
// (in particular CI and AJENT_AUTO_UPDATE, which the test process may
// have set), so every host-safety and opt-out assertion in TestUpgrade
// exercises exactly the variables it sets itself.
func upgradeEnv(home, cfg, binDir, apiURL, fallbackURL string) []string {
	path := binDir
	for _, dir := range upgradeSystemPathDirs(runtime.GOOS) {
		path += string(os.PathListSeparator) + dir
	}

	env := []string{
		"HOME=" + home,
		"USERPROFILE=" + home,
		"AJENT_CONFIG_DIR=" + cfg,
		"AJENT_UPDATE_API=" + apiURL,
		"AJENT_UPDATE_FALLBACK_URL=" + fallbackURL,
		"PATH=" + path,
	}
	if runtime.GOOS == "windows" {
		env = append(env,
			"APPDATA="+filepath.Join(home, "AppData", "Roaming"),
			"LOCALAPPDATA="+filepath.Join(home, "AppData", "Local"),
		)
	}
	return env
}

// upgradeWithEnv returns a copy of base with extra appended, for the one
// or two invocations in TestUpgrade that need an extra variable (phase E's
// AJENT_AUTO_UPDATE=0).
func upgradeWithEnv(base []string, extra ...string) []string {
	out := make([]string, 0, len(base)+len(extra))
	out = append(out, base...)
	out = append(out, extra...)
	return out
}

// upgradeRun runs name with args, dir as its working directory and env as
// its entire environment (never this test process's own), waits up to 60s,
// and returns its combined stdout+stderr and exit code. It fails the test
// outright only when the process cannot be started or reported at all (for
// example a timeout), since every command TestUpgrade runs this way is
// expected to exit cleanly with a code the caller inspects.
func upgradeRun(t *testing.T, dir string, env []string, name string, args ...string) (output string, exitCode int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()
	if err == nil {
		return buf.String(), 0
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return buf.String(), exitErr.ExitCode()
	}

	t.Fatalf("upgrade: running %s %v: %v\noutput:\n%s", name, args, err, buf.String())
	return "", -1
}

// upgradeBinVersion runs "<bin> version --json" and returns the decoded
// version field, or "" when the command fails or its output does not
// parse as JSON (for example while a replace is briefly in flight).
func upgradeBinVersion(t *testing.T, dir string, env []string, bin string) string {
	t.Helper()

	out, code := upgradeRun(t, dir, env, bin, "version", "--json")
	if code != 0 {
		return ""
	}
	var info struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return ""
	}
	return info.Version
}

// upgradePollVersion polls "<bin> version --json" every 500ms until it
// reports want, failing the test if timeout elapses first
// (docs/tasks/T6.8.md step 3.2).
func upgradePollVersion(t *testing.T, dir string, env []string, bin, want string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		last = upgradeBinVersion(t, dir, env, bin)
		if last == want {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("upgrade: %s version --json never reported %q within %s (last seen: %q)", bin, want, timeout, last)
}

// upgradeReadFile reads path, returning nil (never failing the test) when
// it does not yet exist, so callers can poll a file a detached background
// process has not created or finished writing yet.
func upgradeReadFile(path string) []byte {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is always a caller-built path under this test's own t.TempDir() sandbox.
	if err != nil {
		return nil
	}
	return data
}

// upgradePollFileContains polls path every 500ms until its contents
// contain want, failing the test if timeout elapses first.
func upgradePollFileContains(t *testing.T, path, want string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		last = string(upgradeReadFile(path))
		if strings.Contains(last, want) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("upgrade: %s never contained %q within %s; contents:\n%s", path, want, timeout, last)
}

// upgradePollUntil polls cond every interval until it returns true, or
// fails the test with msg once timeout elapses (after one final check).
func upgradePollUntil(t *testing.T, timeout, interval time.Duration, msg string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(interval)
	}
	if !cond() {
		t.Fatalf("upgrade: timed out after %s waiting for: %s", timeout, msg)
	}
}

// upgradeCursorConfig is the subset of Cursor's mcp.json this test
// inspects: the "ajent" and "other" entries under "mcpServers"
// (internal/integrations/cursor.go, T4.4/T4.17).
type upgradeCursorConfig struct {
	MCPServers map[string]map[string]any `json:"mcpServers"`
}

// upgradeReadCursorConfig reads and decodes path as a Cursor mcp.json
// file, failing the test if it is missing or not valid JSON.
func upgradeReadCursorConfig(t *testing.T, path string) upgradeCursorConfig {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // G304: path is always a caller-built path under this test's own t.TempDir() sandbox.
	if err != nil {
		t.Fatalf("upgrade: reading %s: %v", path, err)
	}
	var cfg upgradeCursorConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("upgrade: parsing %s: %v", path, err)
	}
	return cfg
}

// upgradeSamePath reports whether a and b name the same file once symlinks
// are resolved on each side (docs/tasks/T6.8.md step 4.2: "compare with
// filepath.EvalSymlinks on both sides"). A path that does not resolve (for
// example because it does not exist) is compared literally.
func upgradeSamePath(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = a
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = b
	}
	return ra == rb
}

// upgradeBackupIDs lists the backup directory names under
// <cfg>/backups, or nil when that directory does not exist yet
// (internal/integrations/backup.go, Snapshot).
func upgradeBackupIDs(t *testing.T, cfg string) []string {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(cfg, "backups"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("upgrade: reading backups directory: %v", err)
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids
}

// upgradeMCPRequestDeadline bounds how long upgradeMCPInitialize waits for
// a response line before it fails the test.
const upgradeMCPRequestDeadline = 15 * time.Second

// upgradeMCPInitialize spawns command with args, dir and env, writes one
// newline-delimited MCP "initialize" request to its stdin, reads the
// first response line, closes stdin and waits for the process to exit
// (killing it after 5s if it does not), then returns the decoded
// JSON-RPC "result" object (docs/tasks/T6.8.md step 5).
func upgradeMCPInitialize(t *testing.T, dir string, env []string, command string, args []string) map[string]any {
	t.Helper()

	cmd := exec.Command(command, args...) //nolint:gosec // G204: command and args come from this test's own fixture, the Cursor entry it just wrote and then read back.
	cmd.Dir = dir
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("upgrade: opening stdin pipe for %s: %v", command, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("upgrade: opening stdout pipe for %s: %v", command, err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("upgrade: starting %s %v: %v", command, args, err)
	}

	const req = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e","version":"0"}}}` + "\n"
	if _, err := io.WriteString(stdin, req); err != nil {
		t.Fatalf("upgrade: writing initialize request to %s: %v", command, err)
	}

	lineCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 4<<20)
		if scanner.Scan() {
			lineCh <- scanner.Text()
			return
		}
		if err := scanner.Err(); err != nil {
			errCh <- err
			return
		}
		errCh <- io.EOF
	}()

	var resp map[string]any
	select {
	case line := <-lineCh:
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			_ = cmd.Process.Kill()
			t.Fatalf("upgrade: initialize response from %s is not JSON: %v\nline: %s", command, err, line)
		}
	case err := <-errCh:
		_ = cmd.Process.Kill()
		t.Fatalf("upgrade: reading initialize response from %s: %v; stderr:\n%s", command, err, stderr.String())
	case <-time.After(upgradeMCPRequestDeadline):
		_ = cmd.Process.Kill()
		t.Fatalf("upgrade: timed out waiting for an initialize response from %s; stderr:\n%s", command, stderr.String())
	}

	_ = stdin.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}

	result, _ := resp["result"].(map[string]any)
	if result == nil {
		t.Fatalf("upgrade: initialize response from %s has no result object: %#v (stderr:\n%s)", command, resp, stderr.String())
	}
	return result
}

// upgradeWaitQuiet polls path's size every 200ms until it stops growing
// for at least quiet, or timeout elapses, whichever comes first. There is
// no PID tracking for the detached background processes the hook spawns
// (docs/tasks/T6.8.md step 8), so this is the documented fallback: give a
// still-running process a chance to finish writing its log and exit
// before this test's cleanup starts removing its t.TempDir() sandboxes.
func upgradeWaitQuiet(path string, quiet, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	lastSize := int64(-1)
	stableSince := time.Now()
	for time.Now().Before(deadline) {
		size := int64(0)
		if info, err := os.Stat(path); err == nil {
			size = info.Size()
		}
		if size != lastSize {
			lastSize = size
			stableSince = time.Now()
		}
		if time.Since(stableSince) >= quiet {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// upgradeCleanupRetryRemove best-effort removes path, retrying on windows
// (docs/tasks/T6.8.md step 8: "Use t.Cleanup with retries on windows for
// file locks, up to 10 tries every 500ms") since a just-exited detached
// process can hold a brief handle on its own executable there. It is a
// no-op when path does not exist, and never fails the test: a leftover
// file here only means t.TempDir()'s own cleanup has to cope with it.
func upgradeCleanupRetryRemove(path string) {
	tries := 1
	if runtime.GOOS == "windows" {
		tries = 10
	}
	for i := 0; i < tries; i++ {
		err := os.Remove(path)
		if err == nil || os.IsNotExist(err) {
			return
		}
		if i < tries-1 {
			time.Sleep(500 * time.Millisecond)
		}
	}
}
