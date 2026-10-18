// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func init() {
	Register(claudeWriter{})
}

// claudeWriter configures the Ajent MCP server, at user scope, through the
// Claude Code CLI, and registers this repository as a Claude Code plugin
// marketplace (ADR 007). It never edits ~/.claude.json directly; every
// write goes through the claude CLI, which is the only thing that can keep
// that file's own invariants intact.
type claudeWriter struct{}

// Target identifies this writer to the registry.
func (claudeWriter) Target() Target {
	return Target{Name: "claude", Profile: "claude-code"}
}

// Detect reports whether the claude CLI is on PATH.
func (claudeWriter) Detect(_ Params) bool {
	_, err := LookPath("claude")
	return err == nil
}

// Paths returns the files the claude CLI edits, so Sync can back them up
// before Apply or Remove runs.
func (claudeWriter) Paths(p Params) []string {
	return []string{
		filepath.Join(p.Home, ".claude.json"),
		filepath.Join(p.Home, ".claude", "settings.json"),
	}
}

// claudeWant builds the entry Apply wants to see registered: the canonical
// MCP entry plus the "type": "stdio" key Claude Code's schema expects.
func claudeWant(p Params) map[string]any {
	want := Entry(p, "claude-code")
	want["type"] = "stdio"
	return want
}

// claudeCurrent reads the ajent entry from <Home>/.claude.json's
// mcpServers.ajent. It returns nil, false on any read or parse error, or
// when no such entry exists; the file is never written here, only
// inspected, because all writes go through the claude CLI.
func claudeCurrent(p Params) (map[string]any, bool) {
	path := filepath.Join(p.Home, ".claude.json")
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is built from p.Home, the caller-supplied home directory (docs/interfaces.md Params); not attacker-controlled input.
	if err != nil {
		return nil, false
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, false
	}
	servers, ok := doc["mcpServers"].(map[string]any)
	if !ok {
		return nil, false
	}
	entry, ok := servers["ajent"].(map[string]any)
	if !ok {
		return nil, false
	}
	return entry, true
}

// claudeNormalize prepares an entry for a SameEntry comparison: a "type"
// key equal to "stdio" is dropped, and a missing env is treated the same
// as an empty env object.
func claudeNormalize(entry map[string]any) map[string]any {
	out := make(map[string]any, len(entry))
	for k, v := range entry {
		out[k] = v
	}
	if s, ok := out["type"].(string); ok && s == "stdio" {
		delete(out, "type")
	}
	if env, ok := out["env"].(map[string]any); ok && len(env) == 0 {
		delete(out, "env")
	}
	return out
}

// claudeIncompleteDetail is the Detail reported whenever a claude CLI
// mutation fails: the prior settings are left in place, and a copyable
// example is available for manual setup.
func claudeIncompleteDetail(p Params) string {
	return "setup incomplete; existing settings preserved, see " + filepath.Join(p.ConfigDir, "mcp-example.json")
}

// Apply registers the ajent MCP server through `claude mcp add-json` and
// registers this repository's plugin marketplace. Reruns change nothing.
// An outdated ajent-owned entry (including a Python-era one) is replaced.
// A foreign ajent entry is left alone and Apply reports incomplete.
func (claudeWriter) Apply(ctx context.Context, p Params) (Result, error) {
	want := claudeWant(p)
	existing, ok := claudeCurrent(p)

	result := Result{Name: "claude"}
	switch {
	case ok && SameEntry(claudeNormalize(existing), claudeNormalize(want)):
		result.Status = "unchanged"
	case ok && !Owned(existing):
		return Result{
			Name:   "claude",
			Status: "incomplete",
			Detail: "an existing ajent entry with other settings was preserved",
		}, nil
	default:
		if ok && Owned(existing) {
			if _, err := Run(ctx, "claude", "mcp", "remove", "--scope", "user", "ajent"); err != nil {
				return Result{Name: "claude", Status: "incomplete", Detail: claudeIncompleteDetail(p)}, nil
			}
		}
		b, _ := json.Marshal(want)
		if _, err := Run(ctx, "claude", "mcp", "add-json", "--scope", "user", "ajent", string(b)); err != nil {
			return Result{Name: "claude", Status: "incomplete", Detail: claudeIncompleteDetail(p)}, nil
		}
		result.Status = "configured"
	}

	claudeApplyMarketplace(ctx, &result)
	return result, nil
}

// claudeApplyMarketplace registers this repository as a Claude Code plugin
// marketplace when it is not already listed. It never changes result's
// Status: only Detail gains a trailing note.
func claudeApplyMarketplace(ctx context.Context, result *Result) {
	out, err := Run(ctx, "claude", "plugin", "marketplace", "list")
	if err == nil && !claudeMarketplaceListed(out) {
		_, err = Run(ctx, "claude", "plugin", "marketplace", "add", "ajent-social/ajent")
	}
	if err != nil {
		result.Detail += "; plugin marketplace not registered"
		return
	}
	result.Detail += "; enable the plugin with: claude plugin install ajent@ajent"
}

// claudeMarketplaceListed reports whether any line of a
// `claude plugin marketplace list` output names ajent.
func claudeMarketplaceListed(out []byte) bool {
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "ajent") {
			return true
		}
	}
	return false
}

// Remove unregisters the ajent MCP server through `claude mcp remove` when
// the current entry is ajent-owned, and best-effort removes the plugin
// marketplace registration. A foreign entry, or no entry at all, is left
// unchanged.
func (claudeWriter) Remove(ctx context.Context, p Params) (Result, error) {
	result := Result{Name: "claude", Status: "unchanged"}

	if existing, ok := claudeCurrent(p); ok {
		if Owned(existing) {
			_, _ = Run(ctx, "claude", "mcp", "remove", "--scope", "user", "ajent")
			result.Status = "removed"
		} else {
			result.Detail = "foreign ajent entry preserved"
		}
	}

	_, _ = Run(ctx, "claude", "plugin", "marketplace", "remove", "ajent")
	return result, nil
}
