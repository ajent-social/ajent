// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/ajent-social/ajent-client/internal/cli"
	"github.com/ajent-social/ajent-client/internal/setup"
)

func init() {
	cli.Register(cli.Command{
		Name:    "migrate",
		Summary: "Move an earlier Python installation to ajent",
		Usage:   "ajent migrate [--dry-run]",
		Flags:   migrateFlags,
		Run:     runMigrate,
	})
}

// migrateNewFlagSet builds migrate's flag set: --dry-run.
func migrateNewFlagSet() (*flag.FlagSet, *bool) {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "show the migration plan without changing anything")
	return fs, dryRun
}

// migrateFlags returns a fresh FlagSet with migrate's flag definitions, for
// "ajent help migrate".
func migrateFlags() *flag.FlagSet {
	fs, _ := migrateNewFlagSet()
	return fs
}

// runMigrate implements "ajent migrate" (docs/tasks/T4.9.md step 5): with
// no earlier Python installation detected, it prints a message and does
// nothing; --dry-run prints the plan setup.PlanMigration describes without
// changing anything; otherwise it runs setup.Migrate.
func runMigrate(ctx context.Context, env *cli.Env, args []string) error {
	fs, dryRun := migrateNewFlagSet()
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	if !setup.DetectPython(env.ConfigDir) {
		_, err := fmt.Fprintln(env.Stdout, "No earlier Python installation found.")
		return err
	}

	if *dryRun {
		plan, err := setup.PlanMigration(ctx, env)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(env.Stdout, "Planned changes:"); err != nil {
			return err
		}
		for _, change := range plan.Changes {
			if _, err := fmt.Fprintf(env.Stdout, "- %s\n", change); err != nil {
				return err
			}
		}
		return nil
	}

	_, err := setup.Migrate(ctx, env)
	return err
}
