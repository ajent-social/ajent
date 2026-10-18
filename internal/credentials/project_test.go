// SPDX-License-Identifier: Apache-2.0

package credentials

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoldenHashes checks ProjectScope, ProjectProfile and
// Credential.Identity against testdata/contract/golden.json, the fixed
// values the Python reference client produced for the same inputs.
func TestGoldenHashes(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "contract", "golden.json")
	data, err := os.ReadFile(path) //nolint:gosec // fixed repo-relative testdata path, not user input
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}

	var golden struct {
		Token string `json:"token"`
		ID    string `json:"identity"`
		Cases []struct {
			Root         string            `json:"root"`
			ProjectScope string            `json:"project_scope"`
			Profiles     map[string]string `json:"profiles"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatalf("Unmarshal(golden.json): %v", err)
	}

	if got := (Credential{Token: golden.Token}).Identity(); got != golden.ID {
		t.Errorf("Identity() = %q, want %q", got, golden.ID)
	}

	for i, tc := range golden.Cases {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			if got := ProjectScope(tc.Root); got != tc.ProjectScope {
				t.Errorf("ProjectScope(%q) = %q, want %q", tc.Root, got, tc.ProjectScope)
			}
			for name, want := range tc.Profiles {
				if got := ProjectProfile(name, tc.Root); got != want {
					t.Errorf("ProjectProfile(%q, %q) = %q, want %q", name, tc.Root, got, want)
				}
			}
		})
	}
}

// TestProjectRoot walks a temp tree of markers and asserts ProjectRoot's
// walk-and-stop behavior, including the two edge cases the reference
// implementation is explicit about: a marker at home is still found
// (checked before the walk stops there), and a marker only above home is
// never seen (the walk stops at home and falls back to cwd).
func TestProjectRoot(t *testing.T) {
	base := t.TempDir()
	resolved, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", base, err)
	}
	base = resolved

	mkdirAll(t, filepath.Join(base, "home", "repo", "a", "b"))
	mkdirAll(t, filepath.Join(base, "home", "repo", ".git"))

	mkdirAll(t, filepath.Join(base, "home", "proj", "sub"))
	mkdirAll(t, filepath.Join(base, "home", "proj", ".aj"))
	writeFile(t, filepath.Join(base, "home", "proj", ".aj", "config.json"), `{}`)

	mkdirAll(t, filepath.Join(base, "tmp", "home", "x"))
	mkdirAll(t, filepath.Join(base, "tmp", ".git"))

	mkdirAll(t, filepath.Join(base, "home2", "x"))
	mkdirAll(t, filepath.Join(base, "home2", ".git"))

	mkdirAll(t, filepath.Join(base, "dirmarker", "proj2", ".aj", "config.json"))

	tests := []struct {
		name string
		cwd  string
		home string
		want string
	}{
		{
			name: ".git directory marker",
			cwd:  filepath.Join(base, "home", "repo", "a", "b"),
			home: filepath.Join(base, "home"),
			want: filepath.Join(base, "home", "repo"),
		},
		{
			name: ".aj/config.json regular-file marker",
			cwd:  filepath.Join(base, "home", "proj", "sub"),
			home: filepath.Join(base, "home"),
			want: filepath.Join(base, "home", "proj"),
		},
		{
			name: "marker only above home falls back to cwd",
			cwd:  filepath.Join(base, "tmp", "home", "x"),
			home: filepath.Join(base, "tmp", "home"),
			want: filepath.Join(base, "tmp", "home", "x"),
		},
		{
			name: "marker at home itself is found before stopping",
			cwd:  filepath.Join(base, "home2", "x"),
			home: filepath.Join(base, "home2"),
			want: filepath.Join(base, "home2"),
		},
		{
			name: ".aj/config.json as a directory is not a marker",
			cwd:  filepath.Join(base, "dirmarker", "proj2"),
			home: filepath.Join(base, "dirmarker"),
			want: filepath.Join(base, "dirmarker", "proj2"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProjectRoot(tc.cwd, tc.home); got != tc.want {
				t.Errorf("ProjectRoot(%q, %q) = %q, want %q", tc.cwd, tc.home, got, tc.want)
			}
		})
	}
}

// TestProjectProfileTruncates checks that a base longer than 30 bytes is
// cut to exactly 30 before the hash suffix is appended.
func TestProjectProfileTruncates(t *testing.T) {
	base := strings.Repeat("a", 45)
	root := "/some/root"

	got := ProjectProfile(base, root)

	const wantLen = 30 + 1 + 8
	if len(got) != wantLen {
		t.Fatalf("len(ProjectProfile()) = %d, want %d: %q", len(got), wantLen, got)
	}
	if got[:30] != base[:30] {
		t.Fatalf("ProjectProfile() prefix = %q, want %q", got[:30], base[:30])
	}
	if got[30] != '-' {
		t.Fatalf("ProjectProfile()[30] = %q, want '-'", string(got[30]))
	}

	sum := sha256.Sum256([]byte(root))
	wantSuffix := hex.EncodeToString(sum[:])[:8]
	if got[31:] != wantSuffix {
		t.Fatalf("ProjectProfile() suffix = %q, want %q", got[31:], wantSuffix)
	}
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
