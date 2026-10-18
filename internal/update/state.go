// SPDX-License-Identifier: Apache-2.0

package update

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/ajent-social/ajent-client/internal/config"
)

// State is the persisted record of ajent's update checks and preferences,
// stored as configDir/state.json.
type State struct {
	LastCheck      time.Time `json:"last_check"`
	LastNotice     time.Time `json:"last_notice"`
	LastAutoUpdate time.Time `json:"last_auto_update"`
	SeenVersion    string    `json:"seen_version"`
	// Auto records an explicit `ajent update --auto on|off` choice. nil
	// means the user has not chosen, and the per-method default applies.
	Auto   *bool  `json:"auto,omitempty"`
	Latest string `json:"latest,omitempty"`
}

// statePath returns configDir/state.json.
func statePath(configDir string) string {
	return filepath.Join(configDir, "state.json")
}

// LoadState reads configDir/state.json. A missing file returns the zero
// State and a nil error. A file that fails to parse as State is renamed to
// state.json.corrupt and LoadState returns the zero State and a nil error,
// so a corrupt cache never blocks the caller.
func LoadState(configDir string) (State, error) {
	path := statePath(configDir)

	var s State
	err := config.ReadJSON(path, &s)
	if err == nil {
		return s, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}

	// Any other failure (malformed JSON, wrong shape, ...) is treated as
	// corruption: quarantine the file and start fresh rather than fail.
	_ = os.Rename(path, path+".corrupt")
	return State{}, nil
}

// SaveState writes s to configDir/state.json with config.WriteJSON, which
// gives the file mode 0600 and an atomic write.
func SaveState(configDir string, s State) error {
	return config.WriteJSON(statePath(configDir), s)
}

// AutoEnabled reports whether automatic updates are enabled for install
// method m, given state s. Only script installs support automatic updates:
// they default to enabled unless the user set Auto explicitly. Every other
// method always reports false, since it never applies automatically
// (ADR 005).
func (s State) AutoEnabled(m Method) bool {
	if m != MethodScript {
		return false
	}
	if s.Auto != nil {
		return *s.Auto
	}
	return true
}
