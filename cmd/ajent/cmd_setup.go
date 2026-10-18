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
		Name:    "setup",
		Summary: "Connect this machine and configure your coding tools",
		Usage:   "ajent setup [--handle NAME] [--no-wait] [--agent-only] [--scope user|project] [--domain DOMAIN] [--private]",
		Flags:   setupFlags,
		Run:     runSetup,
	})
	cli.Register(cli.Command{
		Name:    "install",
		Hidden:  true,
		Summary: "Connect this machine and configure your coding tools",
		Usage:   "ajent install [--handle NAME] [--no-wait] [--agent-only] [--scope user|project] [--domain DOMAIN] [--private]",
		Flags:   setupFlags,
		Run:     runSetup,
	})
	cli.Register(cli.Command{
		Name:    "link",
		Summary: "Link this installation to your human account",
		Usage:   "ajent link [--handle NAME] [--no-wait]",
		Flags:   linkFlags,
		Run:     runLink,
	})
}

// setupNewFlagSet builds the flag set shared by setup, install and link:
// --handle, --no-wait, --agent-only, --scope, --domain and --private
// (docs/tasks/T4.3.md step 5). A command that does not use one of these
// fields simply leaves it at its zero value.
func setupNewFlagSet(name string) (*flag.FlagSet, *setup.Options) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	opts := &setup.Options{}
	fs.StringVar(&opts.Handle, "handle", "", "installation handle")
	fs.BoolVar(&opts.NoWait, "no-wait", false, "print the approval URL and code, then return without waiting")
	fs.BoolVar(&opts.AgentOnly, "agent-only", false, "skip human linking and sign up directly")
	fs.StringVar(&opts.Scope, "scope", "", `sharing scope: "user" or "project"`)
	fs.StringVar(&opts.Domain, "domain", "", "shared domain")
	fs.BoolVar(&opts.Private, "private", false, "connect privately, without a shared domain")
	return fs, opts
}

// setupFlags returns a fresh FlagSet with setup's (and install's) flag
// definitions, used by "ajent help setup".
func setupFlags() *flag.FlagSet {
	fs, _ := setupNewFlagSet("setup")
	return fs
}

// linkFlags returns a fresh FlagSet with link's flag definitions, used by
// "ajent help link".
func linkFlags() *flag.FlagSet {
	fs, _ := setupNewFlagSet("link")
	return fs
}

// setupValidateScope returns a usage error unless scope is "", "user" or
// "project" (docs/tasks/T4.3.md step 5).
func setupValidateScope(scope string) error {
	switch scope {
	case "", "user", "project":
		return nil
	default:
		return fmt.Errorf("--scope must be \"user\" or \"project\": %w", cli.ErrUsage)
	}
}

// runSetup implements both "ajent setup" and its hidden alias "ajent
// install" (docs/contract.md, "Setup and link flow").
func runSetup(ctx context.Context, env *cli.Env, args []string) error {
	fs, opts := setupNewFlagSet("setup")
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := setupValidateScope(opts.Scope); err != nil {
		return err
	}
	return setup.Run(ctx, env, *opts)
}

// runLink implements "ajent link": it loads the root credential (failing
// with credentials.ErrNoCredential when none exists) and runs only the
// linking step.
func runLink(ctx context.Context, env *cli.Env, args []string) error {
	fs, opts := setupNewFlagSet("link")
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := setupValidateScope(opts.Scope); err != nil {
		return err
	}
	root, err := rootCredential(env)
	if err != nil {
		return err
	}
	_, err = setup.Link(ctx, env, *opts, root)
	return err
}
