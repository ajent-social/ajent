// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// WritePrivate writes data to path atomically: a temp file created in the
// same directory, chmod 0600, fsync, then rename over path. It refuses to
// operate on a path that is a symlink.
func WritePrivate(path string, data []byte) error {
	if err := refuseSymlink(path); err != nil {
		return err
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".ajent-*")
	if err != nil {
		return fmt.Errorf("config: create temp in %s: %w", dir, err)
	}

	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmp.Name())
		}
	}()

	if runtime.GOOS != "windows" {
		if err := tmp.Chmod(0o600); err != nil {
			_ = tmp.Close()
			return fmt.Errorf("config: chmod %s: %w", tmp.Name(), err)
		}
	}

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: write %s: %w", tmp.Name(), err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: sync %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close %s: %w", tmp.Name(), err)
	}

	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("config: rename %s to %s: %w", tmp.Name(), path, err)
	}
	renamed = true

	if runtime.GOOS != "windows" {
		if d, err := os.Open(dir); err == nil { //nolint:gosec // G304: dir is derived from the caller-controlled path (docs/interfaces.md); opened only to fsync the directory entry after the rename above already completed.
			_ = d.Sync()
			_ = d.Close()
		}
	}

	return nil
}

// CreateExclusive creates path with O_EXCL and mode 0600, then writes
// data. When path already exists, the returned error satisfies
// errors.Is(err, fs.ErrExist).
func CreateExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: path is a caller-supplied config-tree path (docs/interfaces.md: CreateExclusive's contract); not attacker-controlled input.
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("config: %s: %w", path, err)
		}
		return fmt.Errorf("config: create %s: %w", path, err)
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("config: sync %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("config: close %s: %w", path, err)
	}
	return nil
}
