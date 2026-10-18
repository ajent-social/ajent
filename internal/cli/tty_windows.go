// SPDX-License-Identifier: Apache-2.0

//go:build windows

package cli

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// isTerminal reports whether fd refers to a terminal.
func isTerminal(fd uintptr) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(fd), &mode) == nil
}

// winTTY combines CONIN$ (read) and CONOUT$ (write) into a single
// ReadWriteCloser: Windows has no single bidirectional handle equivalent
// to Unix's /dev/tty.
type winTTY struct {
	in  *os.File
	out *os.File
}

func (t *winTTY) Read(p []byte) (int, error)  { return t.in.Read(p) }
func (t *winTTY) Write(p []byte) (int, error) { return t.out.Write(p) }

// Close closes both handles, returning the first error encountered.
func (t *winTTY) Close() error {
	inErr := t.in.Close()
	outErr := t.out.Close()
	if inErr != nil {
		return inErr
	}
	return outErr
}

// openTTY opens the controlling terminal for interactive prompts.
func openTTY() (io.ReadWriteCloser, error) {
	in, err := os.OpenFile("CONIN$", os.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("cli: opening CONIN$: %w", err)
	}
	out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		_ = in.Close()
		return nil, fmt.Errorf("cli: opening CONOUT$: %w", err)
	}
	return &winTTY{in: in, out: out}, nil
}
