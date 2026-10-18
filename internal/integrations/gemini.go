// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/ajent-social/ajent/internal/config"
)

// geminiName is the Writer name and the Result.Name for Gemini CLI.
const geminiName = "gemini"

// geminiProfile is the ajent profile Gemini CLI's entry launches.
const geminiProfile = "gemini-cli"

func init() {
	Register(geminiWriter{})
}

// geminiWriter configures Gemini CLI's <Home>/.gemini/settings.json (ADR
// 007). That file also holds unrelated Gemini settings (theme, auth and
// others), which must survive byte for byte; only mcpServers.ajent is
// touched.
type geminiWriter struct{}

// Target identifies this writer to the registry.
func (geminiWriter) Target() Target {
	return Target{Name: geminiName, Profile: geminiProfile}
}

// configPath returns the absolute path to Gemini CLI's settings file.
func (geminiWriter) configPath(p Params) string {
	return filepath.Join(p.Home, ".gemini", "settings.json")
}

// Detect reports whether Gemini CLI is installed: its config directory
// exists. It never creates that directory.
func (geminiWriter) Detect(p Params) bool {
	info, err := os.Stat(filepath.Join(p.Home, ".gemini"))
	return err == nil && info.IsDir()
}

// Paths returns the absolute files this writer may modify, for backups.
func (w geminiWriter) Paths(p Params) []string {
	return []string{w.configPath(p)}
}

// Apply adds or refreshes the ajent MCP server entry under mcpServers in
// Gemini CLI's settings.json. It returns a skipped result without touching
// the filesystem when Gemini CLI is not detected, and an incomplete result
// (with a nil error) when the file cannot be safely merged.
func (w geminiWriter) Apply(_ context.Context, p Params) (Result, error) {
	if !w.Detect(p) {
		return Result{Name: geminiName, Status: "skipped"}, nil
	}

	entry := Entry(p, geminiProfile)
	changed, err := MergeJSONServer(w.configPath(p), "mcpServers", entry)
	switch {
	case err == nil:
		status := "unchanged"
		if changed {
			status = "configured"
		}
		return Result{Name: geminiName, Status: status}, nil
	case errors.Is(err, ErrUnparseable):
		return Result{Name: geminiName, Status: "incomplete", Detail: "config has comments or is not plain JSON; left unchanged"}, nil
	case errors.Is(err, ErrForeignEntry):
		return Result{Name: geminiName, Status: "incomplete", Detail: "an existing ajent entry with other settings was preserved"}, nil
	case errors.Is(err, config.ErrSymlink):
		return Result{Name: geminiName, Status: "incomplete", Detail: "config is a symlink; left unchanged"}, nil
	default:
		return Result{}, err
	}
}

// Remove deletes the ajent entry from Gemini CLI's settings.json when ajent
// owns it, leaving the rest of the file, including any other mcpServers
// entries, untouched. A foreign entry or an unparseable file is left in
// place, same as Apply, and reported as incomplete with a nil error.
func (w geminiWriter) Remove(_ context.Context, p Params) (Result, error) {
	changed, err := RemoveJSONServer(w.configPath(p), "mcpServers")
	switch {
	case err == nil:
		status := "unchanged"
		if changed {
			status = "removed"
		}
		return Result{Name: geminiName, Status: status}, nil
	case errors.Is(err, ErrUnparseable):
		return Result{Name: geminiName, Status: "incomplete", Detail: "config has comments or is not plain JSON; left unchanged"}, nil
	case errors.Is(err, ErrForeignEntry):
		return Result{Name: geminiName, Status: "incomplete", Detail: "an existing ajent entry with other settings was preserved"}, nil
	case errors.Is(err, config.ErrSymlink):
		return Result{Name: geminiName, Status: "incomplete", Detail: "config is a symlink; left unchanged"}, nil
	default:
		return Result{}, err
	}
}
