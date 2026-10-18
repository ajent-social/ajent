// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// defaultGeminiParams returns Params for a fake home with the default
// config dir and no credential file, so Entry(p, "gemini-cli") has no "env"
// key.
func defaultGeminiParams(home string) Params {
	return Params{
		Home:      home,
		ConfigDir: filepath.Join(home, ".aj"),
		Binary:    "/opt/ajent/bin/ajent",
	}
}

// geminiFixture resolves a path under testdata/integrations/gemini.
func geminiFixture(elem ...string) string {
	return filepath.Join(append([]string{"..", "..", "testdata", "integrations", "gemini"}, elem...)...)
}

// copyTree copies every regular file under src into dst, preserving
// relative paths. A missing src is not an error: it represents a fixture
// with nothing to seed (for example, a config directory that exists but
// holds no settings file yet).
func geminiCopyTree(t *testing.T, src, dst string) {
	t.Helper()

	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("stat %s: %v", src, err)
	}
	if !info.IsDir() {
		t.Fatalf("fixture %s is not a directory", src)
	}

	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		data, readErr := os.ReadFile(path) //nolint:gosec // G304: path is produced by walking a fixed testdata fixture tree.
		if readErr != nil {
			return readErr
		}
		target := filepath.Join(dst, rel)
		if mkErr := os.MkdirAll(filepath.Dir(target), 0o700); mkErr != nil {
			return mkErr
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatalf("copy fixture tree %s: %v", src, err)
	}
}

// treeFiles reads every regular file under root into a map keyed by its
// slash-separated relative path. A missing root yields an empty map.
func treeFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()

	out := map[string][]byte{}
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return out
		}
		t.Fatalf("stat %s: %v", root, err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", root)
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		data, readErr := os.ReadFile(path) //nolint:gosec // G304: path is produced by walking a temp directory or a fixed testdata fixture tree.
		if readErr != nil {
			return readErr
		}
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

// assertTreesEqual reports every file that is missing, unexpected or
// byte-different between got and want.
func assertTreesEqual(t *testing.T, got, want map[string][]byte) {
	t.Helper()

	names := make(map[string]bool, len(got)+len(want))
	for name := range got {
		names[name] = true
	}
	for name := range want {
		names[name] = true
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)

	for _, name := range sorted {
		g, gotOK := got[name]
		w, wantOK := want[name]
		switch {
		case gotOK && !wantOK:
			t.Errorf("unexpected file %s:\n%s", name, g)
		case !gotOK && wantOK:
			t.Errorf("missing file %s:\nwant:\n%s", name, w)
		case !bytes.Equal(normalizeFixtureNewlines(g), normalizeFixtureNewlines(w)):
			t.Errorf("%s differs:\n got:\n%s\nwant:\n%s", name, g, w)
		}
	}
}

func TestGeminiGolden(t *testing.T) {
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
			home := filepath.Join(t.TempDir(), "home")
			if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o700); err != nil {
				t.Fatalf("mkdir .gemini: %v", err)
			}
			geminiCopyTree(t, geminiFixture(tc.name, "before"), home)

			p := defaultGeminiParams(home)
			result, err := geminiWriter{}.Apply(context.Background(), p)
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if result.Status != tc.wantStatus {
				t.Errorf("Apply() status = %q, want %q", result.Status, tc.wantStatus)
			}

			got := treeFiles(t, home)
			want := treeFiles(t, geminiFixture(tc.name, "after"))
			assertTreesEqual(t, got, want)
		})
	}
}

func TestGeminiSecondRunUnchanged(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o700); err != nil {
		t.Fatalf("mkdir .gemini: %v", err)
	}
	p := defaultGeminiParams(home)
	w := geminiWriter{}

	first, err := w.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}
	if first.Status != "configured" {
		t.Fatalf("first Apply() status = %q, want configured", first.Status)
	}
	firstBytes, err := os.ReadFile(w.configPath(p))
	if err != nil {
		t.Fatalf("read after first apply: %v", err)
	}

	second, err := w.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("second Apply() error = %v", err)
	}
	if second.Status != "unchanged" {
		t.Fatalf("second Apply() status = %q, want unchanged", second.Status)
	}
	secondBytes, err := os.ReadFile(w.configPath(p))
	if err != nil {
		t.Fatalf("read after second apply: %v", err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("second run changed bytes:\nfirst:\n%s\nsecond:\n%s", firstBytes, secondBytes)
	}
}

func TestGeminiNotDetected(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	p := defaultGeminiParams(home)

	result, err := geminiWriter{}.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	want := Result{Name: "gemini", Status: "skipped"}
	if result != want {
		t.Fatalf("Apply() = %+v, want %+v", result, want)
	}

	if _, err := os.Stat(filepath.Join(home, ".gemini")); !os.IsNotExist(err) {
		t.Fatalf("Apply() created .gemini for an undetected install: err = %v", err)
	}
}

func TestGeminiJSONCPreserved(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o700); err != nil {
		t.Fatalf("mkdir .gemini: %v", err)
	}
	target := filepath.Join(home, ".gemini", "settings.json")
	data := []byte("{\n  // ajent config lives under mcpServers.ajent\n  \"mcpServers\": {}\n}\n")
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatalf("seed settings.json: %v", err)
	}

	p := defaultGeminiParams(home)
	result, err := geminiWriter{}.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	want := Result{Name: "gemini", Status: "incomplete", Detail: "config has comments or is not plain JSON; left unchanged"}
	if result != want {
		t.Fatalf("Apply() = %+v, want %+v", result, want)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("file mutated:\n got:\n%s\nwant:\n%s", got, data)
	}
}

func TestGeminiForeignEntryPreserved(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o700); err != nil {
		t.Fatalf("mkdir .gemini: %v", err)
	}
	target := filepath.Join(home, ".gemini", "settings.json")
	data := []byte("{\n  \"mcpServers\": {\n    \"ajent\": {\n      \"command\": \"npx\",\n      \"args\": [\n        \"-y\",\n        \"some-other-mcp-server\"\n      ]\n    }\n  }\n}\n")
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatalf("seed settings.json: %v", err)
	}

	p := defaultGeminiParams(home)
	result, err := geminiWriter{}.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	want := Result{Name: "gemini", Status: "incomplete", Detail: "an existing ajent entry with other settings was preserved"}
	if result != want {
		t.Fatalf("Apply() = %+v, want %+v", result, want)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("file mutated:\n got:\n%s\nwant:\n%s", got, data)
	}
}

func TestGeminiRemove(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o700); err != nil {
		t.Fatalf("mkdir .gemini: %v", err)
	}
	target := filepath.Join(home, ".gemini", "settings.json")
	before := []byte("{\n  \"mcpServers\": {\n    \"other\": {\n      \"command\": \"npx\",\n      \"args\": [\n        \"other-server\"\n      ]\n    }\n  }\n}\n")
	if err := os.WriteFile(target, before, 0o600); err != nil {
		t.Fatalf("seed settings.json: %v", err)
	}

	p := defaultGeminiParams(home)
	w := geminiWriter{}

	applyResult, err := w.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if applyResult.Status != "configured" {
		t.Fatalf("Apply() status = %q, want configured", applyResult.Status)
	}

	removeResult, err := w.Remove(context.Background(), p)
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if removeResult.Status != "removed" {
		t.Fatalf("Remove() status = %q, want removed", removeResult.Status)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if !bytes.Equal(got, before) {
		t.Fatalf("Remove() left the file different from its pre-configure state:\n got:\n%s\nwant:\n%s", got, before)
	}

	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
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
		t.Fatal("other entry removed by Remove")
	}
}

func TestGeminiCustomConfigDirEnv(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o700); err != nil {
		t.Fatalf("mkdir .gemini: %v", err)
	}
	customDir := filepath.Join(dir, "custom-config")
	p := Params{Home: home, Binary: "/opt/ajent/bin/ajent", ConfigDir: customDir}

	result, err := geminiWriter{}.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Status != "configured" {
		t.Fatalf("Apply() status = %q, want configured", result.Status)
	}

	data, err := os.ReadFile(filepath.Join(home, ".gemini", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	servers, ok := doc["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers section missing or invalid: %#v", doc["mcpServers"])
	}
	ajent, ok := servers["ajent"].(map[string]any)
	if !ok {
		t.Fatalf("ajent entry missing or invalid: %#v", servers["ajent"])
	}
	env, ok := ajent["env"].(map[string]any)
	if !ok {
		t.Fatalf("ajent entry has no env block: %#v", ajent)
	}
	if got := env["AJENT_CONFIG_DIR"]; got != customDir {
		t.Fatalf("env.AJENT_CONFIG_DIR = %v, want %v", got, customDir)
	}
	if _, present := env["AJENT_CREDENTIAL_FILE"]; present {
		t.Fatal("env.AJENT_CREDENTIAL_FILE present without a credential file override")
	}
}

func TestGeminiOtherSettingsKept(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o700); err != nil {
		t.Fatalf("mkdir .gemini: %v", err)
	}
	target := filepath.Join(home, ".gemini", "settings.json")
	before := "{\n" +
		"  \"theme\": \"dark\",\n" +
		"  \"selectedAuthType\": \"oauth-personal\",\n" +
		"  \"ui\": {\n" +
		"    \"hideBanner\": true,\n" +
		"    \"hideTips\": false\n" +
		"  },\n" +
		"  \"mcpServers\": {}\n" +
		"}\n"
	if err := os.WriteFile(target, []byte(before), 0o600); err != nil {
		t.Fatalf("seed settings.json: %v", err)
	}

	p := defaultGeminiParams(home)
	result, err := geminiWriter{}.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Status != "configured" {
		t.Fatalf("Apply() status = %q, want configured", result.Status)
	}

	want := "{\n" +
		"  \"theme\": \"dark\",\n" +
		"  \"selectedAuthType\": \"oauth-personal\",\n" +
		"  \"ui\": {\n" +
		"    \"hideBanner\": true,\n" +
		"    \"hideTips\": false\n" +
		"  },\n" +
		"  \"mcpServers\": {\n" +
		"    \"ajent\": {\n" +
		"      \"args\": [\n" +
		"        \"mcp\",\n" +
		"        \"--profile\",\n" +
		"        \"gemini-cli\",\n" +
		"        \"--project-profile\"\n" +
		"      ],\n" +
		"      \"command\": \"/opt/ajent/bin/ajent\"\n" +
		"    }\n" +
		"  }\n" +
		"}\n"

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != want {
		t.Fatalf("other settings not preserved byte for byte:\n got:\n%s\nwant:\n%s", got, want)
	}
}
