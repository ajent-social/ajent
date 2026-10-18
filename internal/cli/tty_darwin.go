// SPDX-License-Identifier: Apache-2.0

//go:build darwin

package cli

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// isTerminal reports whether fd refers to a terminal.
func isTerminal(fd uintptr) bool {
	_, err := unix.IoctlGetTermios(int(fd), unix.TIOCGETA)
	return err == nil
}

// openTTY opens the controlling terminal for interactive prompts.
func openTTY() (io.ReadWriteCloser, error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("cli: opening /dev/tty: %w", err)
	}
	return f, nil
}
