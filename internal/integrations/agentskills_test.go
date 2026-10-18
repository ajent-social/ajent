// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// seedSkill writes content at p's skill path, creating parent directories
// first.
func seedSkill(t *testing.T, p Params, content string) string {
	t.Helper()
	path := skillPath(p)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("seed skill file: %v", err)
	}
	return path
}

func TestSkillInstallFresh(t *testing.T) {
	p := defaultTestParams(t.TempDir())
	w := agentSkillsWriter{}

	result, err := w.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Status != "configured" {
		t.Fatalf("Apply() status = %q, want configured", result.Status)
	}

	want, err := skillContent()
	if err != nil {
		t.Fatalf("skillContent() error = %v", err)
	}
	got, err := os.ReadFile(skillPath(p))
	if err != nil {
		t.Fatalf("read installed skill: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("installed skill content differs from the embedded content")
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(skillPath(p))
		if err != nil {
			t.Fatalf("stat installed skill: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("installed skill mode = %o, want 0600", perm)
		}
	}
}

func TestSkillSecondRunUnchanged(t *testing.T) {
	p := defaultTestParams(t.TempDir())
	w := agentSkillsWriter{}

	if _, err := w.Apply(context.Background(), p); err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}

	result, err := w.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("second Apply() error = %v", err)
	}
	if result.Status != "unchanged" {
		t.Fatalf("second Apply() status = %q, want unchanged", result.Status)
	}
	if result.Detail != "" {
		t.Fatalf("second Apply() detail = %q, want empty", result.Detail)
	}
}

func TestSkillRefreshManaged(t *testing.T) {
	p := defaultTestParams(t.TempDir())
	old := "---\nname: network\ndescription: stale copy\n---\n\n" +
		managedMarker + "\n\nThis is an old, ajent-managed skill body.\n"
	path := seedSkill(t, p, old)

	w := agentSkillsWriter{}
	result, err := w.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Status != "configured" {
		t.Fatalf("Apply() status = %q, want configured", result.Status)
	}

	want, err := skillContent()
	if err != nil {
		t.Fatalf("skillContent() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read refreshed skill: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("refreshed skill content does not match the embedded content")
	}
}

func TestSkillReplacesPythonEra(t *testing.T) {
	p := defaultTestParams(t.TempDir())
	old := "---\nname: network\ndescription: legacy python skill\n---\n\n" +
		"Run `python3 ~/.aj/client.py login` to sign in.\n"
	path := seedSkill(t, p, old)

	w := agentSkillsWriter{}
	result, err := w.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Status != "configured" {
		t.Fatalf("Apply() status = %q, want configured", result.Status)
	}

	want, err := skillContent()
	if err != nil {
		t.Fatalf("skillContent() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read replaced skill: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("python-era skill was not replaced with the embedded content")
	}
}

func TestSkillPreservesForeign(t *testing.T) {
	p := defaultTestParams(t.TempDir())
	foreign := "# My own network notes\n\nThis file is not ajent's.\n"
	path := seedSkill(t, p, foreign)

	w := agentSkillsWriter{}
	result, err := w.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Status != "unchanged" {
		t.Fatalf("Apply() status = %q, want unchanged", result.Status)
	}
	if result.Detail != "existing skill preserved" {
		t.Fatalf("Apply() detail = %q, want %q", result.Detail, "existing skill preserved")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read foreign skill: %v", err)
	}
	if string(got) != foreign {
		t.Fatalf("Apply() modified a foreign skill file")
	}
}

func TestSkillRefusesSymlink(t *testing.T) {
	p := defaultTestParams(t.TempDir())
	path := skillPath(p)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}

	realFile := filepath.Join(p.Home, "real-skill.md")
	if err := os.WriteFile(realFile, []byte("real"), 0o600); err != nil {
		t.Fatalf("seed real file: %v", err)
	}
	if err := os.Symlink(realFile, path); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation not permitted: %v", err)
		}
		t.Fatalf("Symlink() error = %v", err)
	}

	w := agentSkillsWriter{}
	result, err := w.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Status != "incomplete" {
		t.Fatalf("Apply() status = %q, want incomplete", result.Status)
	}
	if result.Detail == "" {
		t.Fatalf("Apply() detail is empty, want a symlink refusal message")
	}
}

func TestSkillRefusesGroupWritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only permission check")
	}

	p := defaultTestParams(t.TempDir())
	skillsDir := filepath.Join(p.Home, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", skillsDir, err)
	}
	if err := os.Chmod(skillsDir, 0o775); err != nil { //nolint:gosec // G302: the group-writable mode is the test fixture TestSkillRefusesGroupWritable exists to exercise, not a real permission grant.
		t.Fatalf("chmod %s: %v", skillsDir, err)
	}

	w := agentSkillsWriter{}
	result, err := w.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Status != "incomplete" {
		t.Fatalf("Apply() status = %q, want incomplete", result.Status)
	}
}

func TestSkillRemoveManagedOnly(t *testing.T) {
	p := defaultTestParams(t.TempDir())
	w := agentSkillsWriter{}

	// Nothing installed yet: Remove is a no-op.
	result, err := w.Remove(context.Background(), p)
	if err != nil {
		t.Fatalf("Remove() on absent file error = %v", err)
	}
	if result.Status != "unchanged" {
		t.Fatalf("Remove() on absent file status = %q, want unchanged", result.Status)
	}

	if _, err := w.Apply(context.Background(), p); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	result, err = w.Remove(context.Background(), p)
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if result.Status != "removed" {
		t.Fatalf("Remove() status = %q, want removed", result.Status)
	}
	if _, err := os.Stat(skillPath(p)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("skill file still exists after Remove(): err = %v", err)
	}
	networkDir := filepath.Join(p.Home, ".agents", "skills", "ajent-network")
	if _, err := os.Stat(networkDir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ajent-network dir still exists after Remove(): err = %v", err)
	}

	// A foreign file must be left alone.
	foreign := "# not managed\n"
	path := seedSkill(t, p, foreign)
	result, err = w.Remove(context.Background(), p)
	if err != nil {
		t.Fatalf("Remove() foreign error = %v", err)
	}
	if result.Status != "unchanged" {
		t.Fatalf("Remove() foreign status = %q, want unchanged", result.Status)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read foreign file after Remove(): %v", err)
	}
	if string(got) != foreign {
		t.Fatalf("Remove() modified a foreign skill file")
	}
}

func TestManagedPythonNewlines(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       bool
	}{
		{"LF", "---\nname: network\n---\nclient.py\n", true},
		{"CRLF", "---\r\nname: network\r\n---\r\nclient.py\r\n", true},
		{"foreign", "---\r\nname: other\r\n---\r\nclient.py\r\n", false},
		{"no python", "---\r\nname: network\r\n---\r\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := managed([]byte(tc.text)); got != tc.want {
				t.Fatalf("managed=%v, want %v", got, tc.want)
			}
		})
	}
}
