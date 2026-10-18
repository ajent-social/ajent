// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/ajent-social/ajent-client/internal/config"
)

func init() {
	Register(vscodeWriter{})
}

// vscodeWriter configures the VS Code user mcp.json so GitHub Copilot in VS
// Code can launch ajent (ADR 007). VS Code Insiders, VSCodium, Cursor and
// the workspace-level .vscode/mcp.json are out of scope; see
// docs/tasks/T4.15.md.
type vscodeWriter struct{}

// Target identifies this writer as "vscode" under the "copilot-vscode"
// profile.
func (vscodeWriter) Target() Target {
	return Target{Name: "vscode", Profile: "copilot-vscode"}
}

// userDir returns VS Code's per-OS user settings directory. It is a method
// on vscodeWriter, rather than a package-level function, so its name
// cannot collide with another writer's identically named helper in this
// package.
func (vscodeWriter) userDir(p Params) string {
	switch p.GOOS {
	case "darwin":
		return filepath.Join(p.Home, "Library", "Application Support", "Code", "User")
	case "windows":
		appdata := p.Getenv("APPDATA")
		if appdata == "" {
			appdata = filepath.Join(p.Home, "AppData", "Roaming")
		}
		return filepath.Join(appdata, "Code", "User")
	default:
		base := p.Getenv("XDG_CONFIG_HOME")
		if base == "" || !vscodeIsPosixAbs(base) {
			base = filepath.Join(p.Home, ".config")
		}
		return filepath.Join(base, "Code", "User")
	}
}

// vscodeIsPosixAbs reports whether base is an absolute POSIX path.
// XDG_CONFIG_HOME is a POSIX-only convention, so this checks the leading
// separator directly instead of filepath.IsAbs, whose result depends on
// the host OS running this code rather than the target p.GOOS; that keeps
// userDir's linux-and-others branch behaving the same on every CI runner.
func vscodeIsPosixAbs(base string) bool {
	return strings.HasPrefix(base, "/")
}

// configPath returns the absolute path to VS Code's user mcp.json.
func (w vscodeWriter) configPath(p Params) string {
	return filepath.Join(w.userDir(p), "mcp.json")
}

// Detect reports whether VS Code's user settings directory exists.
func (w vscodeWriter) Detect(p Params) bool {
	info, err := os.Stat(w.userDir(p))
	return err == nil && info.IsDir()
}

// Paths returns the single file this writer may modify.
func (w vscodeWriter) Paths(p Params) []string {
	return []string{w.configPath(p)}
}

// Apply merges the ajent server entry into VS Code's user mcp.json under
// "servers", adding "type": "stdio" as VS Code's MCP schema requires.
func (w vscodeWriter) Apply(_ context.Context, p Params) (Result, error) {
	name := w.Target().Name
	if !w.Detect(p) {
		return Result{Name: name, Status: "skipped"}, nil
	}

	entry := Entry(p, w.Target().Profile)
	entry["type"] = "stdio"

	changed, err := MergeJSONServer(w.configPath(p), "servers", entry)
	if err != nil {
		switch {
		case errors.Is(err, ErrUnparseable):
			return Result{Name: name, Status: "incomplete", Detail: "config has comments or is not plain JSON; left unchanged"}, nil
		case errors.Is(err, ErrForeignEntry):
			return Result{Name: name, Status: "incomplete", Detail: "an existing ajent entry with other settings was preserved"}, nil
		case errors.Is(err, config.ErrSymlink):
			return Result{Name: name, Status: "incomplete", Detail: "config is a symlink; left unchanged"}, nil
		default:
			return Result{}, err
		}
	}

	if !changed {
		return Result{Name: name, Status: "unchanged"}, nil
	}
	return Result{Name: name, Status: "configured"}, nil
}

// Remove deletes the ajent entry from VS Code's user mcp.json when ajent
// owns it, leaving a foreign entry untouched.
func (w vscodeWriter) Remove(_ context.Context, p Params) (Result, error) {
	name := w.Target().Name

	changed, err := RemoveJSONServer(w.configPath(p), "servers")
	if err != nil {
		if errors.Is(err, ErrForeignEntry) {
			return Result{Name: name, Status: "unchanged"}, nil
		}
		return Result{}, err
	}

	if !changed {
		return Result{Name: name, Status: "unchanged"}, nil
	}
	return Result{Name: name, Status: "removed"}, nil
}
