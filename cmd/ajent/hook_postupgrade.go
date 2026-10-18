// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ajent-social/ajent-client/internal/cli"
	"github.com/ajent-social/ajent-client/internal/config"
	"github.com/ajent-social/ajent-client/internal/integrations"
	"github.com/ajent-social/ajent-client/internal/update"
	"github.com/ajent-social/ajent-client/internal/version"
)

// postUpgradeLockTimeout bounds how long "ajent post-upgrade" waits for
// another concurrently started sync to finish before giving up
// (docs/tasks/T6.4.md step 3.1).
const postUpgradeLockTimeout = 2 * time.Second

// needsPostUpgrade reports whether the running version has not yet synced
// coding-tool integrations after an upgrade (docs/tasks/T6.4.md step 1). It
// is false for a development build (version.Version is "dev" or contains
// "SNAPSHOT"), when this installation has never set up integrations
// (configDir/integration-status.json is absent), and when the persisted
// state already recorded this version as seen.
func needsPostUpgrade(configDir string) bool {
	if version.Version == "dev" || strings.Contains(version.Version, "SNAPSHOT") {
		return false
	}

	if _, err := os.Stat(filepath.Join(configDir, "integration-status.json")); err != nil {
		return false
	}

	st, err := update.LoadState(configDir)
	if err != nil {
		return false
	}
	return st.SeenVersion != version.Version
}

func init() {
	sessionStartExtras = append(sessionStartExtras, hookPostUpgradeSessionStartExtra)

	cli.Register(cli.Command{
		Name:    "post-upgrade",
		Hidden:  true,
		Summary: "Re-sync coding-tool integrations after an upgrade",
		Run:     runPostUpgrade,
	})
}

// hookPostUpgradeSessionStartExtra is appended to sessionStartExtras
// (docs/tasks/T6.4.md step 2.1). When this project's integrations need a
// post-upgrade resync, it starts "ajent post-upgrade" as a detached
// background process logging to <configDir>/update.log, then returns
// immediately. It never writes to stdout, and every failure is ignored
// silently: the session-start hook must never fail a coding tool's
// session, and a missed sync simply retries at the next session start.
func hookPostUpgradeSessionStartExtra(_ context.Context, env *cli.Env) {
	dir, err := env.Dir()
	if err != nil {
		return
	}
	if !needsPostUpgrade(dir) {
		return
	}
	if env.Getenv("AJENT_DETACHED") == "1" {
		return
	}

	exe, err := os.Executable()
	if err != nil {
		return
	}

	_ = startDetached(exe, []string{"post-upgrade"}, filepath.Join(dir, "update.log"))
}

// runPostUpgrade implements the hidden "ajent post-upgrade" command
// (docs/tasks/T6.4.md step 3). It re-syncs every configured coding-tool
// integration under an on-disk lock, so concurrently started sessions
// converge on exactly one sync per version, then records the version as
// seen. It writes only to stderr; stdout stays empty so it is safe to run
// from the MCP or hook paths (docs/design.md, "Invariants").
func runPostUpgrade(ctx context.Context, env *cli.Env, _ []string) error {
	dir, err := env.Dir()
	if err != nil {
		return err
	}

	lock, err := config.Acquire(filepath.Join(dir, "upgrade.lock"), postUpgradeLockTimeout)
	if err != nil {
		if errors.Is(err, config.ErrLockTimeout) {
			_, _ = fmt.Fprintln(env.Stderr, "post-upgrade: another sync is running")
			return nil
		}
		return err
	}
	defer func() { _ = lock.Release() }()

	// Re-check under the lock: another session may have completed the sync
	// for this version while this one waited to acquire it, which keeps
	// the sync running exactly once even when many sessions start together.
	if !needsPostUpgrade(dir) {
		return nil
	}

	p, err := integrations.ParamsFromEnv(env.Home, dir, env.Getenv)
	if err != nil {
		return err
	}

	results, backup, err := integrations.Sync(ctx, p)
	if err != nil {
		return err
	}

	incomplete := false
	for _, r := range results {
		if r.Detail != "" {
			_, _ = fmt.Fprintf(env.Stderr, "%s: %s: %s\n", r.Name, r.Status, r.Detail)
		} else {
			_, _ = fmt.Fprintf(env.Stderr, "%s: %s\n", r.Name, r.Status)
		}
		if r.Status == "incomplete" {
			incomplete = true
		}
	}
	if incomplete {
		return errors.New("post-upgrade: incomplete; will retry at next session start")
	}

	st, err := update.LoadState(dir)
	if err != nil {
		return err
	}
	st.SeenVersion = version.Version
	if err := update.SaveState(dir, st); err != nil {
		return err
	}

	_, err = fmt.Fprintf(env.Stderr, "post-upgrade: synced %d integrations (backup %s)\n", len(results), backup.ID)
	return err
}
