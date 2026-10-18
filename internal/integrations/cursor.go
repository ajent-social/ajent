// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/ajent-social/ajent/internal/config"
)

func init() {
	Register(cursorWriter{})
}

// cursorWriter configures the ajent MCP server in Cursor's
// <Home>/.cursor/mcp.json (ADR 007).
type cursorWriter struct{}

// Target identifies this writer to the registry.
func (cursorWriter) Target() Target {
	return Target{Name: "cursor", Profile: "cursor"}
}

// configPath returns the absolute path to Cursor's MCP config file.
func (cursorWriter) configPath(p Params) string {
	return filepath.Join(p.Home, ".cursor", "mcp.json")
}

// Detect reports whether Cursor's config directory exists. It never
// creates the directory.
func (cursorWriter) Detect(p Params) bool {
	info, err := os.Stat(filepath.Join(p.Home, ".cursor"))
	if err != nil {
		return false
	}
	return info.IsDir()
}

// Paths returns the files this writer may modify, for backups.
func (w cursorWriter) Paths(p Params) []string {
	return []string{w.configPath(p)}
}

// Apply adds or replaces the ajent entry in Cursor's mcp.json. It never
// touches any other key in the file.
func (w cursorWriter) Apply(_ context.Context, p Params) (Result, error) {
	if !w.Detect(p) {
		return Result{Name: "cursor", Status: "skipped"}, nil
	}

	entry := Entry(p, "cursor")
	changed, err := MergeJSONServer(w.configPath(p), "mcpServers", entry)
	switch {
	case err == nil:
		if changed {
			return Result{Name: "cursor", Status: "configured"}, nil
		}
		return Result{Name: "cursor", Status: "unchanged"}, nil
	case errors.Is(err, ErrUnparseable):
		return Result{Name: "cursor", Status: "incomplete", Detail: "config has comments or is not plain JSON; left unchanged"}, nil
	case errors.Is(err, ErrForeignEntry):
		return Result{Name: "cursor", Status: "incomplete", Detail: "an existing ajent entry with other settings was preserved"}, nil
	case errors.Is(err, config.ErrSymlink):
		return Result{Name: "cursor", Status: "incomplete", Detail: "config is a symlink; left unchanged"}, nil
	default:
		return Result{}, err
	}
}

// Remove deletes the ajent entry from Cursor's mcp.json when ajent owns
// it. A foreign entry is left in place.
func (w cursorWriter) Remove(_ context.Context, p Params) (Result, error) {
	changed, err := RemoveJSONServer(w.configPath(p), "mcpServers")
	switch {
	case err == nil:
		if changed {
			return Result{Name: "cursor", Status: "removed"}, nil
		}
		return Result{Name: "cursor", Status: "unchanged"}, nil
	case errors.Is(err, ErrForeignEntry):
		return Result{Name: "cursor", Status: "unchanged", Detail: "an existing ajent entry with other settings was preserved"}, nil
	case errors.Is(err, ErrUnparseable):
		return Result{Name: "cursor", Status: "incomplete", Detail: "config has comments or is not plain JSON; left unchanged"}, nil
	case errors.Is(err, config.ErrSymlink):
		return Result{Name: "cursor", Status: "incomplete", Detail: "config is a symlink; left unchanged"}, nil
	default:
		return Result{}, err
	}
}
