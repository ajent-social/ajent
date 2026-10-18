// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// vscodeTestParams returns Params for a fake home with the default config
// dir, no credential file and the real runtime GOOS, so
// vscodeWriter{}.configPath(p) resolves to a path under the temp home on
// whichever OS runs the test.
func vscodeTestParams(home string) Params {
	return Params{
		Home:      home,
		Binary:    "/opt/ajent/bin/ajent",
		ConfigDir: filepath.Join(home, ".aj"),
		GOOS:      runtime.GOOS,
		Getenv:    func(string) string { return "" },
	}
}

// vscodeFixturePath returns the path to a golden fixture file. The fixture
// tree mirrors a linux home (.config/Code/User/mcp.json) regardless of the
// host running the test; TestVSCodeGolden reads the fixture's bytes and
// writes them at the real, per-OS userDir it computes for the temp home.
func vscodeFixturePath(caseName, half string) string {
	return filepath.Join("..", "..", "testdata", "integrations", "vscode", caseName, half, ".config", "Code", "User", "mcp.json")
}

func TestVSCodeUserDir(t *testing.T) {
	home := filepath.Join("home", "dave")

	cases := []struct {
		name   string
		goos   string
		getenv func(string) string
		want   string
	}{
		{
			name:   "darwin",
			goos:   "darwin",
			getenv: func(string) string { return "" },
			want:   filepath.Join(home, "Library", "Application Support", "Code", "User"),
		},
		{
			name: "windows with APPDATA set",
			goos: "windows",
			getenv: func(k string) string {
				if k == "APPDATA" {
					return filepath.Join(home, "CustomAppData")
				}
				return ""
			},
			want: filepath.Join(home, "CustomAppData", "Code", "User"),
		},
		{
			name:   "windows with APPDATA empty",
			goos:   "windows",
			getenv: func(string) string { return "" },
			want:   filepath.Join(home, "AppData", "Roaming", "Code", "User"),
		},
		{
			name: "linux with XDG_CONFIG_HOME",
			goos: "linux",
			getenv: func(k string) string {
				if k == "XDG_CONFIG_HOME" {
					return "/x/cfg"
				}
				return ""
			},
			want: filepath.Join("/x/cfg", "Code", "User"),
		},
		{
			name: "linux with a relative XDG_CONFIG_HOME falls back to ~/.config",
			goos: "linux",
			getenv: func(k string) string {
				if k == "XDG_CONFIG_HOME" {
					return "relative/cfg"
				}
				return ""
			},
			want: filepath.Join(home, ".config", "Code", "User"),
		},
	}

	w := vscodeWriter{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Params{Home: home, GOOS: tc.goos, Getenv: tc.getenv}
			if got := w.userDir(p); got != tc.want {
				t.Errorf("userDir() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestVSCodeGolden(t *testing.T) {
	cases := []string{"fresh-dir", "foreign-servers", "python-entry", "current"}

	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			p := vscodeTestParams(home)
			w := vscodeWriter{}

			dest := w.configPath(p)
			if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
				t.Fatalf("mkdir userDir: %v", err)
			}

			before := vscodeFixturePath(name, "before")
			switch data, err := os.ReadFile(before); {
			case err == nil:
				if err := os.WriteFile(dest, data, 0o600); err != nil {
					t.Fatalf("seed before.json: %v", err)
				}
			case os.IsNotExist(err):
				// fresh-dir: the directory exists but mcp.json does not yet.
			default:
				t.Fatalf("read before.json: %v", err)
			}

			if _, err := w.Apply(context.Background(), p); err != nil {
				t.Fatalf("Apply() error = %v", err)
			}

			got, err := os.ReadFile(dest)
			if err != nil {
				t.Fatalf("read result: %v", err)
			}
			want, err := os.ReadFile(vscodeFixturePath(name, "after"))
			if err != nil {
				t.Fatalf("read after.json: %v", err)
			}
			if !bytes.Equal(normalizeFixtureNewlines(got), normalizeFixtureNewlines(want)) {
				t.Errorf("%s:\n got:\n%s\nwant:\n%s", name, got, want)
			}
		})
	}
}

func TestVSCodeSecondRunUnchanged(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	p := vscodeTestParams(home)
	w := vscodeWriter{}
	if err := os.MkdirAll(w.userDir(p), 0o700); err != nil {
		t.Fatalf("mkdir userDir: %v", err)
	}

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

func TestVSCodeNotDetected(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home") // userDir under home is never created
	p := vscodeTestParams(home)

	w := vscodeWriter{}
	result, err := w.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Status != "skipped" {
		t.Fatalf("Apply() status = %q, want skipped", result.Status)
	}
	if _, err := os.Stat(w.configPath(p)); !os.IsNotExist(err) {
		t.Fatalf("Apply() created %s though VS Code was not detected", w.configPath(p))
	}
}

func TestVSCodeJSONCPreserved(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	p := vscodeTestParams(home)
	w := vscodeWriter{}
	if err := os.MkdirAll(w.userDir(p), 0o700); err != nil {
		t.Fatalf("mkdir userDir: %v", err)
	}
	dest := w.configPath(p)
	data := []byte("{\n  // a comment VS Code's jsonc settings allow\n  \"servers\": {}\n}\n")
	if err := os.WriteFile(dest, data, 0o600); err != nil {
		t.Fatalf("seed jsonc file: %v", err)
	}

	result, err := w.Apply(context.Background(), p)
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

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("file mutated:\n got:\n%s\nwant:\n%s", got, data)
	}
}

func TestVSCodeForeignEntryPreserved(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	p := vscodeTestParams(home)
	w := vscodeWriter{}
	if err := os.MkdirAll(w.userDir(p), 0o700); err != nil {
		t.Fatalf("mkdir userDir: %v", err)
	}
	dest := w.configPath(p)
	data := []byte("{\n  \"servers\": {\n    \"ajent\": {\n      \"command\": \"node\",\n      \"args\": [\n        \"server.js\"\n      ]\n    }\n  }\n}\n")
	if err := os.WriteFile(dest, data, 0o600); err != nil {
		t.Fatalf("seed foreign file: %v", err)
	}

	result, err := w.Apply(context.Background(), p)
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

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("file mutated:\n got:\n%s\nwant:\n%s", got, data)
	}
}

func TestVSCodeRemove(t *testing.T) {
	t.Run("removes an owned entry and keeps the section", func(t *testing.T) {
		dir := t.TempDir()
		home := filepath.Join(dir, "home")
		p := vscodeTestParams(home)
		w := vscodeWriter{}
		if err := os.MkdirAll(w.userDir(p), 0o700); err != nil {
			t.Fatalf("mkdir userDir: %v", err)
		}

		if _, err := w.Apply(context.Background(), p); err != nil {
			t.Fatalf("seed Apply() error = %v", err)
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
			t.Fatalf("read dest: %v", err)
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		section, ok := doc["servers"].(map[string]any)
		if !ok {
			t.Fatalf("servers section removed or invalid; want it left in place, got %#v", doc["servers"])
		}
		if _, present := section["ajent"]; present {
			t.Fatal("ajent entry still present after Remove")
		}
	})

	t.Run("leaves a foreign entry unchanged", func(t *testing.T) {
		dir := t.TempDir()
		home := filepath.Join(dir, "home")
		p := vscodeTestParams(home)
		w := vscodeWriter{}
		if err := os.MkdirAll(w.userDir(p), 0o700); err != nil {
			t.Fatalf("mkdir userDir: %v", err)
		}
		dest := w.configPath(p)
		data := []byte("{\n  \"servers\": {\n    \"ajent\": {\n      \"command\": \"node\",\n      \"args\": [\n        \"server.js\"\n      ]\n    }\n  }\n}\n")
		if err := os.WriteFile(dest, data, 0o600); err != nil {
			t.Fatalf("seed foreign file: %v", err)
		}

		result, err := w.Remove(context.Background(), p)
		if err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
		if result.Status != "unchanged" {
			t.Fatalf("Remove() status = %q, want unchanged", result.Status)
		}

		got, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("read dest: %v", err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("file mutated:\n got:\n%s\nwant:\n%s", got, data)
		}
	})

	t.Run("no config file is a no-op", func(t *testing.T) {
		dir := t.TempDir()
		home := filepath.Join(dir, "home")
		p := vscodeTestParams(home)

		w := vscodeWriter{}
		result, err := w.Remove(context.Background(), p)
		if err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
		if result.Status != "unchanged" {
			t.Fatalf("Remove() status = %q, want unchanged", result.Status)
		}
		if _, err := os.Stat(w.configPath(p)); !os.IsNotExist(err) {
			t.Fatalf("Remove() created %s for a missing file", w.configPath(p))
		}
	})
}
