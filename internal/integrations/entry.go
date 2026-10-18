// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
)

// MCPArgs returns the launch arguments for the ajent MCP server under the
// given harness profile.
func MCPArgs(profile string) []string {
	return []string{"mcp", "--profile", profile, "--project-profile"}
}

// EntryEnv returns the env block for an entry. It returns nil when
// p.ConfigDir is the default (filepath.Join(p.Home, ".aj")) and no
// credential file override is set; otherwise it returns AJENT_CONFIG_DIR
// (only when the dir is non-default) and AJENT_CREDENTIAL_FILE (only when
// set).
func EntryEnv(p Params) map[string]string {
	defaultDir := filepath.Join(p.Home, ".aj")
	nonDefaultDir := p.ConfigDir != defaultDir

	if !nonDefaultDir && p.CredentialFile == "" {
		return nil
	}

	env := map[string]string{}
	if nonDefaultDir {
		env["AJENT_CONFIG_DIR"] = p.ConfigDir
	}
	if p.CredentialFile != "" {
		env["AJENT_CREDENTIAL_FILE"] = p.CredentialFile
	}
	return env
}

// Entry builds the MCP server entry for profile: command is p.Binary, args
// is MCPArgs(profile), plus an "env" key when EntryEnv(p) is non-nil. Args
// and env use []any and map[string]any so the result compares equal to a
// decoded JSON value with reflect.DeepEqual.
func Entry(p Params, profile string) map[string]any {
	args := MCPArgs(profile)
	anyArgs := make([]any, len(args))
	for i, a := range args {
		anyArgs[i] = a
	}

	entry := map[string]any{
		"command": p.Binary,
		"args":    anyArgs,
	}

	if env := EntryEnv(p); env != nil {
		anyEnv := make(map[string]any, len(env))
		for k, v := range env {
			anyEnv[k] = v
		}
		entry["env"] = anyEnv
	}

	return entry
}

// Owned reports whether entry was written by ajent, including a Python-era
// install, and is therefore safe to replace or remove. Everything else is
// foreign.
func Owned(entry map[string]any) bool {
	command, _ := entry["command"].(string)
	args, _ := entry["args"].([]any)

	if baseNameIsAjent(command) {
		return true
	}

	for _, a := range args {
		s, ok := a.(string)
		if ok && filepath.Base(filepath.ToSlash(s)) == "client.py" {
			return true
		}
	}

	if isPythonCommand(command) && len(args) > 0 {
		first, ok := args[0].(string)
		if ok && strings.HasSuffix(filepath.ToSlash(first), "client.py") {
			return true
		}
	}

	return false
}

// baseNameIsAjent reports whether path's base name, with a trailing .exe
// trimmed case-insensitively, equals "ajent".
func baseNameIsAjent(path string) bool {
	base := filepath.Base(filepath.ToSlash(path))
	base = strings.TrimSuffix(strings.ToLower(base), ".exe")
	return base == "ajent"
}

// isPythonCommand reports whether command's base name is "python3" or
// "python", or starts with "python3.".
func isPythonCommand(command string) bool {
	base := filepath.Base(filepath.ToSlash(command))
	return base == "python3" || base == "python" || strings.HasPrefix(base, "python3.")
}

// SameEntry normalizes a and b through a JSON marshal and unmarshal, then
// compares the results with reflect.DeepEqual.
func SameEntry(a, b map[string]any) bool {
	na, aOK := normalizeEntry(a)
	nb, bOK := normalizeEntry(b)
	if !aOK || !bOK {
		return false
	}
	return reflect.DeepEqual(na, nb)
}

func normalizeEntry(m map[string]any) (map[string]any, bool) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, false
	}
	return out, true
}
