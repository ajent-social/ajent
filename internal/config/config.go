// SPDX-License-Identifier: Apache-2.0

// Package config provides the private filesystem primitives ajent uses to
// store credentials, profiles and locks: the config directory, atomic
// private writes, exclusive creation, symlink refusal and a cross-process
// advisory lock.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrSymlink is returned when a path that must be a regular file or
// directory turns out to be a symlink.
var ErrSymlink = errors.New("config: refusing symlink")

// ErrLockTimeout is returned by Acquire when a lock could not be obtained
// before the timeout elapsed.
var ErrLockTimeout = errors.New("config: lock timeout")

// Dir returns the ajent config directory: $AJENT_CONFIG_DIR if it is set,
// otherwise ~/.aj. It creates the directory with mode 0700 and refuses a
// symlink.
func Dir() (string, error) {
	dir := os.Getenv("AJENT_CONFIG_DIR")
	if dir != "" {
		expanded, err := expandHome(dir)
		if err != nil {
			return "", err
		}
		abs, err := filepath.Abs(expanded)
		if err != nil {
			return "", fmt.Errorf("config: %w", err)
		}
		dir = abs
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("config: %w", err)
		}
		dir = filepath.Join(home, ".aj")
	}

	if err := EnsurePrivateDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// expandHome expands a leading "~" followed by the path separator (or a
// bare "~") using the current user's home directory. Any other path is
// returned unchanged.
func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}

// EnsurePrivateDir creates path, and any missing parents, with mode 0700,
// and refuses to operate on a path that is a symlink.
func EnsurePrivateDir(path string) error {
	if err := refuseSymlink(path); err != nil {
		return err
	}

	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", path, err)
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o700); err != nil { //nolint:gosec // G302: this chmods a directory, not a file; 0700 (owner rwx) is the deliberate private-directory mode and needs the execute bit to be enterable.
			return fmt.Errorf("config: chmod %s: %w", path, err)
		}
	}

	// Lstat again to close the race between MkdirAll/Chmod and a symlink
	// swapped in concurrently.
	return refuseSymlink(path)
}

// refuseSymlink returns an error wrapping ErrSymlink if path exists and is
// a symlink. A missing path is not an error.
func refuseSymlink(path string) error {
	info, err := os.Lstat(path) //nolint:gosec // G703: path is a caller-supplied config-tree path (docs/interfaces.md: "functions that touch the filesystem take the config directory as a parameter"); callers, not attackers, control it.
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("config: lstat %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("config: %s: %w", path, ErrSymlink)
	}
	return nil
}

// ReadPrivate reads path, refusing a symlink. Errors satisfy
// errors.Is(err, fs.ErrNotExist) when the file does not exist.
func ReadPrivate(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("config: lstat %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("config: %s: %w", path, ErrSymlink)
	}

	data, err := os.ReadFile(path) //nolint:gosec // G304: path is a caller-supplied config-tree path (docs/interfaces.md); ReadPrivate's contract puts path validation on the caller, not attacker input.
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	return data, nil
}

// ReadJSON reads path with ReadPrivate and unmarshals it into v.
func ReadJSON(path string, v any) error {
	data, err := ReadPrivate(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("config: unmarshal %s: %w", path, err)
	}
	return nil
}

// WriteJSON marshals v as indented JSON, appends a trailing newline, and
// writes it to path with WritePrivate.
func WriteJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshal %s: %w", path, err)
	}
	data = append(data, '\n')
	return WritePrivate(path, data)
}
