// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDirHonorsEnv(t *testing.T) {
	tmp := t.TempDir()
	want := filepath.Join(tmp, "x")
	t.Setenv("AJENT_CONFIG_DIR", want)

	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir() error = %v", err)
	}
	if got != want {
		t.Fatalf("Dir() = %q, want %q", got, want)
	}

	info, err := os.Stat(want)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", want, err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", want)
	}

	if runtime.GOOS != "windows" {
		if mode := info.Mode().Perm(); mode != 0o700 {
			t.Fatalf("mode = %o, want %o", mode, 0o700)
		}
	}
}

func TestDirRefusesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks are privileged on windows")
	}

	tmp := t.TempDir()
	realDir := filepath.Join(tmp, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	t.Setenv("AJENT_CONFIG_DIR", link)

	if _, err := Dir(); !errors.Is(err, ErrSymlink) {
		t.Fatalf("Dir() error = %v, want ErrSymlink", err)
	}
}

func TestWritePrivateAtomicReplace(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "file")

	if err := WritePrivate(path, []byte("a")); err != nil {
		t.Fatalf("WritePrivate(a) error = %v", err)
	}
	if err := WritePrivate(path, []byte("b")); err != nil {
		t.Fatalf("WritePrivate(b) error = %v", err)
	}

	data, err := os.ReadFile(path) //nolint:gosec // G304: path is this test's own t.TempDir file, written by the test just above; not external input.
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "b" {
		t.Fatalf("content = %q, want %q", data, "b")
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if matched, _ := filepath.Match(".ajent-*", e.Name()); matched {
			t.Fatalf("leftover temp file %q", e.Name())
		}
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Fatalf("mode = %o, want %o", mode, 0o600)
		}
	}
}

func TestWritePrivateRefusesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks are privileged on windows")
	}

	tmp := t.TempDir()
	realFile := filepath.Join(tmp, "real")
	if err := os.WriteFile(realFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(realFile, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	if err := WritePrivate(link, []byte("y")); !errors.Is(err, ErrSymlink) {
		t.Fatalf("WritePrivate() error = %v, want ErrSymlink", err)
	}
}

func TestCreateExclusive(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "file")

	if err := CreateExclusive(path, []byte("first")); err != nil {
		t.Fatalf("first CreateExclusive() error = %v", err)
	}

	if err := CreateExclusive(path, []byte("second")); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second CreateExclusive() error = %v, want fs.ErrExist", err)
	}

	data, err := os.ReadFile(path) //nolint:gosec // G304: path is this test's own t.TempDir file, written by the test just above; not external input.
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "first" {
		t.Fatalf("content = %q, want %q", data, "first")
	}
}

func TestReadPrivateRefusesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks are privileged on windows")
	}

	tmp := t.TempDir()
	realFile := filepath.Join(tmp, "real")
	if err := os.WriteFile(realFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(realFile, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	if _, err := ReadPrivate(link); !errors.Is(err, ErrSymlink) {
		t.Fatalf("ReadPrivate() error = %v, want ErrSymlink", err)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "file.json")

	type payload struct {
		Name string `json:"name"`
		N    int    `json:"n"`
	}
	want := payload{Name: "ajent", N: 7}

	if err := WriteJSON(path, want); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	raw, err := os.ReadFile(path) //nolint:gosec // G304: path is this test's own t.TempDir file, written by the test just above; not external input.
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	wantRaw := "{\n  \"name\": \"ajent\",\n  \"n\": 7\n}\n"
	if string(raw) != wantRaw {
		t.Fatalf("raw = %q, want %q", raw, wantRaw)
	}

	var got payload
	if err := ReadJSON(path, &got); err != nil {
		t.Fatalf("ReadJSON() error = %v", err)
	}
	if got != want {
		t.Fatalf("ReadJSON() = %+v, want %+v", got, want)
	}
}
