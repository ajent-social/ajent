// SPDX-License-Identifier: Apache-2.0

// package update, not update_test: TestReplaceWindowsRollback below needs
// whitebox access to the unexported replaceExecutable and
// applyTestLockShareRead (replace_unix.go/replace_windows.go). apply_test.go
// cannot hold it: it must be package update_test to use fakerelease
// (which itself imports update), and a whitebox update file importing
// fakerelease would form an import cycle in the test binary.
package update

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestHelperProcess is not a real test: it is re-executed as a subprocess
// by StartDetached tests below (os.Args[0] with -test.run=TestHelperProcess
// and GO_HELPER=1), and simply writes a fixed line to stdout before
// exiting. When run as part of the normal test suite (GO_HELPER unset), it
// skips immediately.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_HELPER") != "1" {
		t.Skip("detach: TestHelperProcess only runs as a re-exec'd helper")
	}
	if _, err := os.Stdout.WriteString("hello"); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

// detachTestPollLog polls path until its content contains want or timeout
// elapses, failing the test in the latter case.
func detachTestPollLog(t *testing.T, path, want string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		data, _ := os.ReadFile(path) //nolint:gosec // G304: path is a t.TempDir() path this same test created.
		if strings.Contains(string(data), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("log %s did not contain %q within %s; got %q", path, want, timeout, data)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStartDetachedWritesLog(t *testing.T) {
	t.Setenv("GO_HELPER", "1")

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	logPath := filepath.Join(t.TempDir(), "detach.log")

	start := time.Now()
	if err := StartDetached(exe, []string{"-test.run=TestHelperProcess"}, logPath); err != nil {
		t.Fatalf("StartDetached: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Errorf("StartDetached took %s, want under 1s", elapsed)
	}

	detachTestPollLog(t, logPath, "hello", 5*time.Second)
}

func TestStartDetachedTruncatesLargeLog(t *testing.T) {
	t.Setenv("GO_HELPER", "1")

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	logPath := filepath.Join(t.TempDir(), "detach.log")
	old := bytes.Repeat([]byte("x"), detachMaxLogBytes+1)
	if err := os.WriteFile(logPath, old, 0o600); err != nil {
		t.Fatalf("seeding log: %v", err)
	}

	if err := StartDetached(exe, []string{"-test.run=TestHelperProcess"}, logPath); err != nil {
		t.Fatalf("StartDetached: %v", err)
	}

	detachTestPollLog(t, logPath, "hello", 5*time.Second)

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading log: %v", err)
	}
	if bytes.Contains(data, []byte("xxxx")) {
		t.Errorf("log still contains pre-truncation content, len=%d", len(data))
	}
	if len(data) >= len(old) {
		t.Errorf("log length = %d, want it truncated well below the original %d bytes", len(data), len(old))
	}
}

// TestReplaceWindowsRollback exercises replace_windows.go's rollback
// branch: replaceExecutable's second rename (newPath -> exe) fails
// because newPath itself is held open under a Windows share mode that
// excludes delete access (so it cannot be renamed away), after the first
// rename (exe -> exe+".old") has already succeeded. It asserts the
// original exe is restored and the leftover ".old" is gone.
func TestReplaceWindowsRollback(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("TestReplaceWindowsRollback only runs on windows")
	}

	dir := t.TempDir()
	exe := filepath.Join(dir, "ajent.exe")
	original := []byte("original-exe-bytes")
	if err := os.WriteFile(exe, original, 0o755); err != nil { //nolint:gosec // G306: a real executable needs this mode; the path is under t.TempDir().
		t.Fatalf("seeding exe: %v", err)
	}

	newPath := filepath.Join(dir, "ajent.exe.new")
	if err := os.WriteFile(newPath, []byte("new-exe-bytes"), 0o755); err != nil { //nolint:gosec // G306: a real executable needs this mode; the path is under t.TempDir().
		t.Fatalf("seeding new binary: %v", err)
	}

	lock, err := applyTestLockShareRead(newPath)
	if err != nil {
		t.Fatalf("applyTestLockShareRead: %v", err)
	}
	defer func() { _ = lock.Close() }()

	err = replaceExecutable(newPath, exe)
	if err == nil {
		t.Fatal("replaceExecutable() succeeded, want a sharing-violation error")
	}

	after, readErr := os.ReadFile(exe) //nolint:gosec // G304: exe is this test's own t.TempDir() file.
	if readErr != nil {
		t.Fatalf("reading exe after failed replace: %v", readErr)
	}
	if !bytes.Equal(after, original) {
		t.Errorf("exe = %q after rollback, want the original %q", after, original)
	}

	if _, statErr := os.Stat(exe + ".old"); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("exe.old still exists after rollback: %v", statErr)
	}
}
