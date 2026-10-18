// SPDX-License-Identifier: Apache-2.0

// Package integrations provides the harness integration machinery shared by
// every writer: the Writer registry, an exec seam for tests, the canonical
// MCP entry builder, an order-preserving JSON merge and stable binary path
// resolution (ADR 007).
package integrations

import (
	"context"
	"fmt"
	"sort"
)

// Target identifies the coding harness a Writer configures.
type Target struct {
	Name    string // "claude", "codex", "cursor", "gemini", "copilot", "vscode", "agentskills"
	Profile string // "claude-code", "codex", "cursor", "gemini-cli", "copilot", "copilot-vscode"; "" for agentskills
}

// Result reports the outcome of a Writer.Apply or Writer.Remove call.
type Result struct {
	Name   string `json:"name"`
	Status string `json:"status"` // configured | unchanged | skipped | incomplete | removed
	Detail string `json:"detail,omitempty"`
}

// Params carries the values a Writer needs to detect and configure a
// harness. Callers build it with ParamsFromEnv.
type Params struct {
	Home           string              // user home directory
	Binary         string              // stable absolute path to launch (StableBinary result)
	ConfigDir      string              // ajent config dir actually in use
	CredentialFile string              // "" when AJENT_CREDENTIAL_FILE is unset
	GOOS           string              // runtime.GOOS in production, overridden in tests
	Getenv         func(string) string // for APPDATA, XDG_CONFIG_HOME, LOCALAPPDATA
}

// Writer configures or removes ajent from one coding harness.
type Writer interface {
	Target() Target
	Detect(p Params) bool
	Paths(p Params) []string // absolute files this writer may modify (for backups)
	Apply(ctx context.Context, p Params) (Result, error)
	Remove(ctx context.Context, p Params) (Result, error)
}

// writerOrder is the fixed Writers() ordering. Names not listed here sort
// after all of these, alphabetically.
var writerOrder = map[string]int{
	"claude":      0,
	"codex":       1,
	"cursor":      2,
	"gemini":      3,
	"copilot":     4,
	"vscode":      5,
	"agentskills": 6,
}

var registry []Writer

// Register adds a writer to the registry. Call it from init() in the
// writer's own file. It panics if a writer with the same Target().Name is
// already registered.
func Register(w Writer) {
	name := w.Target().Name
	for _, existing := range registry {
		if existing.Target().Name == name {
			panic(fmt.Sprintf("integrations: duplicate writer %q", name))
		}
	}
	registry = append(registry, w)
}

// Writers returns the registered writers, sorted by the fixed order claude,
// codex, cursor, gemini, copilot, vscode, agentskills, then unknown names
// last, alphabetically.
func Writers() []Writer {
	out := make([]Writer, len(registry))
	copy(out, registry)
	sort.SliceStable(out, func(i, j int) bool {
		ni, nj := out[i].Target().Name, out[j].Target().Name
		ri, oki := writerOrder[ni]
		rj, okj := writerOrder[nj]
		switch {
		case oki && okj:
			return ri < rj
		case oki && !okj:
			return true
		case !oki && okj:
			return false
		default:
			return ni < nj
		}
	})
	return out
}

// ResetForTest swaps in an empty registry and returns a function that
// restores the previous one. Tests in other packages use it as:
//
//	defer integrations.ResetForTest()()
func ResetForTest() (restore func()) {
	prev := registry
	registry = nil
	return func() {
		registry = prev
	}
}
