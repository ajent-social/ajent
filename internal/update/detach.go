// SPDX-License-Identifier: Apache-2.0

package update

import (
	"os"
	"os/exec"
)

// detachMaxLogBytes is the size above which StartDetached truncates
// logPath before appending to it, so a long-lived background process
// never grows its log without bound.
const detachMaxLogBytes = 1 << 20 // 1 MiB

// StartDetached launches executable with args as a fully independent
// background process: a new session/process group (setsid on unix,
// CREATE_NEW_PROCESS_GROUP|DETACHED_PROCESS on windows) so it survives
// this process's exit, stdin closed, stdout and stderr appended to
// logPath (truncated first if it has grown past detachMaxLogBytes), and
// AJENT_DETACHED=1 added to its environment. It starts the process,
// releases this process's handle to it and closes the parent's copy of
// the log file, then returns without waiting for it to finish.
func StartDetached(executable string, args []string, logPath string) error {
	if info, err := os.Stat(logPath); err == nil && info.Size() > detachMaxLogBytes {
		if err := os.Truncate(logPath, 0); err != nil {
			return err
		}
	}

	log, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // G304: logPath is StartDetached's caller-supplied parameter (docs/interfaces.md), not attacker-controlled input; every call site names its own fixed log path under the ajent config directory.
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()

	// #nosec G204 -- executable and args are caller-supplied by design: StartDetached's whole purpose is launching another process (a freshly-updated ajent binary, or this same binary re-exec'd for a background check), not sanitizing external input.
	cmd := exec.Command(executable, args...)
	cmd.Stdin = nil
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.Env = append(os.Environ(), "AJENT_DETACHED=1")
	setDetached(cmd)

	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
