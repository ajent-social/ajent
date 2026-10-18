// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package update

import (
	"os/exec"
	"syscall"
)

// setDetached starts cmd in a new session, detaching it from the parent's
// controlling terminal and process group so it survives the parent's
// exit.
func setDetached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
