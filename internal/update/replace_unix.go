// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package update

import (
	"errors"
	"io"
	"os"
)

// replaceExecutable atomically replaces exe with newPath. On unix,
// os.Rename within one directory is atomic.
func replaceExecutable(newPath, exe string) error {
	return os.Rename(newPath, exe)
}

// applyTestLockShareRead exists only so apply_test.go's
// TestReplaceWindowsRollback (which t.Skips immediately outside windows)
// compiles on every platform. It is never called here.
func applyTestLockShareRead(string) (io.Closer, error) {
	return nil, errors.New("update: applyTestLockShareRead is windows-only")
}
