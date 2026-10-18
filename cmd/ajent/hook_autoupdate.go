// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ajent-social/ajent-client/internal/cli"
	"github.com/ajent-social/ajent-client/internal/update"
	"github.com/ajent-social/ajent-client/internal/version"
)

// autoUpdateInterval is the minimum time between automatic-update spawns
// from the session-start hook (ADR 005: "at most once every 24 hours").
const autoUpdateInterval = 24 * time.Hour

func init() {
	sessionStartExtras = append(sessionStartExtras, autoUpdateSessionStart)
	autoRun = runAutoUpdate
}

// autoUpdateSessionStart is the sessionStartExtras entry that starts a
// detached "ajent update --auto-run" at most once every 24 hours for
// eligible script installs (docs/tasks/T6.6.md step 2). It never writes to
// stdout and ignores errors resolving the config directory or the running
// executable: a hook must never fail a coding tool's session.
func autoUpdateSessionStart(_ context.Context, env *cli.Env) {
	dir, err := env.Dir()
	if err != nil {
		return
	}
	exe, err := executablePath()
	if err != nil {
		return
	}

	if !autoUpdateDue(env, dir, exe) {
		return
	}

	// Record the rate limit before spawning, so a crash between here and
	// the detached process actually starting cannot cause a spawn storm on
	// every subsequent session start.
	st, err := update.LoadState(dir)
	if err != nil {
		return
	}
	st.LastAutoUpdate = env.Now()
	if err := update.SaveState(dir, st); err != nil {
		return
	}

	_ = startDetached(exe, []string{"update", "--auto-run"}, filepath.Join(dir, "update.log"))
}

// autoUpdateDue reports whether the session-start hook should spawn a
// detached "ajent update --auto-run" for the running exe under config
// directory dir (docs/tasks/T6.6.md step 1). It checks, in order, the
// AJENT_AUTO_UPDATE opt-out, CI/AJENT_NO_UPDATE_CHECK (update.CheckAllowed),
// whether this call is itself already running detached, that the running
// version is a real, non-snapshot release, that the install method is
// script, that automatic updates are enabled for it, and the 24-hour rate
// limit.
func autoUpdateDue(env *cli.Env, dir, exe string) bool {
	if env.Getenv("AJENT_AUTO_UPDATE") == "0" {
		return false
	}
	if !update.CheckAllowed(env.Getenv) {
		return false
	}
	if env.Getenv("AJENT_DETACHED") == "1" {
		return false
	}
	if !update.Valid(version.Version) || strings.Contains(version.Version, "SNAPSHOT") {
		return false
	}

	m := update.Detect(exe, dir)
	if m != update.MethodScript {
		return false
	}

	st, err := update.LoadState(dir)
	if err != nil {
		return false
	}
	if !st.AutoEnabled(m) {
		return false
	}

	return env.Now().Sub(st.LastAutoUpdate) >= autoUpdateInterval
}

// runAutoUpdate implements "ajent update --auto-run" (docs/tasks/T6.6.md
// step 3): it applies the newest verified stable release when automatic
// updates are enabled for this install, printing progress to stderr only.
// The detached launcher (autoUpdateSessionStart, via startDetached)
// redirects stderr to update.log; runAutoUpdate itself never writes to
// stdout.
func runAutoUpdate(ctx context.Context, env *cli.Env) error {
	dir, err := env.Dir()
	if err != nil {
		return err
	}
	exe, err := executablePath()
	if err != nil {
		return err
	}
	src := newSource(dir)

	st, err := update.LoadState(dir)
	if err != nil {
		return err
	}
	m := update.Detect(exe, dir)

	if !st.AutoEnabled(m) || env.Getenv("AJENT_AUTO_UPDATE") == "0" {
		_, _ = fmt.Fprintln(env.Stderr, "auto-run: disabled")
		return nil
	}

	// Automatic runs only ever apply stable releases (ADR 005: "Pre-releases
	// are never applied automatically").
	r, err := src.Latest(ctx, false)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "auto-run: failed: %v\n", err)
		return err
	}

	current := version.Version
	if !update.Newer(r.Version, current) {
		_, _ = fmt.Fprintf(env.Stderr, "auto-run: up to date %s\n", current)
		return nil
	}

	if err := update.Apply(ctx, src, r, exe); err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "auto-run: failed: %v\n", err)
		return err
	}

	_, _ = fmt.Fprintf(env.Stderr, "auto-run: updated %s -> %s\n", current, r.Version)

	st.Latest = r.Version
	return update.SaveState(dir, st)
}
