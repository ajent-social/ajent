// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ajent-social/ajent-client/internal/cli"
	"github.com/ajent-social/ajent-client/internal/config"
	"github.com/ajent-social/ajent-client/internal/integrations"
)

func init() {
	cli.Register(cli.Command{
		Name:    "uninstall",
		Summary: "Remove ajent from your coding tools",
		Usage:   "ajent uninstall [--purge] [--yes]",
		Flags:   uninstallFlags,
		Run:     runUninstall,
	})
}

// uninstallExampleFile is the copyable MCP entry that integrations.Sync
// writes and that uninstall deletes alongside every ajent-owned
// integration entry (docs/tasks/T4.8.md step 5).
const uninstallExampleFile = "mcp-example.json"

// uninstallNewFlagSet defines uninstall's flags: --purge and --yes
// (docs/tasks/T4.8.md step 1).
func uninstallNewFlagSet() (flagSet *flag.FlagSet, purge, yes *bool) {
	flagSet = flag.NewFlagSet("uninstall", flag.ContinueOnError)
	purge = flagSet.Bool("purge", false, "also delete local credentials")
	yes = flagSet.Bool("yes", false, "do not ask for confirmation")
	return flagSet, purge, yes
}

// uninstallFlags returns a fresh FlagSet with uninstall's flag
// definitions, used by "ajent help uninstall".
func uninstallFlags() *flag.FlagSet {
	flagSet, _, _ := uninstallNewFlagSet()
	return flagSet
}

// runUninstall implements "ajent uninstall [--purge] [--yes]"
// (docs/tasks/T4.8.md). It removes every ajent-owned integration entry
// from each coding tool and deletes mcp-example.json, snapshotting
// everything first so the removal can be rolled back. Credentials stay
// unless --purge is given. It never removes the ajent binary itself.
func runUninstall(ctx context.Context, env *cli.Env, args []string) error {
	flagSet, purge, yes := uninstallNewFlagSet()
	flagSet.SetOutput(env.Stderr)
	if err := flagSet.Parse(args); err != nil {
		return err
	}

	p, err := paramsFromEnv(env)
	if err != nil {
		return err
	}

	backup, err := integrations.Snapshot(p.ConfigDir, uninstallBackupPaths(p))
	if err != nil {
		return err
	}

	var results []integrations.Result
	for _, w := range integrations.Writers() {
		if !w.Detect(p) {
			continue
		}
		res, removeErr := w.Remove(ctx, p)
		if removeErr != nil {
			results = append(results, integrations.Result{Name: w.Target().Name, Status: "incomplete", Detail: removeErr.Error()})
			continue
		}
		results = append(results, res)
	}
	integrationsPrintResults(env, results)

	examplePath := filepath.Join(p.ConfigDir, uninstallExampleFile)
	if err := os.Remove(examplePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("uninstall: removing %s: %w", examplePath, err)
	}
	if _, err := fmt.Fprintf(env.Stdout, "Integration backup: %s. Roll back with: ajent rollback %s\n", backup.ID, backup.ID); err != nil {
		return err
	}

	if *purge {
		if _, err := fmt.Fprintln(env.Stdout, "To revoke this installation on the server, sign in at https://ajent.social and revoke it from your dashboard."); err != nil {
			return err
		}

		if !*yes {
			if !env.IsTerminal(os.Stdin.Fd()) {
				return fmt.Errorf("%w: --purge needs --yes when not interactive", cli.ErrUsage)
			}

			if _, err := fmt.Fprintf(env.Stdout, "Delete local ajent credentials in %s? [y/N] ", p.ConfigDir); err != nil {
				return err
			}
			confirmed, err := uninstallReadYesNo(env.Stdin)
			if err != nil {
				return err
			}
			if !confirmed {
				_, err := fmt.Fprintln(env.Stdout, "Credentials kept.")
				return err
			}
		}

		if err := uninstallPurgeConfigDir(p.ConfigDir, env.Home); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(env.Stdout, "Local credentials deleted. Backups kept in %s/backups.\n", p.ConfigDir); err != nil {
			return err
		}
	}

	binary, err := integrations.CurrentBinary()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(env.Stdout, "Remove the ajent program with the tool you installed it with, or delete %s.\n", binary)
	return err
}

// uninstallBackupPaths returns the union of every registered writer's
// Paths, detected or not, plus <ConfigDir>/mcp-example.json
// (docs/tasks/T4.8.md step 3). Unlike integrations.Sync and "integrations
// remove", which snapshot only detected writers, uninstall snapshots every
// writer's targets so rolling back restores a tool even if it is not
// currently detected.
func uninstallBackupPaths(p integrations.Params) []string {
	var paths []string
	for _, w := range integrations.Writers() {
		paths = append(paths, w.Paths(p)...)
	}
	return append(paths, filepath.Join(p.ConfigDir, uninstallExampleFile))
}

// uninstallReadYesNo reads one line from r and reports whether it is "y"
// or "yes", case-insensitively, after trimming surrounding whitespace
// (docs/tasks/T4.8.md step 6.2). Reaching EOF without a newline is treated
// like any other non-affirmative answer.
func uninstallReadYesNo(r io.Reader) (bool, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("uninstall: reading answer: %w", err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

// uninstallPurgeConfigDir deletes every entry directly under configDir
// except "backups" (docs/tasks/T4.8.md step 6.3). It refuses when
// configDir is a symlink or equals home, so a misconfigured
// AJENT_CONFIG_DIR can never turn --purge into deleting a user's home
// directory.
func uninstallPurgeConfigDir(configDir, home string) error {
	if err := uninstallPurgeGuard(configDir, home); err != nil {
		return err
	}

	info, err := os.Lstat(configDir)
	if err != nil {
		return fmt.Errorf("uninstall: %s: %w", configDir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("uninstall: %s: %w", configDir, config.ErrSymlink)
	}

	entries, err := os.ReadDir(configDir)
	if err != nil {
		return fmt.Errorf("uninstall: reading %s: %w", configDir, err)
	}
	for _, e := range entries {
		if e.Name() == "backups" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(configDir, e.Name())); err != nil {
			return fmt.Errorf("uninstall: removing %s: %w", e.Name(), err)
		}
	}
	return nil
}

// uninstallMarkers are entries only an ajent config directory holds. Purge
// requires at least one, so a mistyped AJENT_CONFIG_DIR never empties an
// unrelated directory.
var uninstallMarkers = []string{"credential.json", "profiles", "config.json", "install.json", "integration-status.json", "feedback", "scope-grants.json"}

// uninstallPurgeGuard refuses to purge a filesystem root, the home
// directory or any directory that contains it, and any directory that holds
// none of the ajent markers.
func uninstallPurgeGuard(configDir, home string) error {
	dir, err := filepath.Abs(configDir)
	if err != nil {
		return fmt.Errorf("uninstall: %s: %w", configDir, err)
	}
	if dir == filepath.Dir(dir) {
		return fmt.Errorf("uninstall: refusing to purge %s: it is a filesystem root", configDir)
	}
	if home != "" {
		h, err := filepath.Abs(home)
		if err != nil {
			return fmt.Errorf("uninstall: %s: %w", home, err)
		}
		if rel, err := filepath.Rel(dir, h); err == nil && (rel == "." || !strings.HasPrefix(rel, "..")) {
			return fmt.Errorf("uninstall: refusing to purge %s: it is or contains the home directory", configDir)
		}
	}
	for _, m := range uninstallMarkers {
		if _, err := os.Lstat(filepath.Join(dir, m)); err == nil {
			return nil
		}
	}
	return fmt.Errorf("uninstall: refusing to purge %s: it does not look like an ajent config directory", configDir)
}
