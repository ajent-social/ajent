// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ajent-social/ajent-client/internal/cli"
	"github.com/ajent-social/ajent-client/internal/update"
	"github.com/ajent-social/ajent-client/internal/version"
)

// osArgs is a seam over os.Args, so wireNotice's tests can choose which
// subcommand is running without touching the real process arguments
// (docs/tasks/T6.5.md step 2).
var osArgs = func() []string { return os.Args }

// noticeSkippedCommands never see an update notice or trigger a background
// refresh: mcp must keep stdout pure JSON-RPC, hook and update already
// manage update state themselves, post-upgrade and version run before or
// around an update, and help is not an interactive session
// (docs/tasks/T6.5.md step 2.1).
var noticeSkippedCommands = map[string]bool{
	"mcp":          true,
	"hook":         true,
	"update":       true,
	"post-upgrade": true,
	"version":      true,
	"help":         true,
	"watch":        true, // Local polling must not launch a network update checker.
}

func init() {
	wire = append(wire, wireNotice)
}

// wireNotice prints at most one update notice per day on stderr, and
// refreshes the cached release state through a detached background
// "ajent update --check" at most once per day, so no command waits on the
// network (ADR 005, "Notices"; docs/tasks/T6.5.md step 2). A notice must
// never break a command: every error here is silently ignored.
func wireNotice(env *cli.Env) {
	args := osArgs()
	if len(args) <= 1 || noticeSkippedCommands[args[1]] {
		return
	}
	if !update.CheckAllowed(env.Getenv) || env.Getenv("AJENT_DETACHED") == "1" {
		return
	}

	dir, err := env.Dir()
	if err != nil {
		return
	}
	st, err := update.LoadState(dir)
	if err != nil {
		return
	}

	exe, err := os.Executable()
	if err != nil {
		return
	}

	if update.NeedsRefresh(st, env.Now()) {
		st.LastCheck = env.Now()
		if err := update.SaveState(dir, st); err == nil {
			_ = startDetached(exe, []string{"update", "--check"}, filepath.Join(dir, "update.log"))
		}
	}

	if !env.IsTerminal(os.Stderr.Fd()) {
		return
	}
	method := update.Detect(exe, dir)
	text, show := update.Notice(st, version.Version, method, env.Now())
	if !show {
		return
	}
	if _, err := fmt.Fprintln(env.Stderr, text); err != nil {
		return
	}
	st.LastNotice = env.Now()
	_ = update.SaveState(dir, st)
}
