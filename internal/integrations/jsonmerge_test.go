// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ajent-social/ajent/internal/config"
)

func normalizeFixtureNewlines(data []byte) []byte {
	return bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
}

// defaultTestParams returns Params for a fake home with the default config
// dir and no credential file, so Entry(p, ...) has no "env" key.
func defaultTestParams(home string) Params {
	return Params{
		Home:      home,
		ConfigDir: filepath.Join(home, ".aj"),
		Binary:    "/usr/local/bin/ajent",
	}
}

func goldenFixture(elem ...string) string {
	return filepath.Join(append([]string{"..", "..", "testdata", "integrations", "jsonmerge"}, elem...)...)
}

func TestMergeGolden(t *testing.T) {
	cases := []string{
		"empty-file",
		"foreign-servers-kept",
		"other-top-level-keys-kept",
		"replace-python-entry",
		"unchanged-second-run",
		"section-missing",
	}

	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "cursor", "mcp.json")

			before := goldenFixture(name, "before.json")
			switch data, err := os.ReadFile(before); {
			case err == nil:
				if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
					t.Fatalf("seed dir: %v", err)
				}
				if err := os.WriteFile(target, data, 0o600); err != nil {
					t.Fatalf("seed before.json: %v", err)
				}
			case os.IsNotExist(err):
				// "before absent" case: leave target missing.
			default:
				t.Fatalf("read before.json: %v", err)
			}

			home := filepath.Join(dir, "home")
			p := defaultTestParams(home)
			entry := Entry(p, "cursor")

			if _, err := MergeJSONServer(target, "mcpServers", entry); err != nil {
				t.Fatalf("MergeJSONServer() error = %v", err)
			}

			got, err := os.ReadFile(target)
			if err != nil {
				t.Fatalf("read result: %v", err)
			}
			want, err := os.ReadFile(goldenFixture(name, "after.json"))
			if err != nil {
				t.Fatalf("read after.json: %v", err)
			}
			if !bytes.Equal(normalizeFixtureNewlines(got), normalizeFixtureNewlines(want)) {
				t.Errorf("%s:\n got:\n%s\nwant:\n%s", name, got, want)
			}
		})
	}
}

func TestMergeSecondRunIdentical(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mcp.json")
	p := defaultTestParams(filepath.Join(dir, "home"))
	entry := Entry(p, "cursor")

	changed1, err := MergeJSONServer(target, "mcpServers", entry)
	if err != nil {
		t.Fatalf("first MergeJSONServer() error = %v", err)
	}
	if !changed1 {
		t.Fatal("first MergeJSONServer() changed = false, want true")
	}
	first, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after first run: %v", err)
	}

	changed2, err := MergeJSONServer(target, "mcpServers", entry)
	if err != nil {
		t.Fatalf("second MergeJSONServer() error = %v", err)
	}
	if changed2 {
		t.Fatal("second MergeJSONServer() changed = true, want false")
	}
	second, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after second run: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("second run bytes differ:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestMergeRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	realFile := filepath.Join(dir, "real.json")
	if err := os.WriteFile(realFile, []byte("{}"), 0o600); err != nil {
		t.Fatalf("seed real file: %v", err)
	}
	link := filepath.Join(dir, "mcp.json")
	if err := os.Symlink(realFile, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation not permitted: %v", err)
		}
		t.Fatalf("Symlink() error = %v", err)
	}

	p := defaultTestParams(filepath.Join(dir, "home"))
	entry := Entry(p, "cursor")

	if _, err := MergeJSONServer(link, "mcpServers", entry); !errors.Is(err, config.ErrSymlink) {
		t.Fatalf("MergeJSONServer() error = %v, want ErrSymlink", err)
	}
}

func TestMergeJSONCUnchanged(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mcp.json")
	data, err := os.ReadFile(goldenFixture("jsonc", "before.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	p := defaultTestParams(filepath.Join(dir, "home"))
	entry := Entry(p, "cursor")

	changed, err := MergeJSONServer(target, "mcpServers", entry)
	if !errors.Is(err, ErrUnparseable) {
		t.Fatalf("MergeJSONServer() error = %v, want ErrUnparseable", err)
	}
	if changed {
		t.Fatal("MergeJSONServer() changed = true, want false")
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("file mutated:\n got:\n%s\nwant:\n%s", got, data)
	}
}

func TestMergeForeignEntry(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mcp.json")
	data, err := os.ReadFile(goldenFixture("foreign-ajent", "before.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	p := defaultTestParams(filepath.Join(dir, "home"))
	entry := Entry(p, "cursor")

	changed, err := MergeJSONServer(target, "mcpServers", entry)
	if !errors.Is(err, ErrForeignEntry) {
		t.Fatalf("MergeJSONServer() error = %v, want ErrForeignEntry", err)
	}
	if changed {
		t.Fatal("MergeJSONServer() changed = true, want false")
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("file mutated:\n got:\n%s\nwant:\n%s", got, data)
	}
}

func TestRemoveOwnedOnly(t *testing.T) {
	t.Run("removes an owned entry and keeps the section", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "mcp.json")
		p := defaultTestParams(filepath.Join(dir, "home"))
		entry := Entry(p, "cursor")
		if _, err := MergeJSONServer(target, "mcpServers", entry); err != nil {
			t.Fatalf("seed MergeJSONServer() error = %v", err)
		}

		changed, err := RemoveJSONServer(target, "mcpServers")
		if err != nil {
			t.Fatalf("RemoveJSONServer() error = %v", err)
		}
		if !changed {
			t.Fatal("RemoveJSONServer() changed = false, want true")
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
			t.Fatalf("mcpServers section removed or invalid; want it left in place, got %#v", doc["mcpServers"])
		}
		if _, present := section["ajent"]; present {
			t.Fatal("ajent entry still present after remove")
		}
	})

	t.Run("absent key is a no-op", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "mcp.json")
		seed := []byte("{\n  \"mcpServers\": {}\n}\n")
		if err := os.WriteFile(target, seed, 0o600); err != nil {
			t.Fatalf("seed target: %v", err)
		}

		changed, err := RemoveJSONServer(target, "mcpServers")
		if err != nil {
			t.Fatalf("RemoveJSONServer() error = %v", err)
		}
		if changed {
			t.Fatal("RemoveJSONServer() changed = true, want false")
		}
		after, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("read target: %v", err)
		}
		if !bytes.Equal(after, seed) {
			t.Fatalf("file mutated for absent key:\n got:\n%s\nwant:\n%s", after, seed)
		}
	})

	t.Run("missing file is a no-op", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "mcp.json")

		changed, err := RemoveJSONServer(target, "mcpServers")
		if err != nil {
			t.Fatalf("RemoveJSONServer() error = %v", err)
		}
		if changed {
			t.Fatal("RemoveJSONServer() changed = true, want false")
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatalf("RemoveJSONServer() created %s for a missing file", target)
		}
	})

	t.Run("foreign entry is preserved", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "mcp.json")
		data, err := os.ReadFile(goldenFixture("foreign-ajent", "before.json"))
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			t.Fatalf("seed target: %v", err)
		}

		changed, err := RemoveJSONServer(target, "mcpServers")
		if !errors.Is(err, ErrForeignEntry) {
			t.Fatalf("RemoveJSONServer() error = %v, want ErrForeignEntry", err)
		}
		if changed {
			t.Fatal("RemoveJSONServer() changed = true, want false")
		}

		got, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("read target: %v", err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("file mutated:\n got:\n%s\nwant:\n%s", got, data)
		}
	})
}
