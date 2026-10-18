// SPDX-License-Identifier: Apache-2.0

package version

import (
	"encoding/json"
	"runtime"
	"sort"
	"testing"
)

func TestGetDefaults(t *testing.T) {
	info := Get()

	if info.Version != "dev" {
		t.Errorf("Version = %q, want %q", info.Version, "dev")
	}
	if info.Commit != "none" {
		t.Errorf("Commit = %q, want %q", info.Commit, "none")
	}
	if info.Date != "unknown" {
		t.Errorf("Date = %q, want %q", info.Date, "unknown")
	}
	if info.ClientKind != "go-cli" {
		t.Errorf("ClientKind = %q, want %q", info.ClientKind, "go-cli")
	}
	if info.GoVersion == "" {
		t.Error("GoVersion is empty, want non-empty")
	}
	if info.OS != runtime.GOOS {
		t.Errorf("OS = %q, want %q", info.OS, runtime.GOOS)
	}
	if info.Arch != runtime.GOARCH {
		t.Errorf("Arch = %q, want %q", info.Arch, runtime.GOARCH)
	}
}

func TestInfoJSONFieldNames(t *testing.T) {
	data, err := json.Marshal(Get())
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	want := []string{"version", "commit", "date", "client_kind", "go_version", "os", "arch"}
	got := make([]string, 0, len(m))
	for k := range m {
		got = append(got, k)
	}

	sort.Strings(want)
	sort.Strings(got)

	if len(got) != len(want) {
		t.Fatalf("key set = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("key set = %v, want %v", got, want)
		}
	}
}

func TestUserAgent(t *testing.T) {
	orig := Version
	Version = "1.2.3"
	t.Cleanup(func() { Version = orig })

	want := "ajent/1.2.3 (" + runtime.GOOS + "/" + runtime.GOARCH + ")"
	if got := UserAgent(); got != want {
		t.Errorf("UserAgent() = %q, want %q", got, want)
	}
}
