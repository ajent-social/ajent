// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/ajent-social/ajent-client/internal/cli"
	"github.com/ajent-social/ajent-client/internal/config"
	"github.com/ajent-social/ajent-client/internal/integrations"
)

func init() {
	cli.Register(cli.Command{
		Name:    "integrations",
		Summary: "Show, sync or remove coding-tool integrations",
		Usage:   "ajent integrations [status|sync|remove]",
		Run:     runIntegrations,
	})
	cli.Register(cli.Command{
		Name:    "rollback",
		Summary: "Restore a configuration backup",
		Usage:   "ajent rollback BACKUP_ID",
		Run:     runRollback,
	})
}

// paramsFromEnv builds the integrations.Params every "integrations" and
// "rollback" subcommand needs (docs/tasks/T4.6.md step 5).
func paramsFromEnv(env *cli.Env) (integrations.Params, error) {
	return integrations.ParamsFromEnv(env.Home, env.ConfigDir, env.Getenv)
}

// runIntegrations dispatches "ajent integrations [status|sync|remove]",
// defaulting to "status" when no subcommand is given.
func runIntegrations(ctx context.Context, env *cli.Env, args []string) error {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}

	switch sub {
	case "status":
		return runIntegrationsStatus(env)
	case "sync":
		return runIntegrationsSync(ctx, env)
	case "remove":
		return runIntegrationsRemove(ctx, env)
	default:
		return fmt.Errorf("unknown subcommand %q: %w", sub, cli.ErrUsage)
	}
}

// integrationsDisabled reports whether AJENT_INTEGRATIONS=0 is set, which
// skips every harness integration write (docs/contract.md section 4). It
// prints a note to stderr so the skip is visible.
func integrationsDisabled(env *cli.Env) bool {
	if env.Getenv("AJENT_INTEGRATIONS") != "0" {
		return false
	}
	_, _ = fmt.Fprintln(env.Stderr, "Skipped coding-tool integrations because AJENT_INTEGRATIONS=0.")
	return true
}

// runIntegrationsStatus implements "ajent integrations status": the names
// of every currently detected writer, plus the last recorded sync (or null
// when none has run yet).
func runIntegrationsStatus(env *cli.Env) error {
	p, err := paramsFromEnv(env)
	if err != nil {
		return err
	}

	detected := []string{}
	for _, w := range integrations.Writers() {
		if w.Detect(p) {
			detected = append(detected, w.Target().Name)
		}
	}

	var last any
	statusPath := filepath.Join(env.ConfigDir, "integration-status.json")
	if err := config.ReadJSON(statusPath, &last); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		last = nil
	}

	return cli.PrintJSON(env.Stdout, map[string]any{
		"detected": detected,
		"last":     last,
	})
}

// runIntegrationsSync implements "ajent integrations sync" and, by
// docs/tasks/T4.6.md step 6, is reused directly as setup.Integrate
// (wire_integrations.go): it snapshots and applies every detected writer,
// then prints one line per result plus the backup and reload lines.
func runIntegrationsSync(ctx context.Context, env *cli.Env) error {
	if integrationsDisabled(env) {
		return nil
	}
	p, err := paramsFromEnv(env)
	if err != nil {
		return err
	}

	results, backup, err := integrations.Sync(ctx, p)
	if err != nil {
		return err
	}

	integrationsPrintResults(env, results)
	if _, err := fmt.Fprintf(env.Stdout, "Integration backup: %s. Roll back with: ajent rollback %s\n", backup.ID, backup.ID); err != nil {
		return err
	}
	_, err = fmt.Fprintln(env.Stdout, "Reload MCP connections or start new agent sessions to pick up the integration.")
	return err
}

// runIntegrationsRemove implements "ajent integrations remove": it snapshots
// every currently detected writer's Paths, then removes ajent's entry from
// each.
func runIntegrationsRemove(ctx context.Context, env *cli.Env) error {
	if integrationsDisabled(env) {
		return nil
	}
	p, err := paramsFromEnv(env)
	if err != nil {
		return err
	}

	var detected []integrations.Writer
	seen := map[string]bool{}
	var paths []string
	for _, w := range integrations.Writers() {
		if !w.Detect(p) {
			continue
		}
		detected = append(detected, w)
		for _, path := range w.Paths(p) {
			if !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
		}
	}

	backup, err := integrations.Snapshot(p.ConfigDir, paths)
	if err != nil {
		return err
	}

	results := make([]integrations.Result, 0, len(detected))
	for _, w := range detected {
		res, removeErr := w.Remove(ctx, p)
		if removeErr != nil {
			results = append(results, integrations.Result{Name: w.Target().Name, Status: "incomplete", Detail: removeErr.Error()})
			continue
		}
		results = append(results, res)
	}

	integrationsPrintResults(env, results)
	_, err = fmt.Fprintf(env.Stdout, "Integration backup: %s. Roll back with: ajent rollback %s\n", backup.ID, backup.ID)
	return err
}

// integrationsPrintResults prints one "<name>: <status>" line per result to
// env.Stdout, appending ": <detail>" when Detail is set.
func integrationsPrintResults(env *cli.Env, results []integrations.Result) {
	for _, r := range results {
		if r.Detail != "" {
			_, _ = fmt.Fprintf(env.Stdout, "%s: %s: %s\n", r.Name, r.Status, r.Detail)
		} else {
			_, _ = fmt.Fprintf(env.Stdout, "%s: %s\n", r.Name, r.Status)
		}
	}
}

// runRollback implements "ajent rollback BACKUP_ID".
func runRollback(_ context.Context, env *cli.Env, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: ajent rollback BACKUP_ID", cli.ErrUsage)
	}
	id := args[0]

	if err := integrations.Rollback(env.ConfigDir, id); err != nil {
		return err
	}

	_, err := fmt.Fprintf(env.Stdout, "Restored installation backup %s. Restart affected MCP processes.\n", id)
	return err
}
