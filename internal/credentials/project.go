// SPDX-License-Identifier: Apache-2.0

package credentials

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// ProjectRoot returns the nearest ancestor of cwd that looks like a
// project.
//
// cwd and home are each resolved with filepath.Abs then
// filepath.EvalSymlinks (falling back to the Abs value if EvalSymlinks
// fails). Starting at the resolved cwd and walking upward, ProjectRoot
// returns the first directory containing a .git entry of any type, or a
// regular file .aj/config.json. Home itself is checked before the walk
// stops there, so a marker at home is still found. If no marker is found
// at or above cwd by the time the walk reaches home or the filesystem
// root, ProjectRoot returns the resolved cwd unchanged.
func ProjectRoot(cwd, home string) string {
	cwd = resolvePath(cwd)
	home = resolvePath(home)

	d := cwd
	for {
		if hasProjectMarker(d) {
			return d
		}
		if d == home {
			return cwd
		}
		parent := filepath.Dir(d)
		if parent == d {
			return cwd
		}
		d = parent
	}
}

// resolvePath returns the absolute, symlink-resolved form of path. If
// filepath.Abs fails (only possible when os.Getwd fails) it keeps path
// unchanged; if filepath.EvalSymlinks then fails (for instance because a
// component does not exist) it keeps the absolute form.
func resolvePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// hasProjectMarker reports whether d looks like a project root: a .git
// entry of any type (file, directory or symlink), or a regular file
// .aj/config.json.
func hasProjectMarker(d string) bool {
	if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
		return true
	}
	info, err := os.Lstat(filepath.Join(d, ".aj", "config.json"))
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}

// ProjectScope returns the server scope identifier for project root:
// "project:" followed by the hex SHA-256 digest of root.
func ProjectScope(root string) string {
	sum := sha256.Sum256([]byte(root))
	return "project:" + hex.EncodeToString(sum[:])
}

// ProjectProfile derives a profile name for base scoped to root: the
// first 30 bytes of base (profile names are ASCII, so this is also the
// first 30 characters, cut at a byte boundary), a hyphen, and the first 8
// hex characters of the SHA-256 digest of root.
func ProjectProfile(base, root string) string {
	if len(base) > 30 {
		base = base[:30]
	}
	sum := sha256.Sum256([]byte(root))
	return base + "-" + hex.EncodeToString(sum[:])[:8]
}
