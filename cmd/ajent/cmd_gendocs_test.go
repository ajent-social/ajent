// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ajent-social/ajent/internal/cli"
)

// gendocsCommittedDocsDir is where the committed, generated reference docs
// live relative to this package.
const gendocsCommittedDocsDir = "../../docs"

func normalizeGeneratedNewlines(data []byte) []byte {
	return []byte(strings.ReplaceAll(string(data), "\r\n", "\n"))
}

// TestGendocsUpToDate proves docs/commands.md and docs/mcp-tools.md are
// byte-for-byte what runGendocs currently generates from the CLI commands
// and MCP tools registered in this build. A mismatch means some task
// registered a command or tool without running "make docs" (docs/tasks/T7.3.md).
func TestGendocsUpToDate(t *testing.T) {
	dir := t.TempDir()

	if err := runGendocs(context.Background(), &cli.Env{}, []string{dir}); err != nil {
		t.Fatalf("runGendocs: %v", err)
	}

	for _, name := range []string{"commands.md", "mcp-tools.md"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("reading generated %s: %v", name, err)
		}
		want, err := os.ReadFile(filepath.Join(gendocsCommittedDocsDir, name))
		if err != nil {
			t.Fatalf("reading committed docs/%s: %v", name, err)
		}
		if !bytes.Equal(normalizeGeneratedNewlines(got), normalizeGeneratedNewlines(want)) {
			t.Fatalf("docs are stale; run make docs")
		}
	}
}

// TestGendocsDefaultDir proves runGendocs writes into gendocsDefaultDir
// ("docs") when no directory argument is given.
func TestGendocsDefaultDir(t *testing.T) {
	dir := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatalf("restoring wd: %v", err)
		}
	})
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	if err := runGendocs(context.Background(), &cli.Env{}, nil); err != nil {
		t.Fatalf("runGendocs: %v", err)
	}

	for _, name := range []string{"commands.md", "mcp-tools.md"} {
		if _, err := os.Stat(filepath.Join(dir, gendocsDefaultDir, name)); err != nil {
			t.Errorf("expected %s under %s: %v", name, gendocsDefaultDir, err)
		}
	}
}

// TestGendocsRegisteredHidden proves the gendocs command is registered but
// excluded from the command listing, so it never appears in "ajent help"
// or the generated commands.md.
func TestGendocsRegisteredHidden(t *testing.T) {
	for _, c := range cli.Commands() {
		if c.Name == "gendocs" {
			t.Fatalf("gendocs command appears in cli.Commands(); want it Hidden and excluded")
		}
	}
}
