// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ajent-social/ajent/internal/testutil/fakeserver"
)

// binaryPath is the ajent binary TestMain builds once, before any test in
// this package runs.
var binaryPath string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "ajent-main-test")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	binaryPath = filepath.Join(tmp, "ajent")
	if runtime.GOOS == "windows" {
		binaryPath += ".exe"
	}

	build := exec.Command("go", "build", "-o", binaryPath, ".")
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building ajent: %v\n%s\n", err, out)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// runBinary execs the built ajent binary with args, isolated from the real
// host: HOME and USERPROFILE point at a fresh t.TempDir() so a command that
// happened to touch the filesystem could never reach the real home
// directory or its harness configs.
func runBinary(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()

	home := t.TempDir()
	cmd := exec.Command(binaryPath, args...) //nolint:gosec // G204: binaryPath is the binary this test built into a t.TempDir() above, not attacker-controlled input.
	cmd.Env = []string{
		"HOME=" + home,
		"USERPROFILE=" + home,
		"PATH=" + os.Getenv("PATH"),
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		t.Fatalf("running ajent %v: %v", args, runErr)
	}

	return outBuf.String(), errBuf.String(), cmd.ProcessState.ExitCode()
}

func TestBinaryVersion(t *testing.T) {
	stdout, _, code := runBinary(t, "version")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.HasPrefix(stdout, "ajent") {
		t.Errorf("stdout = %q, want it to start with %q", stdout, "ajent")
	}
}

func TestBinaryUnknown(t *testing.T) {
	stdout, stderr, code := runBinary(t, "nope")

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Errorf("stderr = %q, want it to contain %q", stderr, "unknown command")
	}
}

func TestBinaryHelp(t *testing.T) {
	stdout, _, code := runBinary(t, "help")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "version") {
		t.Errorf("stdout = %q, want it to list %q", stdout, "version")
	}
	if !strings.Contains(stdout, "help") {
		t.Errorf("stdout = %q, want it to list %q", stdout, "help")
	}
}

// TestRunDispatchesRegisteredCommand exercises the shared newTestEnv/run
// test helpers (testenv_test.go) in-process, against the real "version"
// command this package registers, so command tasks (T2.7 to T2.11) start
// from a helper pair already known to work end to end.
func TestRunDispatchesRegisteredCommand(t *testing.T) {
	srv := fakeserver.New(t)
	env, stdout, stderr, _ := newTestEnv(t, srv)

	code := run(t, env, "version")

	if code != 0 {
		t.Fatalf("run() code = %d, want 0", code)
	}
	if !strings.HasPrefix(stdout.String(), "ajent") {
		t.Errorf("stdout = %q, want it to start with %q", stdout.String(), "ajent")
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}
