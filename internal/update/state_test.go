// SPDX-License-Identifier: Apache-2.0

package update

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()

	auto := true
	want := State{
		LastCheck:      time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		LastNotice:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		LastAutoUpdate: time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC),
		SeenVersion:    "0.7.0",
		Auto:           &auto,
		Latest:         "0.7.1",
	}

	if err := SaveState(dir, want); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	got, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	if !got.LastCheck.Equal(want.LastCheck) ||
		!got.LastNotice.Equal(want.LastNotice) ||
		!got.LastAutoUpdate.Equal(want.LastAutoUpdate) ||
		got.SeenVersion != want.SeenVersion ||
		got.Latest != want.Latest ||
		got.Auto == nil || *got.Auto != *want.Auto {
		t.Fatalf("LoadState() = %+v, want %+v", got, want)
	}

	info, err := os.Stat(statePath(dir))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("state.json mode = %o, want 0600", perm)
		}
	}
}

func TestStateMissing(t *testing.T) {
	dir := t.TempDir()

	got, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got != (State{}) {
		t.Errorf("LoadState() = %+v, want zero value", got)
	}
}

func TestStateCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := statePath(dir)

	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got != (State{}) {
		t.Errorf("LoadState() = %+v, want zero value", got)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("state.json still present after quarantine: err = %v", err)
	}
	quarantined := filepath.Join(dir, "state.json.corrupt")
	data, err := os.ReadFile(quarantined) //nolint:gosec // reads a file this test just wrote under t.TempDir()
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", quarantined, err)
	}
	if string(data) != "not json" {
		t.Errorf("quarantined content = %q, want %q", data, "not json")
	}
}

func TestAutoEnabled(t *testing.T) {
	on := true
	off := false

	tests := []struct {
		name string
		s    State
		m    Method
		want bool
	}{
		{"script nil defaults on", State{}, MethodScript, true},
		{"script explicit on", State{Auto: &on}, MethodScript, true},
		{"script explicit off", State{Auto: &off}, MethodScript, false},
		{"homebrew nil never auto", State{}, MethodHomebrew, false},
		{"homebrew explicit on still false", State{Auto: &on}, MethodHomebrew, false},
		{"homebrew explicit off still false", State{Auto: &off}, MethodHomebrew, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.AutoEnabled(tt.m); got != tt.want {
				t.Errorf("AutoEnabled(%v) = %v, want %v", tt.m, got, tt.want)
			}
		})
	}
}
