// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
)

func init() {
	Register(codexWriter{})
}

// codexWriter configures the ajent MCP server for the Codex CLI through
// `codex mcp add`/`codex mcp remove` (ADR 007). It never parses or writes
// ~/.codex/config.toml directly.
type codexWriter struct{}

// Target identifies this writer as "codex", profile "codex".
func (codexWriter) Target() Target {
	return Target{Name: "codex", Profile: "codex"}
}

// Detect reports whether the codex CLI is on PATH.
func (codexWriter) Detect(Params) bool {
	_, err := LookPath("codex")
	return err == nil
}

// Paths returns the config file codex owns, for backups only: this writer
// never reads or writes it directly.
func (codexWriter) Paths(p Params) []string {
	return []string{filepath.Join(p.Home, ".codex", "config.toml")}
}

// Apply registers the ajent MCP server with codex. A matching entry is left
// alone. A foreign (non-ajent-owned) entry is preserved and reported
// incomplete. An owned entry that differs (including a Python-era
// client.py install) is removed and re-added.
func (codexWriter) Apply(ctx context.Context, p Params) (Result, error) {
	const name = "codex"
	want := Entry(p, "codex")

	existing, present := codexCurrent(ctx)
	if present {
		if SameEntry(existing, want) {
			return Result{Name: name, Status: "unchanged"}, nil
		}
		if !Owned(existing) {
			return Result{
				Name:   name,
				Status: "incomplete",
				Detail: "an existing ajent entry with other settings was preserved",
			}, nil
		}
		if _, err := Run(ctx, "codex", "mcp", "remove", "ajent"); err != nil {
			return Result{Name: name, Status: "incomplete", Detail: codexIncompleteDetail(p)}, nil
		}
	}

	if _, err := Run(ctx, "codex", codexAddArgv(p)...); err != nil {
		return Result{Name: name, Status: "incomplete", Detail: codexIncompleteDetail(p)}, nil
	}

	return Result{Name: name, Status: "configured"}, nil
}

// Remove unregisters an ajent-owned entry. A foreign entry is left in
// place. An absent entry is a no-op.
func (codexWriter) Remove(ctx context.Context, p Params) (Result, error) {
	const name = "codex"

	existing, present := codexCurrent(ctx)
	if !present {
		return Result{Name: name, Status: "unchanged"}, nil
	}
	if !Owned(existing) {
		return Result{Name: name, Status: "unchanged", Detail: "foreign ajent entry preserved"}, nil
	}

	if _, err := Run(ctx, "codex", "mcp", "remove", "ajent"); err != nil {
		return Result{Name: name, Status: "incomplete", Detail: codexIncompleteDetail(p)}, nil
	}

	return Result{Name: name, Status: "removed"}, nil
}

// codexIncompleteDetail builds the shared "setup incomplete" detail. It
// never includes Run output: vendor CLI output can carry other MCP
// secrets.
func codexIncompleteDetail(p Params) string {
	return fmt.Sprintf("setup incomplete; existing settings preserved, see %s", filepath.Join(p.ConfigDir, "mcp-example.json"))
}

// codexCurrent runs `codex mcp get ajent --json` and reports the existing
// ajent entry, if any. A Run error means the entry is absent. Output that
// does not parse as JSON, or that parses but has no object with a string
// "command" field anywhere in it, counts as a present entry with an
// unknown command, which Owned always reports as foreign, so it is
// preserved rather than replaced.
func codexCurrent(ctx context.Context) (entry map[string]any, present bool) {
	out, err := Run(ctx, "codex", "mcp", "get", "ajent", "--json")
	if err != nil {
		return nil, false
	}

	var decoded any
	if err := json.Unmarshal(out, &decoded); err != nil {
		return map[string]any{"command": "<unknown>"}, true
	}

	if found, ok := codexFindEntry(decoded); ok {
		return found, true
	}
	return map[string]any{"command": "<unknown>"}, true
}

// codexFindEntry searches v depth-first for the first object with a string
// "command" field and returns a normalized entry: "command", "args" (the
// []any found next to it, or an empty slice) and "env" (the object found
// next to it, when present). Object keys are visited in sorted order so
// the search is deterministic.
func codexFindEntry(v any) (map[string]any, bool) {
	switch val := v.(type) {
	case map[string]any:
		if command, ok := val["command"].(string); ok {
			entry := map[string]any{"command": command}
			if args, ok := val["args"].([]any); ok {
				entry["args"] = args
			} else {
				entry["args"] = []any{}
			}
			if env, ok := val["env"].(map[string]any); ok {
				entry["env"] = env
			}
			return entry, true
		}

		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if entry, ok := codexFindEntry(val[k]); ok {
				return entry, true
			}
		}
	case []any:
		for _, item := range val {
			if entry, ok := codexFindEntry(item); ok {
				return entry, true
			}
		}
	}
	return nil, false
}

// codexAddArgv builds the argv for `codex mcp add`: "mcp", "add", then one
// "--env KEY=VALUE" pair per env key in sorted order, then "ajent", "--",
// the stable binary and its MCP launch arguments.
func codexAddArgv(p Params) []string {
	argv := []string{"mcp", "add"}

	env := EntryEnv(p)
	if len(env) > 0 {
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			argv = append(argv, "--env", k+"="+env[k])
		}
	}

	argv = append(argv, "ajent", "--", p.Binary)
	argv = append(argv, MCPArgs("codex")...)
	return argv
}
