// SPDX-License-Identifier: Apache-2.0

//go:build windows

package update

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// replaceExecutable replaces the running exe on Windows, where a binary
// cannot be overwritten or removed while it is executing but can be
// renamed. It renames exe aside to exe+".old" (removing any leftover
// ".old" first), then renames newPath into place. If the second rename
// fails, it renames ".old" back to exe to restore the original binary and
// returns the second rename's error.
func replaceExecutable(newPath, exe string) error {
	old := exe + ".old"

	if err := os.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("update: removing %s: %w", old, err)
	}

	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("update: renaming %s to %s: %w", exe, old, err)
	}

	if err := os.Rename(newPath, exe); err != nil {
		if rbErr := os.Rename(old, exe); rbErr != nil {
			return fmt.Errorf("update: renaming %s to %s: %w (rollback also failed: %v)", newPath, exe, err, rbErr)
		}
		return fmt.Errorf("update: renaming %s to %s: %w", newPath, exe, err)
	}

	return nil
}

// applyTestLockShareRead opens path with a Windows share mode that grants
// only read sharing (no write, no delete), for apply_test.go's
// TestReplaceWindowsRollback. Go's standard os.OpenFile always requests
// FILE_SHARE_DELETE on Windows (so Rename and Remove behave like on Unix
// even while a file is open elsewhere in this same process), so
// reproducing a genuine "another handle has this file open exclusively"
// sharing violation needs the raw Windows API.
func applyTestLockShareRead(path string) (io.Closer, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(
		p,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, err
	}
	return applyTestWinHandle{h: h}, nil
}

// applyTestWinHandle adapts a raw windows.Handle to io.Closer for
// applyTestLockShareRead.
type applyTestWinHandle struct{ h windows.Handle }

func (c applyTestWinHandle) Close() error { return windows.CloseHandle(c.h) }
