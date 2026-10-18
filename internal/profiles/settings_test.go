// SPDX-License-Identifier: Apache-2.0

package profiles

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ajent-social/ajent/internal/config"
	"github.com/ajent-social/ajent/internal/credentials"
)

// profilesWriteJSON marshals v as JSON and writes it to path, creating any
// missing parent directory first. It is shared by settings_test.go and
// enroll_test.go.
func profilesWriteJSON(t *testing.T, path string, v any) {
	t.Helper()

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("profilesWriteJSON: marshaling %s: %v", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("profilesWriteJSON: mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("profilesWriteJSON: writing %s: %v", path, err)
	}
}

// profilesTestRoot returns a fixed, deterministic root credential so grant
// files written under its Identity() line up across a test's setup and
// call.
func profilesTestRoot() credentials.Credential {
	return credentials.Credential{
		Server: "https://ajent.social",
		Token:  "aj_fedcba9876543210_settingstesttoken0000000000000000000000000",
	}
}

func TestScopeSettingsTable(t *testing.T) {
	const (
		userGrantID    = "00000000-0000-4000-8000-000000000010"
		projectGrantID = "00000000-0000-4000-8000-000000000011"
	)

	tests := []struct {
		name        string
		skipWindows bool
		setup       func(t *testing.T, dir, projectRoot, scope string, root credentials.Credential)
		want        Settings
		wantOK      bool
		wantErr     error
		wantErrText string
	}{
		{
			name:   "no files",
			setup:  func(*testing.T, string, string, string, credentials.Credential) {},
			want:   Settings{},
			wantOK: false,
		},
		{
			name: "a user-only file",
			setup: func(t *testing.T, dir, _, _ string, root credentials.Credential) {
				profilesWriteJSON(t, filepath.Join(dir, "config.json"), map[string]any{
					"version": 1, "domain": "example.com", "private_only": false,
				})
				profilesWriteJSON(t, filepath.Join(dir, "scope-grants.json"), map[string]any{
					root.Identity(): map[string]any{
						"user": []map[string]string{{"domain": "example.com", "id": userGrantID}},
					},
				})
			},
			want: Settings{
				Grants:      []string{userGrantID},
				Domain:      "example.com",
				PrivateOnly: false,
			},
			wantOK: true,
		},
		{
			name: "project overriding user",
			setup: func(t *testing.T, dir, projectRoot, scope string, root credentials.Credential) {
				profilesWriteJSON(t, filepath.Join(dir, "config.json"), map[string]any{
					"domain": "user-domain.com", "private_only": false,
				})
				profilesWriteJSON(t, filepath.Join(projectRoot, ".aj", "config.json"), map[string]any{
					"domain": "project-domain.com", "private_only": false,
				})
				profilesWriteJSON(t, filepath.Join(dir, "scope-grants.json"), map[string]any{
					root.Identity(): map[string]any{
						"user": []map[string]string{{"domain": "user-domain.com", "id": userGrantID}},
						scope:  []map[string]string{{"domain": "project-domain.com", "id": projectGrantID}},
					},
				})
			},
			want: Settings{
				Grants:      []string{userGrantID, projectGrantID},
				Domain:      "project-domain.com",
				PrivateOnly: false,
			},
			wantOK: true,
		},
		{
			name: "private_only true",
			setup: func(t *testing.T, dir, _, _ string, _ credentials.Credential) {
				profilesWriteJSON(t, filepath.Join(dir, "config.json"), map[string]any{
					"domain": "example.com", "private_only": true,
				})
			},
			want: Settings{
				Grants:      []string{},
				Domain:      "",
				PrivateOnly: true,
			},
			wantOK: true,
		},
		{
			name: "an unapproved domain",
			setup: func(t *testing.T, dir, _, _ string, _ credentials.Credential) {
				profilesWriteJSON(t, filepath.Join(dir, "config.json"), map[string]any{
					"domain": "nope.example", "private_only": false,
				})
			},
			wantErr: errProfilesUnapprovedDomain,
		},
		{
			name: "a hostile project file listing a grant absent from scope-grants.json",
			setup: func(t *testing.T, _, projectRoot, _ string, _ credentials.Credential) {
				profilesWriteJSON(t, filepath.Join(projectRoot, ".aj", "config.json"), map[string]any{
					"domain": "evil.example", "private_only": false,
					"grants": []string{"evil.example"},
				})
			},
			wantErr: errProfilesUnapprovedDomain,
		},
		{
			name: "a non-bool private_only",
			setup: func(t *testing.T, dir, _, _ string, _ credentials.Credential) {
				profilesWriteJSON(t, filepath.Join(dir, "config.json"), map[string]any{
					"private_only": "yes",
				})
			},
			wantErr: errProfilesPrivateOnlyNotBool,
		},
		{
			name:        "a symlinked project config",
			skipWindows: true,
			setup: func(t *testing.T, _, projectRoot, _ string, _ credentials.Credential) {
				target := t.TempDir()
				profilesWriteJSON(t, filepath.Join(target, "config.json"), map[string]any{
					"domain": "example.com", "private_only": false,
				})
				if err := os.Symlink(target, filepath.Join(projectRoot, ".aj")); err != nil {
					t.Fatalf("symlinking .aj: %v", err)
				}
			},
			wantErrText: config.ErrSymlink.Error(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skipWindows && runtime.GOOS == "windows" {
				t.Skip("symlink creation needs elevated privileges on windows")
			}

			dir := t.TempDir()
			projectRoot := t.TempDir()
			root := profilesTestRoot()
			scope := credentials.ProjectScope(projectRoot)

			tc.setup(t, dir, projectRoot, scope, root)

			got, ok, err := ScopeSettings(dir, projectRoot, root)

			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("ScopeSettings() err = %v, want %v", err, tc.wantErr)
				}
				return
			case tc.wantErrText != "":
				if err == nil || !errors.Is(err, config.ErrSymlink) {
					t.Fatalf("ScopeSettings() err = %v, want it to wrap %v", err, config.ErrSymlink)
				}
				return
			}

			if err != nil {
				t.Fatalf("ScopeSettings() unexpected err: %v", err)
			}
			if ok != tc.wantOK {
				t.Fatalf("ScopeSettings() ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				if !settingsEqual(got, Settings{}) {
					t.Fatalf("ScopeSettings() = %+v, want zero value", got)
				}
				return
			}

			want := tc.want
			want.Scope = scope
			if !settingsEqual(got, want) {
				t.Fatalf("ScopeSettings() = %+v, want %+v", got, want)
			}

			// Grants must marshal as [], never null (docs/interfaces.md
			// "internal/profiles").
			data, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshaling Settings: %v", err)
			}
			if got.Grants != nil {
				var probe struct {
					Grants json.RawMessage `json:"grants"`
				}
				if err := json.Unmarshal(data, &probe); err != nil {
					t.Fatalf("unmarshaling Settings: %v", err)
				}
				if string(probe.Grants) == "null" {
					t.Fatalf("Settings marshaled grants as null, want []")
				}
			}
		})
	}
}

// settingsEqual compares two Settings values, treating Grants slices as
// equal when they hold the same elements regardless of nilness (Settings
// zero values and explicitly empty grant lists both compare as no
// grants).
func settingsEqual(a, b Settings) bool {
	if a.Scope != b.Scope || a.Domain != b.Domain || a.PrivateOnly != b.PrivateOnly {
		return false
	}
	if len(a.Grants) != len(b.Grants) {
		return false
	}
	for i := range a.Grants {
		if a.Grants[i] != b.Grants[i] {
			return false
		}
	}
	return true
}
