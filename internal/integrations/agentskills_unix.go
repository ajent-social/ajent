// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package integrations

import (
	"io/fs"
	"os"
	"syscall"
)

// checkPrivateOwnership rejects a directory or file at path that is not
// owned by the current user, or that is writable by group or other (mode
// bits 0o022 set). info must come from os.Lstat(path), already confirmed
// not to be a symlink.
func checkPrivateOwnership(_ string, info fs.FileInfo) error {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Getuid() {
		return errUnsafeSkillDestination
	}
	if info.Mode().Perm()&0o022 != 0 {
		return errUnsafeSkillDestination
	}
	return nil
}
