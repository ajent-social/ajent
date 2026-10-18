// SPDX-License-Identifier: Apache-2.0

package ajent

import (
	"encoding/json"
	"io/fs"
	"os"
	"strings"
	"testing"
)

func TestPluginsEmbedded(t *testing.T) {
	paths := []string{
		"plugins/ajent/.claude-plugin/plugin.json",
		"plugins/ajent/.codex-plugin/plugin.json",
		"plugins/ajent/hooks/hooks.json",
		"plugins/ajent/skills/network/SKILL.md",
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			if _, err := fs.ReadFile(Plugins, p); err != nil {
				t.Fatalf("fs.ReadFile(%q): %v", p, err)
			}
		})
	}
}

func loadSchema(t *testing.T) map[string][]string {
	t.Helper()
	raw, err := os.ReadFile("testdata/plugins/schema.json")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schema map[string][]string
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	return schema
}

func loadManifest(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := fs.ReadFile(Plugins, path)
	if err != nil {
		t.Fatalf("read manifest %q: %v", path, err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("unmarshal manifest %q: %v", path, err)
	}
	return manifest
}

func requireKeys(t *testing.T, name string, manifest map[string]any, keys []string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := manifest[key]; !ok {
			t.Errorf("%s: missing required key %q", name, key)
		}
	}
}

func TestManifestsHaveRequiredKeys(t *testing.T) {
	schema := loadSchema(t)

	claudePlugin := loadManifest(t, "plugins/ajent/.claude-plugin/plugin.json")
	requireKeys(t, "claude_plugin", claudePlugin, schema["claude_plugin"])

	codexPlugin := loadManifest(t, "plugins/ajent/.codex-plugin/plugin.json")
	requireKeys(t, "codex_plugin", codexPlugin, schema["codex_plugin"])

	raw, err := os.ReadFile(".claude-plugin/marketplace.json")
	if err != nil {
		t.Fatalf("read marketplace.json: %v", err)
	}
	var marketplace map[string]any
	if err := json.Unmarshal(raw, &marketplace); err != nil {
		t.Fatalf("unmarshal marketplace.json: %v", err)
	}
	requireKeys(t, "marketplace", marketplace, schema["marketplace"])

	plugins, ok := marketplace["plugins"].([]any)
	if !ok || len(plugins) == 0 {
		t.Fatalf("marketplace.json: plugins must be a non-empty array")
	}
	for i, p := range plugins {
		entry, ok := p.(map[string]any)
		if !ok {
			t.Fatalf("marketplace.json: plugins[%d] is not an object", i)
		}
		requireKeys(t, "marketplace_plugin", entry, schema["marketplace_plugin"])
	}
}

func TestVersionsAgree(t *testing.T) {
	claudePlugin := loadManifest(t, "plugins/ajent/.claude-plugin/plugin.json")
	codexPlugin := loadManifest(t, "plugins/ajent/.codex-plugin/plugin.json")

	raw, err := os.ReadFile(".claude-plugin/marketplace.json")
	if err != nil {
		t.Fatalf("read marketplace.json: %v", err)
	}
	var marketplace map[string]any
	if err := json.Unmarshal(raw, &marketplace); err != nil {
		t.Fatalf("unmarshal marketplace.json: %v", err)
	}
	plugins, ok := marketplace["plugins"].([]any)
	if !ok || len(plugins) == 0 {
		t.Fatalf("marketplace.json: plugins must be a non-empty array")
	}
	entry, ok := plugins[0].(map[string]any)
	if !ok {
		t.Fatalf("marketplace.json: plugins[0] is not an object")
	}

	claudeVersion, _ := claudePlugin["version"].(string)
	codexVersion, _ := codexPlugin["version"].(string)
	marketplaceVersion, _ := entry["version"].(string)

	if claudeVersion == "" || codexVersion == "" || marketplaceVersion == "" {
		t.Fatalf("empty version: claude=%q codex=%q marketplace=%q", claudeVersion, codexVersion, marketplaceVersion)
	}
	if claudeVersion != codexVersion || claudeVersion != marketplaceVersion {
		t.Errorf("version mismatch: claude=%q codex=%q marketplace=%q", claudeVersion, codexVersion, marketplaceVersion)
	}
}

func TestMarketplaceSourceExists(t *testing.T) {
	raw, err := os.ReadFile(".claude-plugin/marketplace.json")
	if err != nil {
		t.Fatalf("read marketplace.json: %v", err)
	}
	var marketplace map[string]any
	if err := json.Unmarshal(raw, &marketplace); err != nil {
		t.Fatalf("unmarshal marketplace.json: %v", err)
	}
	plugins, ok := marketplace["plugins"].([]any)
	if !ok || len(plugins) == 0 {
		t.Fatalf("marketplace.json: plugins must be a non-empty array")
	}
	entry, ok := plugins[0].(map[string]any)
	if !ok {
		t.Fatalf("marketplace.json: plugins[0] is not an object")
	}
	source, _ := entry["source"].(string)
	if source != "./plugins/ajent" {
		t.Fatalf("plugins[0].source = %q, want %q", source, "./plugins/ajent")
	}
	if _, err := os.Stat("plugins/ajent"); err != nil {
		t.Fatalf("os.Stat(%q): %v", source, err)
	}
}

func TestHookCommand(t *testing.T) {
	raw, err := fs.ReadFile(Plugins, "plugins/ajent/hooks/hooks.json")
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	var doc struct {
		Hooks struct {
			SessionStart []struct {
				Hooks []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
					Timeout int    `json:"timeout"`
				} `json:"hooks"`
			} `json:"SessionStart"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal hooks.json: %v", err)
	}
	if len(doc.Hooks.SessionStart) == 0 || len(doc.Hooks.SessionStart[0].Hooks) == 0 {
		t.Fatalf("hooks.json: SessionStart[0].hooks is empty")
	}
	hook := doc.Hooks.SessionStart[0].Hooks[0]
	if hook.Command != "ajent hook session-start" {
		t.Errorf("command = %q, want %q", hook.Command, "ajent hook session-start")
	}
	if hook.Timeout != 5 {
		t.Errorf("timeout = %d, want 5", hook.Timeout)
	}
}

func TestSkillIsPublicSafe(t *testing.T) {
	raw, err := fs.ReadFile(Plugins, "plugins/ajent/skills/network/SKILL.md")
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	content := string(raw)

	for i, r := range content {
		if r > 127 {
			t.Fatalf("SKILL.md contains a non-ASCII rune %q at byte offset %d", r, i)
		}
	}

	const wantPrefix = "---\nname: network\n"
	if !strings.HasPrefix(strings.ReplaceAll(content, "\r\n", "\n"), wantPrefix) {
		t.Errorf("SKILL.md does not start with %q", wantPrefix)
	}

	const marker = "Managed by ajent. ajent integrations sync overwrites local edits."
	if !strings.Contains(content, marker) {
		t.Errorf("SKILL.md is missing the managed marker line")
	}

	for _, banned := range []string{"python3", "client.py", "0.5."} {
		if strings.Contains(content, banned) {
			t.Errorf("SKILL.md contains banned string %q", banned)
		}
	}
}
