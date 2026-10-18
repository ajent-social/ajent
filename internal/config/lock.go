// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"os"
	"time"
)

// pollInterval is how often Acquire retries a contended lock.
const pollInterval = 10 * time.Millisecond

// Lock is an exclusive advisory lock on a file.
type Lock struct {
	f *os.File
}

// Acquire takes an exclusive advisory lock on path, creating it with mode
// 0600 if it does not exist, and polls until the lock is obtained or
// timeout elapses. A zero timeout means exactly one attempt. It refuses to
// operate on a path that is a symlink.
func Acquire(path string, timeout time.Duration) (*Lock, error) {
	if err := refuseSymlink(path); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // G304: path is a caller-supplied config-tree path (docs/interfaces.md: Acquire's contract); not attacker-controlled input.
	if err != nil {
		return nil, fmt.Errorf("config: open %s: %w", path, err)
	}

	deadline := time.Now().Add(timeout)
	for {
		ok, err := tryLock(f)
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("config: lock %s: %w", path, err)
		}
		if ok {
			return &Lock{f: f}, nil
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, ErrLockTimeout
		}
		time.Sleep(pollInterval)
	}
}

// Release releases the lock and closes the underlying file. It is safe to
// call twice; the second call returns nil.
func (l *Lock) Release() error {
	if l.f == nil {
		return nil
	}
	f := l.f
	l.f = nil

	unlockErr := unlock(f)
	closeErr := f.Close()
	if unlockErr != nil {
		return fmt.Errorf("config: unlock: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("config: close: %w", closeErr)
	}
	return nil
}
