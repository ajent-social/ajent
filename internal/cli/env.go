// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ajent-social/ajent-client/internal/api"
	"github.com/ajent-social/ajent-client/internal/config"
)

// Dir sets e.ConfigDir from config.Dir() when it is empty, then ensures the
// directory exists with private permissions (config.EnsurePrivateDir,
// mode 0700, refuses a symlink), and returns it. Read-only code may use
// e.ConfigDir directly; any code that writes under it must call Dir first.
func (e *Env) Dir() (string, error) {
	if e.ConfigDir == "" {
		dir, err := config.Dir()
		if err != nil {
			return "", err
		}
		e.ConfigDir = dir
	}
	if err := config.EnsurePrivateDir(e.ConfigDir); err != nil {
		return "", err
	}
	return e.ConfigDir, nil
}

// DefaultEnv builds the production Env from the operating system. It never
// creates the config directory itself, so read-only commands like
// "version" and "help" have no filesystem side effects; a command that
// needs to write under ConfigDir calls (*Env).Dir first.
func DefaultEnv() (*Env, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("cli: getting working directory: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cli: getting home directory: %w", err)
	}

	configDir := os.Getenv("AJENT_CONFIG_DIR")
	if configDir != "" {
		configDir = filepath.Clean(configDir)
	} else {
		configDir = filepath.Join(home, ".aj")
	}

	return &Env{
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		Getenv:     os.Getenv,
		Cwd:        cwd,
		Home:       home,
		ConfigDir:  configDir,
		API:        &api.Client{},
		IsTerminal: isTerminal,
		OpenTTY:    openTTY,
		Now:        time.Now,
	}, nil
}
