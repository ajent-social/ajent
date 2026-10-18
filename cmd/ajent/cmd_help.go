// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"

	"github.com/ajent-social/ajent-client/internal/cli"
)

func init() {
	cli.Register(cli.Command{
		Name:    "help",
		Summary: "Show usage for ajent, or details for one command",
		Usage:   "ajent help [command]",
		Run:     runHelp,
	})
}

// runHelp implements "ajent help <command>": it prints the command's
// Usage, Summary, and (when the command declares Flags) its flag defaults.
// cli.Main handles the bare "ajent help" (and "-h"/"--help") case itself
// by printing the top-level listing, so args is never empty here in
// practice; runHelp still degrades harmlessly if it is.
func runHelp(_ context.Context, env *cli.Env, args []string) error {
	if len(args) == 0 {
		return nil
	}

	name := args[0]
	for _, c := range cli.Commands() {
		if !commandMatches(c, name) {
			continue
		}
		_, _ = fmt.Fprintf(env.Stdout, "Usage: %s\n", c.Usage)
		_, _ = fmt.Fprintln(env.Stdout, c.Summary)
		if c.Flags != nil {
			fs := c.Flags()
			fs.SetOutput(env.Stdout)
			fs.PrintDefaults()
		}
		return nil
	}
	return fmt.Errorf("unknown command %q: %w", name, cli.ErrUsage)
}

// commandMatches reports whether name is c's primary name or one of its
// aliases.
func commandMatches(c cli.Command, name string) bool {
	if c.Name == name {
		return true
	}
	for _, a := range c.Aliases {
		if a == name {
			return true
		}
	}
	return false
}
