// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package config

import (
	"errors"
	"os"
	"syscall"
)

// tryLock attempts a single non-blocking exclusive flock on f. It reports
// (false, nil) when the lock is already held elsewhere.
func tryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return false, err
}

// unlock releases the flock held on f.
func unlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
