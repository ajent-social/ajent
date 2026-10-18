// SPDX-License-Identifier: Apache-2.0

// Package ajent exposes files embedded into the ajent binary.
package ajent

import "embed"

// Plugins holds the Claude Code and Codex plugin shipped with ajent.
//
//go:embed all:plugins
var Plugins embed.FS
