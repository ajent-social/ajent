// SPDX-License-Identifier: Apache-2.0

// Package profiles resolves the credential for a tool profile: reusing an
// already-enrolled profile, lazily enrolling a new one with the
// installation's root credential exactly once even under heavy
// concurrency, re-syncing scope settings when they change, and falling
// back to private-only collaboration when a domain grant is refused
// (docs/design.md; docs/contract.md "Scope settings" and "Enrollment").
package profiles

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/ajent-social/ajent-client/internal/config"
	"github.com/ajent-social/ajent-client/internal/credentials"
)

// Settings is the enrollment scope payload sent to POST /v1/fleet
// (docs/contract.md "Scope settings").
type Settings struct {
	Scope       string   `json:"scope"`
	Grants      []string `json:"grants"`
	Domain      string   `json:"domain"`
	PrivateOnly bool     `json:"private_only"`
}

// errProfilesPrivateOnlyNotBool is returned when a settings file's
// "private_only" key is present but not a JSON boolean.
var errProfilesPrivateOnlyNotBool = errors.New("profiles: private_only must be a boolean")

// errProfilesUnapprovedDomain is returned when the selected domain is not
// among the grants the installation owner already approved
// (docs/contract.md "Scope settings"). Its text is the exact,
// CLI-user-facing sentence docs/tasks/T3.6.md mandates verbatim, not
// internal diagnostic wrapping text, so it is capitalized and punctuated
// like the sentence it is meant to print as-is.
//
//nolint:revive,staticcheck // error-strings/ST1005: this sentence is a mandated user-facing message, not a wrapped diagnostic.
var errProfilesUnapprovedDomain = errors.New("This project selects an unapproved domain. Run ajent configure --scope project, or select private mode.")

// profilesScopeGrant is one entry of dir/scope-grants.json[identity][key]:
// the shape of a /v1/domain-grants response, trimmed to what ScopeSettings
// needs.
type profilesScopeGrant struct {
	Domain string `json:"domain"`
	ID     string `json:"id"`
}

// ScopeSettings merges user settings (dir/config.json) and project
// settings (projectRoot/.aj/config.json) with the grants the installation
// owner already approved (dir/scope-grants.json), and returns the payload
// to send with an enrollment sync (docs/contract.md "Scope settings"). The
// second return value is false when neither settings file has content:
// legacy manual installs predate scope configuration and send no settings
// at all.
func ScopeSettings(dir, projectRoot string, root credentials.Credential) (Settings, bool, error) {
	user, err := readProfilesSettingsFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return Settings{}, false, err
	}
	project, err := readProfilesSettingsFile(filepath.Join(projectRoot, ".aj", "config.json"))
	if err != nil {
		return Settings{}, false, err
	}

	if len(user) == 0 && len(project) == 0 {
		return Settings{}, false, nil
	}

	selected := project
	if len(selected) == 0 {
		selected = user
	}

	private := false
	if raw, ok := selected["private_only"]; ok {
		b, ok := raw.(bool)
		if !ok {
			return Settings{}, false, errProfilesPrivateOnlyNotBool
		}
		private = b
	}

	scope := credentials.ProjectScope(projectRoot)

	// Grants come only from dir/scope-grants.json, under this
	// installation's identity: a project settings file can select among
	// grants the user already approved but can never add one
	// (docs/adr/006-client-trust-boundaries.md "Repository content grants
	// nothing").
	allowed := map[string]string{}
	if !private {
		grants, err := readProfilesScopeGrants(filepath.Join(dir, "scope-grants.json"))
		if err != nil {
			return Settings{}, false, err
		}
		identityGrants := grants[root.Identity()]
		for _, key := range [2]string{"user", scope} {
			for _, g := range identityGrants[key] {
				allowed[g.Domain] = g.ID
			}
		}
	}

	domain, _ := selected["domain"].(string)
	switch {
	case private:
		domain = ""
	case domain != "":
		if _, ok := allowed[domain]; !ok {
			return Settings{}, false, errProfilesUnapprovedDomain
		}
	}

	ids := make(map[string]struct{}, len(allowed))
	for _, id := range allowed {
		ids[id] = struct{}{}
	}
	grantIDs := make([]string, 0, len(ids))
	for id := range ids {
		grantIDs = append(grantIDs, id)
	}
	sort.Strings(grantIDs)

	return Settings{
		Scope:       scope,
		Grants:      grantIDs,
		Domain:      domain,
		PrivateOnly: private,
	}, true, nil
}

// readProfilesSettingsFile reads path (a user or project settings file)
// with config.ReadJSON, refusing a symlinked parent directory in addition
// to the symlink refusal config.ReadJSON already applies to path itself
// (docs/tasks/T3.6.md: "Refuse symlinks, including a symlinked .aj
// directory"). A missing file returns a nil map and no error.
func readProfilesSettingsFile(path string) (map[string]any, error) {
	dir := filepath.Dir(path)
	if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 { //nolint:gosec // G703: dir is derived from a caller-supplied config-tree path (docs/interfaces.md: "functions that touch the filesystem take the config directory as a parameter"); callers, not attackers, control it.
		return nil, fmt.Errorf("profiles: %s: %w", dir, config.ErrSymlink)
	}

	var v any
	if err := config.ReadJSON(path, &v); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("profiles: reading %s: %w", path, err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("profiles: %s: not a JSON object", path)
	}
	return m, nil
}

// readProfilesScopeGrants reads dir/scope-grants.json. A missing file
// returns a nil map and no error, which indexes and ranges safely as
// empty.
func readProfilesScopeGrants(path string) (map[string]map[string][]profilesScopeGrant, error) {
	var v map[string]map[string][]profilesScopeGrant
	if err := config.ReadJSON(path, &v); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("profiles: reading %s: %w", path, err)
	}
	return v, nil
}
