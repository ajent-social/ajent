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
	Register(copilotWriter{})
}

// copilotWriter configures the ajent MCP server for GitHub Copilot CLI by
// merging an entry into <Home>/.copilot/mcp-config.json (ADR 007). Copilot
// CLI entries additionally require "type": "local" and "tools": ["*"].
type copilotWriter struct{}

// Target identifies this writer as "copilot", profile "copilot".
func (copilotWriter) Target() Target {
	return Target{Name: "copilot", Profile: "copilot"}
}

// copilotConfigPath returns the Copilot CLI MCP config file under p.Home.
func copilotConfigPath(p Params) string {
	return filepath.Join(p.Home, ".copilot", "mcp-config.json")
}

// Detect reports whether the Copilot CLI config directory exists. It never
// creates the directory.
func (copilotWriter) Detect(p Params) bool {
	info, err := os.Stat(filepath.Join(p.Home, ".copilot"))
	return err == nil && info.IsDir()
}

// Paths returns the files this writer may modify, for backups.
func (w copilotWriter) Paths(p Params) []string {
	return []string{copilotConfigPath(p)}
}

// Apply merges the ajent entry into mcpServers in the Copilot CLI config.
// It reports "skipped" when Copilot CLI is not detected, and degrades to
// "incomplete" (Go error nil) rather than a partial write when the config
// cannot be safely merged.
func (w copilotWriter) Apply(_ context.Context, p Params) (Result, error) {
	if !w.Detect(p) {
		return Result{Name: "copilot", Status: "skipped"}, nil
	}

	e := Entry(p, "copilot")
	e["type"] = "local"
	e["tools"] = []any{"*"}

	changed, err := MergeJSONServer(copilotConfigPath(p), "mcpServers", e)
	switch {
	case err == nil:
		if changed {
			return Result{Name: "copilot", Status: "configured"}, nil
		}
		return Result{Name: "copilot", Status: "unchanged"}, nil
	case errors.Is(err, ErrUnparseable):
		return Result{Name: "copilot", Status: "incomplete", Detail: "config has comments or is not plain JSON; left unchanged"}, nil
	case errors.Is(err, ErrForeignEntry):
		return Result{Name: "copilot", Status: "incomplete", Detail: "an existing ajent entry with other settings was preserved"}, nil
	case errors.Is(err, config.ErrSymlink):
		return Result{Name: "copilot", Status: "incomplete", Detail: "config is a symlink; left unchanged"}, nil
	default:
		return Result{}, err
	}
}

// Remove deletes the ajent entry from mcpServers in the Copilot CLI config,
// only when ajent owns it. It reports "skipped" when Copilot CLI is not
// detected.
func (w copilotWriter) Remove(_ context.Context, p Params) (Result, error) {
	if !w.Detect(p) {
		return Result{Name: "copilot", Status: "skipped"}, nil
	}

	changed, err := RemoveJSONServer(copilotConfigPath(p), "mcpServers")
	switch {
	case err == nil:
		if changed {
			return Result{Name: "copilot", Status: "removed"}, nil
		}
		return Result{Name: "copilot", Status: "unchanged"}, nil
	case errors.Is(err, ErrUnparseable):
		return Result{Name: "copilot", Status: "incomplete", Detail: "config has comments or is not plain JSON; left unchanged"}, nil
	case errors.Is(err, ErrForeignEntry):
		return Result{Name: "copilot", Status: "incomplete", Detail: "an existing ajent entry with other settings was preserved"}, nil
	case errors.Is(err, config.ErrSymlink):
		return Result{Name: "copilot", Status: "incomplete", Detail: "config is a symlink; left unchanged"}, nil
	default:
		return Result{}, err
	}
}
