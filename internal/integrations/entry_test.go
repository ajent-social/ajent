// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestOwned(t *testing.T) {
	cases := []struct {
		name    string
		command string
		args    []any
		want    bool
	}{
		{
			name:    "ajent",
			command: "ajent",
			args:    []any{"mcp", "--profile", "cursor", "--project-profile"},
			want:    true,
		},
		{
			name:    "ajent.exe",
			command: "ajent.exe",
			args:    []any{"mcp", "--profile", "cursor", "--project-profile"},
			want:    true,
		},
		{
			name:    "AJENT.EXE uppercase",
			command: "AJENT.EXE",
			args:    []any{"mcp"},
			want:    true,
		},
		{
			name:    "python3 plus client.py",
			command: "python3",
			args:    []any{"/HOME/.aj/client.py", "mcp", "--profile", "cursor", "--project-profile"},
			want:    true,
		},
		{
			name:    "versioned python3.12 plus client.py",
			command: "/opt/homebrew/bin/python3.12",
			args:    []any{"/HOME/.aj/client.py", "mcp", "--profile", "cursor", "--project-profile"},
			want:    true,
		},
		{
			name:    "node",
			command: "node",
			args:    []any{"server.js"},
			want:    false,
		},
		{
			name:    "npx with foreign args",
			command: "npx",
			args:    []any{"-y", "@modelcontextprotocol/server-github"},
			want:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := map[string]any{"command": tc.command, "args": tc.args}
			if got := Owned(entry); got != tc.want {
				t.Errorf("Owned(command=%q, args=%v) = %v, want %v", tc.command, tc.args, got, tc.want)
			}
		})
	}
}

func TestEntryEnv(t *testing.T) {
	home := filepath.FromSlash("/HOME")
	defaultDir := filepath.Join(home, ".aj")

	t.Run("default dir and no credential file gives nil", func(t *testing.T) {
		p := Params{Home: home, ConfigDir: defaultDir}
		if env := EntryEnv(p); env != nil {
			t.Fatalf("EntryEnv() = %v, want nil", env)
		}
		if e := Entry(p, "cursor"); e["env"] != nil {
			t.Fatalf("Entry()[env] = %v, want absent", e["env"])
		}
	})

	t.Run("custom config dir", func(t *testing.T) {
		custom := filepath.Join(home, "custom-aj")
		p := Params{Home: home, ConfigDir: custom}
		want := map[string]string{"AJENT_CONFIG_DIR": custom}
		if env := EntryEnv(p); !reflect.DeepEqual(env, want) {
			t.Fatalf("EntryEnv() = %v, want %v", env, want)
		}
	})

	t.Run("credential file override", func(t *testing.T) {
		cred := filepath.Join(home, "credential")
		p := Params{Home: home, ConfigDir: defaultDir, CredentialFile: cred}
		want := map[string]string{"AJENT_CREDENTIAL_FILE": cred}
		if env := EntryEnv(p); !reflect.DeepEqual(env, want) {
			t.Fatalf("EntryEnv() = %v, want %v", env, want)
		}
	})

	t.Run("custom dir and credential file", func(t *testing.T) {
		custom := filepath.Join(home, "custom-aj")
		cred := filepath.Join(home, "credential")
		p := Params{Home: home, ConfigDir: custom, CredentialFile: cred}
		want := map[string]string{"AJENT_CONFIG_DIR": custom, "AJENT_CREDENTIAL_FILE": cred}
		if env := EntryEnv(p); !reflect.DeepEqual(env, want) {
			t.Fatalf("EntryEnv() = %v, want %v", env, want)
		}
	})
}
