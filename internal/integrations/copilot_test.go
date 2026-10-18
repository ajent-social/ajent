// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// copilotTestParams returns Params for a fake home with the default config
// dir, the binary path used by the golden fixtures under
// testdata/integrations/copilot, and no credential file override.
func copilotTestParams(home string) Params {
	return Params{
		Home:      home,
		ConfigDir: filepath.Join(home, ".aj"),
		Binary:    "/opt/ajent/bin/ajent",
		GOOS:      "linux",
		Getenv:    func(string) string { return "" },
	}
}

func copilotFixture(elem ...string) string {
	return filepath.Join(append([]string{"..", "..", "testdata", "integrations", "copilot"}, elem...)...)
}

// copyTree copies every file under src into dst, preserving relative paths
// and creating directories (including otherwise-empty ones) as needed.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()

	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path) //nolint:gosec // G304: path walks a fixed testdata tree under the repo.
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatalf("copyTree(%s -> %s): %v", src, dst, err)
	}
}

// compareTree asserts that gotDir holds exactly the files in wantDir, with
// identical bytes.
func compareTree(t *testing.T, wantDir, gotDir string) {
	t.Helper()

	want := readTreeFiles(t, wantDir)
	got := readTreeFiles(t, gotDir)

	for rel, wantData := range want {
		gotData, ok := got[rel]
		if !ok {
			t.Errorf("missing file %s", rel)
			continue
		}
		if !bytes.Equal(normalizeFixtureNewlines(gotData), normalizeFixtureNewlines(wantData)) {
			t.Errorf("%s:\n got:\n%s\nwant:\n%s", rel, gotData, wantData)
		}
	}
	for rel := range got {
		if _, ok := want[rel]; !ok {
			t.Errorf("unexpected file %s", rel)
		}
	}
}

func readTreeFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()

	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path) //nolint:gosec // G304: path walks a fixed testdata or t.TempDir() tree.
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return files
}

func TestCopilotGolden(t *testing.T) {
	cases := []struct {
		name       string
		wantStatus string
	}{
		{"fresh-dir", "configured"},
		{"foreign-servers", "configured"},
		{"python-entry", "configured"},
		{"current", "unchanged"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			copyTree(t, copilotFixture(tc.name, "before", "home"), home)

			p := copilotTestParams(home)
			result, err := (copilotWriter{}).Apply(context.Background(), p)
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if result.Status != tc.wantStatus {
				t.Errorf("Apply() status = %q, want %q", result.Status, tc.wantStatus)
			}

			compareTree(t, copilotFixture(tc.name, "after", "home"), home)
		})
	}
}

func TestCopilotSecondRunUnchanged(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".copilot"), 0o700); err != nil {
		t.Fatalf("mkdir .copilot: %v", err)
	}

	p := copilotTestParams(home)
	w := copilotWriter{}
	ctx := context.Background()

	first, err := w.Apply(ctx, p)
	if err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}
	if first.Status != "configured" {
		t.Fatalf("first Apply() status = %q, want configured", first.Status)
	}
	before, err := os.ReadFile(copilotConfigPath(p))
	if err != nil {
		t.Fatalf("read after first apply: %v", err)
	}

	second, err := w.Apply(ctx, p)
	if err != nil {
		t.Fatalf("second Apply() error = %v", err)
	}
	if second.Status != "unchanged" {
		t.Fatalf("second Apply() status = %q, want unchanged", second.Status)
	}
	after, err := os.ReadFile(copilotConfigPath(p))
	if err != nil {
		t.Fatalf("read after second apply: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("second run bytes differ:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestCopilotNotDetected(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}

	p := copilotTestParams(home)
	result, err := (copilotWriter{}).Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Status != "skipped" {
		t.Fatalf("Apply() status = %q, want skipped", result.Status)
	}

	configDir := filepath.Join(home, ".copilot")
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Fatalf("Apply() created %s", configDir)
	}
}

func TestCopilotJSONCPreserved(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	configDir := filepath.Join(home, ".copilot")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("mkdir .copilot: %v", err)
	}

	data := []byte("{\n  // comment\n  \"mcpServers\": {}\n}\n")
	target := filepath.Join(configDir, "mcp-config.json")
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	p := copilotTestParams(home)
	result, err := (copilotWriter{}).Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Status != "incomplete" {
		t.Fatalf("Apply() status = %q, want incomplete", result.Status)
	}
	const wantDetail = "config has comments or is not plain JSON; left unchanged"
	if result.Detail != wantDetail {
		t.Fatalf("Apply() detail = %q, want %q", result.Detail, wantDetail)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("file mutated:\n got:\n%s\nwant:\n%s", got, data)
	}
}

func TestCopilotForeignEntryPreserved(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	configDir := filepath.Join(home, ".copilot")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("mkdir .copilot: %v", err)
	}

	data := []byte("{\n  \"mcpServers\": {\n    \"ajent\": {\n      \"command\": \"npx\",\n      \"args\": [\n        \"ajent-mcp\"\n      ]\n    }\n  }\n}\n")
	target := filepath.Join(configDir, "mcp-config.json")
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	p := copilotTestParams(home)
	result, err := (copilotWriter{}).Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Status != "incomplete" {
		t.Fatalf("Apply() status = %q, want incomplete", result.Status)
	}
	const wantDetail = "an existing ajent entry with other settings was preserved"
	if result.Detail != wantDetail {
		t.Fatalf("Apply() detail = %q, want %q", result.Detail, wantDetail)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("file mutated:\n got:\n%s\nwant:\n%s", got, data)
	}
}

func TestCopilotRemove(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".copilot"), 0o700); err != nil {
		t.Fatalf("mkdir .copilot: %v", err)
	}

	p := copilotTestParams(home)
	w := copilotWriter{}
	ctx := context.Background()

	target := copilotConfigPath(p)
	seed := []byte("{\n  \"mcpServers\": {\n    \"other\": {\n      \"command\": \"npx\",\n      \"args\": [\n        \"other-server\"\n      ]\n    }\n  }\n}\n")
	if err := os.WriteFile(target, seed, 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if _, err := w.Apply(ctx, p); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	result, err := w.Remove(ctx, p)
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if result.Status != "removed" {
		t.Fatalf("Remove() status = %q, want removed", result.Status)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	section, ok := doc["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers section missing or invalid: %#v", doc["mcpServers"])
	}
	if _, present := section["ajent"]; present {
		t.Fatal("ajent entry still present after Remove")
	}
	if _, present := section["other"]; !present {
		t.Fatal("Remove deleted the foreign \"other\" entry")
	}
}

func TestCopilotCustomConfigDirEnv(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".copilot"), 0o700); err != nil {
		t.Fatalf("mkdir .copilot: %v", err)
	}

	p := copilotTestParams(home)
	p.ConfigDir = filepath.Join(dir, "custom-config")

	if _, err := (copilotWriter{}).Apply(context.Background(), p); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	data, err := os.ReadFile(copilotConfigPath(p))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var doc struct {
		MCPServers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	entry, ok := doc.MCPServers["ajent"]
	if !ok {
		t.Fatal("ajent entry missing")
	}
	if entry.Env["AJENT_CONFIG_DIR"] != p.ConfigDir {
		t.Fatalf("env AJENT_CONFIG_DIR = %q, want %q", entry.Env["AJENT_CONFIG_DIR"], p.ConfigDir)
	}
}

func TestCopilotEntryShape(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".copilot"), 0o700); err != nil {
		t.Fatalf("mkdir .copilot: %v", err)
	}

	p := copilotTestParams(home)
	if _, err := (copilotWriter{}).Apply(context.Background(), p); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	data, err := os.ReadFile(copilotConfigPath(p))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var doc struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	entry, ok := doc.MCPServers["ajent"]
	if !ok {
		t.Fatal("ajent entry missing")
	}
	if entry["type"] != "local" {
		t.Fatalf("type = %v, want %q", entry["type"], "local")
	}
	tools, ok := entry["tools"].([]any)
	if !ok || len(tools) != 1 || tools[0] != "*" {
		t.Fatalf("tools = %v, want [\"*\"]", entry["tools"])
	}
}
