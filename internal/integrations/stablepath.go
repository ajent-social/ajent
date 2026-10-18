// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"os"
	"regexp"
	"runtime"
	"strings"
)

// scoopVersionPattern matches a Scoop app path segment, capturing the
// installed version so it can be swapped for "current".
var scoopVersionPattern = regexp.MustCompile(`/scoop/apps/ajent/([^/]+)/`)

// BinaryOverride is empty by default. Tests set it to pin the binary path:
// when non-empty, CurrentBinary returns it unchanged.
var BinaryOverride string

// StableBinary returns the non-versioned path to launch (ADR 007). Rules
// are evaluated in order on the slash-normalized executable path; the
// first matching rule whose stable path exists wins. executable is
// returned unchanged when no rule matches or its stable path does not
// exist. Path syntax is driven entirely by goos, not the host OS running
// this code, so the five rules are testable for every target platform
// from a single binary.
func StableBinary(executable string, goos string, getenv func(string) string, exists func(string) bool) string {
	slashed := toSlash(executable)

	if idx := strings.Index(slashed, "/Cellar/ajent/"); idx >= 0 {
		candidate := fromSlash(slashed[:idx]+"/bin/ajent", goos)
		if exists(candidate) {
			return candidate
		}
	}

	if idx := strings.Index(slashed, "/Caskroom/ajent/"); idx >= 0 {
		candidate := fromSlash(slashed[:idx]+"/bin/ajent", goos)
		if exists(candidate) {
			return candidate
		}
	}

	if containsFold(slashed, "/Microsoft/WinGet/Packages/") {
		if local := getenv("LOCALAPPDATA"); local != "" {
			candidate := joinNative(goos, strings.TrimRight(local, `/\`), "Microsoft", "WinGet", "Links", "ajent.exe")
			if exists(candidate) {
				return candidate
			}
		}
	}

	if loc := scoopVersionPattern.FindStringSubmatchIndex(slashed); loc != nil {
		version := slashed[loc[2]:loc[3]]
		if version != "current" {
			candidate := fromSlash(slashed[:loc[2]]+"current"+slashed[loc[3]:], goos)
			if exists(candidate) {
				return candidate
			}
		}
	}

	return executable
}

// CurrentBinary returns the stable absolute path to the running ajent
// executable (ADR 007). BinaryOverride, when set, short-circuits this.
func CurrentBinary() (string, error) {
	if BinaryOverride != "" {
		return BinaryOverride, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return StableBinary(exe, runtime.GOOS, os.Getenv, fileExists), nil
}

// ParamsFromEnv builds Params the way every caller (T4.6, T4.8, T4.9, T6.4)
// does: from the resolved home and config directories, the environment and
// CurrentBinary.
func ParamsFromEnv(home, configDir string, getenv func(string) string) (Params, error) {
	binary, err := CurrentBinary()
	if err != nil {
		return Params{}, err
	}
	return Params{
		Home:           home,
		Binary:         binary,
		ConfigDir:      configDir,
		CredentialFile: getenv("AJENT_CREDENTIAL_FILE"),
		GOOS:           runtime.GOOS,
		Getenv:         getenv,
	}, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// toSlash normalizes backslashes to forward slashes regardless of the host
// OS running this code (unlike path/filepath.ToSlash, which only replaces
// the host's own separator). StableBinary's rules match on this
// normalized form so a path written in any of the five conventions can be
// evaluated the same way on every host.
func toSlash(path string) string {
	return strings.ReplaceAll(path, `\`, "/")
}

// fromSlash converts a forward-slash path to goos's native separator.
func fromSlash(path, goos string) string {
	if goos == "windows" {
		return strings.ReplaceAll(path, "/", `\`)
	}
	return path
}

// joinNative joins parts with goos's native separator.
func joinNative(goos string, parts ...string) string {
	sep := "/"
	if goos == "windows" {
		sep = `\`
	}
	return strings.Join(parts, sep)
}
