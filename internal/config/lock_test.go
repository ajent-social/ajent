// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// lockHelperEnv names the environment variable that tells TestLockHelper
// which lock file to acquire, and signals it is running as a subprocess
// helper rather than as a normal test.
const lockHelperEnv = "AJENT_LOCK_HELPER"

func TestLockExclusionAcrossProcesses(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "lock")

	cmd := exec.Command(os.Args[0], "-test.run=^TestLockHelper$") //nolint:gosec // G204: standard Go re-exec-self test helper pattern; os.Args[0] is this test binary's own path, not external input.
	cmd.Env = append(os.Environ(), lockHelperEnv+"="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatalf("helper produced no output: %v (stderr: %s)", scanner.Err(), stderr.String())
	}
	if got := scanner.Text(); got != "locked" {
		t.Fatalf("helper output = %q, want %q", got, "locked")
	}

	if _, err := Acquire(path, 200*time.Millisecond); !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("Acquire() while held error = %v, want ErrLockTimeout", err)
	}

	waitErr := cmd.Wait()
	waited = true
	if waitErr != nil {
		t.Fatalf("helper process: %v (stderr: %s)", waitErr, stderr.String())
	}

	lock, err := Acquire(path, time.Second)
	if err != nil {
		t.Fatalf("Acquire() after release error = %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

// TestLockHelper is not a real test. It is invoked as a subprocess by
// TestLockExclusionAcrossProcesses. It returns immediately unless
// AJENT_LOCK_HELPER is set, in which case it acquires the lock at that
// path, prints "locked", then holds the lock for 2s.
func TestLockHelper(_ *testing.T) {
	path := os.Getenv(lockHelperEnv)
	if path == "" {
		return
	}

	lock, err := Acquire(path, time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "helper Acquire() error = %v\n", err)
		os.Exit(1)
	}
	fmt.Println("locked")
	time.Sleep(2 * time.Second)
	_ = lock.Release()
}

func TestLockReleaseTwice(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "lock")

	lock, err := Acquire(path, time.Second)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("first Release() error = %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("second Release() error = %v", err)
	}
}
