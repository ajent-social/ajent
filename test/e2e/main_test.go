// SPDX-License-Identifier: Apache-2.0

// Package e2e builds the real ajent binary once (TestMain) and drives it as
// a subprocess against internal/testutil/fakeserver, so the MCP session
// tests exercise the exact bytes a coding tool would see over stdio
// (docs/tasks/T3.7.md). T6.8 adds upgrade-focused, e2e-tagged files to this
// same package; it never declares TestMain.
package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// ajentBin is the path to the ajent binary TestMain builds once, before any
// test in this package runs.
var ajentBin string

func TestMain(m *testing.M) {
	os.Exit(e2eRunMain(m))
}

// e2eRunMain builds the ajent binary into a temporary directory (removed
// before this function returns), runs the package's tests and reports the
// process exit code. It is split out from TestMain so the deferred cleanup
// still runs before the process exits (os.Exit in TestMain itself would
// skip defers registered there).
func e2eRunMain(m *testing.M) int {
	root, err := e2eModuleRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	tmp, err := os.MkdirTemp("", "ajent-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	ajentBin = filepath.Join(tmp, "ajent")
	if runtime.GOOS == "windows" {
		ajentBin += ".exe"
	}

	build := exec.Command("go", "build", "-o", ajentBin, "./cmd/ajent")
	build.Dir = root
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building ajent: %v\n%s\n", err, out)
		return 1
	}

	return m.Run()
}

// e2eModuleRoot locates the module root by walking up from the working
// directory until go.mod is found.
func e2eModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("e2e: go.mod not found above %s", dir)
		}
		dir = parent
	}
}
