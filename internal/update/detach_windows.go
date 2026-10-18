// SPDX-License-Identifier: Apache-2.0

//go:build windows

package update

import (
	"os/exec"
	"syscall"
)

// detachCreationFlags is CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS.
const detachCreationFlags = 0x00000200 | 0x00000008

// setDetached starts cmd detached from the parent's console and in its
// own process group, so it survives the parent's exit.
func setDetached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachCreationFlags, HideWindow: true}
}
