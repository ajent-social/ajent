// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ajent-social/ajent"
	"github.com/ajent-social/ajent/internal/config"
)

// managedMarker identifies a SKILL.md that ajent installed and may refresh.
// It must match the marker line in plugins/ajent/skills/network/SKILL.md.
const managedMarker = "<!-- Managed by ajent. ajent integrations sync overwrites local edits. -->"

// pythonEraSkillPrefix is the frontmatter that opened the Python client's
// bundled skill file. A file starting with this prefix and mentioning
// client.py is a Python-era install that T4.9 migration relies on ajent
// detecting and replacing here.
const pythonEraSkillPrefix = "---\nname: network\n"

// errUnsafeSkillDestination is returned by safeDestination when the shared
// skills directory is not private to the current user.
var errUnsafeSkillDestination = errors.New("integrations: shared skill destination is not private to you; left unchanged")

// agentSkillsWriter installs the embedded Ajent network skill at
// ~/.agents/skills/ajent-network/SKILL.md, the directory Codex and other
// compatible tools scan for user skills. It never touches Claude Code's own
// skill loading; the plugin carries that skill (T4.1, T4.4).
type agentSkillsWriter struct{}

func init() {
	Register(agentSkillsWriter{})
}

// Target identifies this writer. Profile is empty: the shared skill is not
// scoped to one coding tool.
func (agentSkillsWriter) Target() Target {
	return Target{Name: "agentskills", Profile: ""}
}

// skillPath returns the absolute path of the shared skill file under p.Home.
func skillPath(p Params) string {
	return filepath.Join(p.Home, ".agents", "skills", "ajent-network", "SKILL.md")
}

// Detect always returns true: the shared skill directory is not tied to the
// presence of any particular coding tool.
func (agentSkillsWriter) Detect(Params) bool {
	return true
}

// Paths returns the single file this writer may modify.
func (agentSkillsWriter) Paths(p Params) []string {
	return []string{skillPath(p)}
}

// skillContent loads the embedded SKILL.md ajent ships in its plugin.
func skillContent() ([]byte, error) {
	data, err := fs.ReadFile(ajent.Plugins, "plugins/ajent/skills/network/SKILL.md")
	if err != nil {
		return nil, fmt.Errorf("integrations: read embedded skill: %w", err)
	}
	return data, nil
}

// managed reports whether existing is a SKILL.md ajent may refresh: either
// it carries the managed marker, or it is the Python-era copy of this same
// skill (identified by its frontmatter and its reference to client.py).
func managed(existing []byte) bool {
	if bytes.Contains(existing, []byte(managedMarker)) {
		return true
	}
	prefix := []byte(pythonEraSkillPrefix)
	hasLegacyHeader := bytes.HasPrefix(existing, prefix) ||
		bytes.HasPrefix(existing, bytes.ReplaceAll(prefix, []byte("\n"), []byte("\r\n")))
	if hasLegacyHeader && bytes.Contains(existing, []byte("client.py")) {
		return true
	}
	return false
}

// safeDestination rejects a skill destination that is not private to the
// current user:
//   - skillPath(p), or any of ajent-network, skills or .agents under
//     p.Home, is a symlink.
//   - on unix, any existing one of those directories or the file is not
//     owned by the current user, or is writable by group or other
//     (checkPrivateOwnership, agentskills_unix.go / agentskills_windows.go).
func safeDestination(p Params) error {
	agentsDir := filepath.Join(p.Home, ".agents")
	skillsDir := filepath.Join(agentsDir, "skills")
	networkDir := filepath.Join(skillsDir, "ajent-network")
	file := skillPath(p)

	for _, path := range []string{agentsDir, skillsDir, networkDir, file} {
		info, err := os.Lstat(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("integrations: lstat %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return config.ErrSymlink
		}
		if err := checkPrivateOwnership(path, info); err != nil {
			return err
		}
	}
	return nil
}

// Apply installs or refreshes the shared skill file. It never overwrites a
// file the user wrote themselves.
func (agentSkillsWriter) Apply(_ context.Context, p Params) (Result, error) {
	name := "agentskills"

	if err := safeDestination(p); err != nil {
		return Result{Name: name, Status: "incomplete", Detail: err.Error()}, nil
	}

	content, err := skillContent()
	if err != nil {
		return Result{}, err
	}

	path := skillPath(p)
	existing, err := os.ReadFile(path) //nolint:gosec // G304: path is derived from Params.Home under the caller's control; safeDestination refused a symlink above.
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if mkErr := os.MkdirAll(filepath.Dir(path), 0o700); mkErr != nil {
			return Result{}, fmt.Errorf("integrations: mkdir %s: %w", filepath.Dir(path), mkErr)
		}
		if wErr := config.WritePrivate(path, content); wErr != nil {
			return Result{}, wErr
		}
		return Result{Name: name, Status: "configured"}, nil
	case err != nil:
		return Result{}, fmt.Errorf("integrations: read %s: %w", path, err)
	}

	if bytes.Equal(existing, content) {
		return Result{Name: name, Status: "unchanged"}, nil
	}

	if !managed(existing) {
		return Result{Name: name, Status: "unchanged", Detail: "existing skill preserved"}, nil
	}

	if wErr := config.WritePrivate(path, content); wErr != nil {
		return Result{}, wErr
	}
	return Result{Name: name, Status: "configured"}, nil
}

// Remove deletes the shared skill file only when it is ajent-managed, and
// then removes the ajent-network directory if that leaves it empty. A
// missing, foreign or symlinked file is left unchanged.
func (agentSkillsWriter) Remove(_ context.Context, p Params) (Result, error) {
	name := "agentskills"
	path := skillPath(p)

	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Result{Name: name, Status: "unchanged"}, nil
		}
		return Result{}, fmt.Errorf("integrations: lstat %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Result{Name: name, Status: "unchanged"}, nil
	}

	existing, err := os.ReadFile(path) //nolint:gosec // G304: path is derived from Params.Home under the caller's control; the Lstat above confirmed it is not a symlink.
	if err != nil {
		return Result{}, fmt.Errorf("integrations: read %s: %w", path, err)
	}
	if !managed(existing) {
		return Result{Name: name, Status: "unchanged"}, nil
	}

	if err := os.Remove(path); err != nil {
		return Result{}, fmt.Errorf("integrations: remove %s: %w", path, err)
	}
	removeSkillDirIfEmpty(filepath.Dir(path))

	return Result{Name: name, Status: "removed"}, nil
}

// removeSkillDirIfEmpty removes dir (ajent-network/) when it has no
// remaining entries. It is best-effort: a failure here does not change the
// Remove result, since the managed file is already gone.
func removeSkillDirIfEmpty(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		return
	}
	_ = os.Remove(dir)
}
