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

// cursorTestParams returns Params for a fake home with the default config
// dir and no credential file, matching the golden fixtures' assumptions
// (docs/tasks/T4.5.md step 6): default config dir and
// Binary "/opt/ajent/bin/ajent".
func cursorTestParams(home string) Params {
	return Params{
		Home:      home,
		ConfigDir: filepath.Join(home, ".aj"),
		Binary:    "/opt/ajent/bin/ajent",
	}
}

func cursorFixture(elem ...string) string {
	return filepath.Join(append([]string{"..", "..", "testdata", "integrations", "cursor"}, elem...)...)
}

// copyTree copies every regular file under src into the same relative
// path under dst, creating directories as needed. A missing src is not an
// error: the fresh-dir fixture has nothing to seed.
func cursorCopyTree(t *testing.T, src, dst string) {
	t.Helper()
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return
	}
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
		data, err := os.ReadFile(path) //nolint:gosec // G304: path is produced by WalkDir over a fixture tree under testdata.
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatalf("cursorCopyTree(%s, %s): %v", src, dst, err)
	}
}

// collectRelFiles returns every regular file under dir as a path relative
// to dir.
func collectRelFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
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
		files = append(files, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("collectRelFiles(%s): %v", dir, err)
	}
	return files
}

func TestCursorGolden(t *testing.T) {
	cases := []string{"fresh-dir", "foreign-servers", "python-entry", "current"}

	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			cursorCopyTree(t, cursorFixture(name, "before"), home)
			// The config directory must exist for Detect even when
			// before/ has no mcp.json to seed (the fresh-dir case).
			if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o700); err != nil {
				t.Fatalf("mkdir .cursor: %v", err)
			}

			p := cursorTestParams(home)
			w := cursorWriter{}
			if _, err := w.Apply(context.Background(), p); err != nil {
				t.Fatalf("Apply() error = %v", err)
			}

			afterDir := cursorFixture(name, "after")
			for _, rel := range collectRelFiles(t, afterDir) {
				gotPath := filepath.Join(home, rel)
				got, err := os.ReadFile(gotPath) //nolint:gosec // G304: gotPath is built from a fixture-listed relative path under t.TempDir().
				if err != nil {
					t.Fatalf("read %s: %v", gotPath, err)
				}
				want, err := os.ReadFile(filepath.Join(afterDir, rel)) //nolint:gosec // G304: path is under testdata, listed by collectRelFiles.
				if err != nil {
					t.Fatalf("read fixture %s: %v", rel, err)
				}
				if !bytes.Equal(normalizeFixtureNewlines(got), normalizeFixtureNewlines(want)) {
					t.Errorf("%s:\n got:\n%s\nwant:\n%s", rel, got, want)
				}
			}
		})
	}
}

func TestCursorSecondRunUnchanged(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o700); err != nil {
		t.Fatalf("mkdir .cursor: %v", err)
	}
	p := cursorTestParams(home)
	w := cursorWriter{}

	first, err := w.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}
	if first.Status != "configured" {
		t.Fatalf("first Apply() status = %q, want configured", first.Status)
	}
	firstBytes, err := os.ReadFile(w.configPath(p))
	if err != nil {
		t.Fatalf("read after first Apply: %v", err)
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
		t.Fatalf("read after second Apply: %v", err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("second run bytes differ:\nfirst:\n%s\nsecond:\n%s", firstBytes, secondBytes)
	}
}

func TestCursorNotDetected(t *testing.T) {
	home := t.TempDir()
	p := cursorTestParams(home)
	w := cursorWriter{}

	result, err := w.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Status != "skipped" {
		t.Fatalf("Apply() status = %q, want skipped", result.Status)
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor")); !os.IsNotExist(err) {
		t.Fatalf("Apply() created %s (err = %v)", filepath.Join(home, ".cursor"), err)
	}
}

func TestCursorJSONCPreserved(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o700); err != nil {
		t.Fatalf("mkdir .cursor: %v", err)
	}
	p := cursorTestParams(home)
	w := cursorWriter{}
	data := []byte("{\n  // ajent config lives under mcpServers.ajent\n  \"mcpServers\": {}\n}\n")
	if err := os.WriteFile(w.configPath(p), data, 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	result, err := w.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Status != "incomplete" {
		t.Fatalf("Apply() status = %q, want incomplete", result.Status)
	}
	wantDetail := "config has comments or is not plain JSON; left unchanged"
	if result.Detail != wantDetail {
		t.Fatalf("Apply() detail = %q, want %q", result.Detail, wantDetail)
	}

	got, err := os.ReadFile(w.configPath(p))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("file mutated:\n got:\n%s\nwant:\n%s", got, data)
	}
}

func TestCursorForeignEntryPreserved(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o700); err != nil {
		t.Fatalf("mkdir .cursor: %v", err)
	}
	p := cursorTestParams(home)
	w := cursorWriter{}
	data := []byte("{\n  \"mcpServers\": {\n    \"ajent\": {\n      \"command\": \"npx\",\n      \"args\": [\n        \"-y\",\n        \"some-other-mcp-server\"\n      ]\n    }\n  }\n}\n")
	if err := os.WriteFile(w.configPath(p), data, 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	result, err := w.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Status != "incomplete" {
		t.Fatalf("Apply() status = %q, want incomplete", result.Status)
	}
	wantDetail := "an existing ajent entry with other settings was preserved"
	if result.Detail != wantDetail {
		t.Fatalf("Apply() detail = %q, want %q", result.Detail, wantDetail)
	}

	got, err := os.ReadFile(w.configPath(p))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("file mutated:\n got:\n%s\nwant:\n%s", got, data)
	}
}

func TestCursorRemove(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o700); err != nil {
		t.Fatalf("mkdir .cursor: %v", err)
	}
	p := cursorTestParams(home)
	w := cursorWriter{}

	seed := []byte("{\n  \"mcpServers\": {\n    \"github\": {\n      \"command\": \"npx\",\n      \"args\": [\n        \"-y\",\n        \"@modelcontextprotocol/server-github\"\n      ]\n    }\n  }\n}\n")
	if err := os.WriteFile(w.configPath(p), seed, 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if _, err := w.Apply(context.Background(), p); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	result, err := w.Remove(context.Background(), p)
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if result.Status != "removed" {
		t.Fatalf("Remove() status = %q, want removed", result.Status)
	}

	data, err := os.ReadFile(w.configPath(p))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	section, ok := doc["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers missing or invalid: %#v", doc["mcpServers"])
	}
	if _, present := section["ajent"]; present {
		t.Fatal("ajent entry still present after Remove")
	}
	if _, present := section["github"]; !present {
		t.Fatal("github entry removed by Remove; want it kept")
	}
}

func TestCursorCustomConfigDirEnv(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o700); err != nil {
		t.Fatalf("mkdir .cursor: %v", err)
	}
	p := cursorTestParams(home)
	p.ConfigDir = filepath.Join(home, "custom-aj")
	w := cursorWriter{}

	if _, err := w.Apply(context.Background(), p); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	data, err := os.ReadFile(w.configPath(p))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var doc struct {
		McpServers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	ajent, ok := doc.McpServers["ajent"]
	if !ok {
		t.Fatal("mcpServers.ajent missing")
	}
	if ajent.Env["AJENT_CONFIG_DIR"] != p.ConfigDir {
		t.Fatalf("env.AJENT_CONFIG_DIR = %q, want %q", ajent.Env["AJENT_CONFIG_DIR"], p.ConfigDir)
	}
}
