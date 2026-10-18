// SPDX-License-Identifier: Apache-2.0

//go:build windows

package config

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLock attempts a single non-blocking exclusive LockFileEx on f. It
// reports (false, nil) when the lock is already held elsewhere.
func tryLock(f *os.File) (bool, error) {
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&windows.Overlapped{},
	)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return false, err
}

// unlock releases the LockFileEx lock held on f.
func unlock(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
