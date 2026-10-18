// SPDX-License-Identifier: Apache-2.0

// Package update classifies how the running ajent binary was installed and
// applies verified upgrades for installs that manage themselves.
package update

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Method identifies how the running binary reached the machine.
type Method string

// Recognized install methods.
const (
	MethodScript    Method = "script"
	MethodHomebrew  Method = "homebrew"
	MethodDpkg      Method = "dpkg"
	MethodRPM       Method = "rpm"
	MethodScoop     Method = "scoop"
	MethodWinget    Method = "winget"
	MethodGoInstall Method = "go-install"
	MethodUnknown   Method = "unknown"
)

// Seams so tests can override host interaction without touching the real
// filesystem, environment or OS.
var (
	goos         = runtime.GOOS
	dpkgInfoDir  = "/var/lib/dpkg/info"
	runCmd       = func(name string, args ...string) ([]byte, error) { return exec.Command(name, args...).Output() } //nolint:gosec // G204: test-injection seam (see comment above); the only production call site, rpmOwns, invokes it with hardcoded literal arguments, never external input.
	getenv       = os.Getenv
	evalSymlinks = filepath.EvalSymlinks
)

// installRecord is the subset of the install.json fields (written by the
// install scripts, docs/tasks/T5.9.md step 2.9) that detection needs.
type installRecord struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// Detect classifies how executable was installed, consulting configDir's
// install.json for script installs.
func Detect(executable, configDir string) Method {
	exe, err := evalSymlinks(executable)
	if err != nil {
		exe = executable
	}
	exe = filepath.Clean(exe)

	if matchesScript(configDir, exe) {
		return MethodScript
	}

	p := toSlashLower(exe)

	if strings.Contains(p, "/cellar/ajent/") || strings.Contains(p, "/caskroom/ajent/") {
		return MethodHomebrew
	}

	if goos == "linux" {
		if dpkgOwns(p) {
			return MethodDpkg
		}
		if rpmOwns(p) {
			return MethodRPM
		}
	}

	if goos == "windows" {
		if strings.Contains(p, "/scoop/apps/ajent/") {
			return MethodScoop
		}
		if strings.Contains(p, "/scoop/shims/") && strings.HasSuffix(p, "/ajent.exe") {
			return MethodScoop
		}
		if strings.Contains(p, "/microsoft/winget/packages/ajentsocial.ajent") || strings.Contains(p, "/microsoft/winget/links/") {
			return MethodWinget
		}
	}

	if gobin := goBin(); gobin != "" && filepath.Dir(exe) == gobin {
		return MethodGoInstall
	}

	return MethodUnknown
}

// matchesScript reports whether configDir/install.json records a script
// install whose path resolves to exe.
func matchesScript(configDir, exe string) bool {
	data, err := os.ReadFile(filepath.Join(configDir, "install.json")) //nolint:gosec // G304: configDir is Detect's caller-supplied config directory parameter; not attacker-controlled input.
	if err != nil {
		return false
	}

	var rec installRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return false
	}
	if rec.Method != "script" {
		return false
	}

	p, err := evalSymlinks(rec.Path)
	if err != nil {
		p = rec.Path
	}
	p = filepath.Clean(p)

	if goos == "windows" {
		return strings.EqualFold(p, exe)
	}
	return p == exe
}

// dpkgOwns reports whether dpkg's ajent.list claims exe (linux only). p is
// exe already normalized by toSlashLower.
func dpkgOwns(p string) bool {
	if p != "/usr/bin/ajent" {
		return false
	}

	data, err := os.ReadFile(filepath.Join(dpkgInfoDir, "ajent.list"))
	if err != nil {
		return false
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimRight(line, "\r") == "/usr/bin/ajent" {
			return true
		}
	}
	return false
}

// rpmOwns reports whether rpm claims exe (linux only). p is exe already
// normalized by toSlashLower.
func rpmOwns(p string) bool {
	if p != "/usr/bin/ajent" {
		return false
	}

	out, err := runCmd("rpm", "-qf", "--queryformat", "%{NAME}", "/usr/bin/ajent")
	if err != nil {
		return false
	}
	return string(out) == "ajent"
}

// goBin returns the directory `go install` places binaries in, or "" when it
// cannot be determined.
func goBin() string {
	if gobin := getenv("GOBIN"); gobin != "" {
		return gobin
	}
	if gopath := getenv("GOPATH"); gopath != "" {
		return filepath.Join(gopath, "bin")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "go", "bin")
	}
	return ""
}

// toSlashLower lowercases exe and converts backslashes to forward slashes so
// Windows-style paths can be matched with plain substrings regardless of the
// host this code runs on.
func toSlashLower(exe string) string {
	return strings.ToLower(strings.ReplaceAll(exe, `\`, "/"))
}

// UpgradeCommand returns the shell command that upgrades a binary installed
// with m, or "" when m manages itself (script) or is unknown.
func UpgradeCommand(m Method) string {
	switch m {
	case MethodHomebrew:
		return "brew upgrade ajent"
	case MethodDpkg:
		return "sudo apt update && sudo apt install --only-upgrade ajent"
	case MethodRPM:
		return "sudo dnf upgrade ajent"
	case MethodScoop:
		return "scoop update ajent"
	case MethodWinget:
		return "winget upgrade --id AjentSocial.Ajent --exact"
	case MethodGoInstall:
		return "go install github.com/ajent-social/ajent/cmd/ajent@latest"
	default:
		return ""
	}
}

// Managed reports whether a package manager owns upgrades for m.
func (m Method) Managed() bool {
	switch m {
	case MethodHomebrew, MethodDpkg, MethodRPM, MethodScoop, MethodWinget, MethodGoInstall:
		return true
	default:
		return false
	}
}

// Label returns a human-readable name for m, suitable for CLI messages.
func (m Method) Label() string {
	switch m {
	case MethodHomebrew:
		return "Homebrew"
	case MethodDpkg:
		return "APT"
	case MethodRPM:
		return "DNF"
	case MethodScoop:
		return "Scoop"
	case MethodWinget:
		return "winget"
	case MethodGoInstall:
		return "go install"
	case MethodScript:
		return "the install script"
	default:
		return "an unknown method"
	}
}
