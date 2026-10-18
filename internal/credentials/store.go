// SPDX-License-Identifier: Apache-2.0

package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ajent-social/ajent/internal/config"
)

// defaultOrigin is the default Ajent server origin used to build a
// Credential from AJENT_TOKEN (a Go addition, docs/contract.md section 4)
// when AJENT_URL is unset. It duplicates api.DefaultOrigin: this package
// must not import internal/api, since that package depends on this one.
const defaultOrigin = "https://ajent.social"

// RootPath returns the path to the root credential file: the value of
// AJENT_CREDENTIAL_FILE, with a leading "~" expanded to the current
// user's home directory, if that variable is set; otherwise
// dir/credential.json.
func RootPath(dir string) string {
	if envPath := os.Getenv("AJENT_CREDENTIAL_FILE"); envPath != "" {
		return expandTilde(envPath)
	}
	return filepath.Join(dir, "credential.json")
}

// expandTilde expands a leading "~" (a bare "~", or "~" followed by the OS
// path separator) to the current user's home directory. Any other path,
// or any error resolving the home directory, is returned unchanged.
func expandTilde(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

// LoadRoot loads the root credential for dir.
//
// AJENT_TOKEN is a Go addition absent from the Python client
// (docs/contract.md section 4): when it is non-empty, LoadRoot returns a
// Credential built from it and AJENT_URL (or defaultOrigin when that is
// unset) without touching the filesystem. Otherwise it reads
// RootPath(dir); a missing file returns ErrNoCredential, and any other
// read or decode error is wrapped and returned.
func LoadRoot(dir string) (Credential, error) {
	if token := os.Getenv("AJENT_TOKEN"); token != "" {
		origin := os.Getenv("AJENT_URL")
		if origin == "" {
			origin = defaultOrigin
		}
		return Credential{Server: origin, Token: token}, nil
	}

	var c Credential
	if err := config.ReadJSON(RootPath(dir), &c); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Credential{}, ErrNoCredential
		}
		return Credential{}, fmt.Errorf("credentials: loading root credential: %w", err)
	}
	return c, nil
}

// SaveRoot writes c to RootPath(dir), creating the config directory with
// private permissions first.
func SaveRoot(dir string, c Credential) error {
	path := RootPath(dir)
	if err := config.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	return config.WriteJSON(path, c)
}

// CreateRoot creates RootPath(dir) exclusively with c, creating the config
// directory with private permissions first. When the file already exists,
// the returned error satisfies errors.Is(err, fs.ErrExist).
func CreateRoot(dir string, c Credential) error {
	path := RootPath(dir)
	if err := config.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("credentials: marshaling root credential: %w", err)
	}
	return config.CreateExclusive(path, data)
}

// ProfileDir returns the directory holding root's profile credential and
// lock files: dir/profiles/<root.Identity()>.
func ProfileDir(dir string, root Credential) string {
	return filepath.Join(dir, "profiles", root.Identity())
}

// ProfilePath returns the path of profile's credential file within
// ProfileDir(dir, root).
func ProfilePath(dir string, root Credential, profile string) string {
	return filepath.Join(ProfileDir(dir, root), profile+".json")
}

// ProfileLockPath returns the path of profile's lock file within
// ProfileDir(dir, root).
func ProfileLockPath(dir string, root Credential, profile string) string {
	return filepath.Join(ProfileDir(dir, root), profile+".lock")
}

// LoadProfile loads profile's credential file for root from dir.
//
// It returns ErrInvalidProfile when profile fails ValidProfile. A missing
// file returns (Credential{}, false, nil): callers other than the lazy
// enrollment path (T3.6) treat that as "not yet enrolled". A symlinked
// profile file returns an error wrapping config.ErrSymlink. Otherwise the
// file is decoded and returned with ok == true, including when the
// credential is pending; callers decide what to do with a pending
// credential.
func LoadProfile(dir string, root Credential, profile string) (Credential, bool, error) {
	if !ValidProfile(profile) {
		return Credential{}, false, ErrInvalidProfile
	}

	var c Credential
	if err := config.ReadJSON(ProfilePath(dir, root, profile), &c); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Credential{}, false, nil
		}
		return Credential{}, false, fmt.Errorf("credentials: loading profile %s: %w", profile, err)
	}
	return c, true, nil
}

// SaveProfile validates profile and writes c to its credential file for
// root in dir, creating the profile directory with private permissions
// first.
func SaveProfile(dir string, root Credential, profile string, c Credential) error {
	if !ValidProfile(profile) {
		return ErrInvalidProfile
	}
	if err := config.EnsurePrivateDir(ProfileDir(dir, root)); err != nil {
		return err
	}
	return config.WriteJSON(ProfilePath(dir, root, profile), c)
}
