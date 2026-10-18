// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"

	"github.com/ajent-social/ajent-client/internal/cli"
	"github.com/ajent-social/ajent-client/internal/gendocs"
	"github.com/ajent-social/ajent-client/internal/mcp"
)

// gendocsDefaultDir is the output directory runGendocs writes to when no
// argument is given.
const gendocsDefaultDir = "docs"

func init() {
	cli.Register(cli.Command{
		Name:    "gendocs",
		Hidden:  true,
		Summary: "Generate reference docs",
		Usage:   "ajent gendocs [DIR]",
		Run:     runGendocs,
	})
}

// runGendocs writes commands.md and mcp-tools.md into args[0] (default
// gendocsDefaultDir), generated from the CLI commands and MCP tools
// registered in this build. It writes with plain os.WriteFile at mode 0644,
// independent of env.ConfigDir, because the generated docs live in the
// repository, not the user's ajent config directory (docs/tasks/T7.3.md).
func runGendocs(_ context.Context, _ *cli.Env, args []string) error {
	dir := gendocsDefaultDir
	if len(args) > 0 && args[0] != "" {
		dir = args[0]
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}

	var commands, tools bytes.Buffer
	if err := gendocs.Commands(&commands, cli.Commands()); err != nil {
		return err
	}
	if err := gendocs.Tools(&tools, mcp.Tools()); err != nil {
		return err
	}

	// Mode 0644 is required by docs/tasks/T7.3.md: the generated reference
	// docs are committed, world-readable repository files, not private
	// config under env.ConfigDir.
	if err := os.WriteFile(filepath.Join(dir, "commands.md"), commands.Bytes(), 0o644); err != nil { //nolint:gosec // G306: docs/tasks/T7.3.md mandates mode 0644 for these committed, world-readable docs.
		return err
	}
	return os.WriteFile(filepath.Join(dir, "mcp-tools.md"), tools.Bytes(), 0o644) //nolint:gosec // G306: docs/tasks/T7.3.md mandates mode 0644 for these committed, world-readable docs.
}
