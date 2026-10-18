// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/ajent-social/ajent/internal/cli"
	"github.com/ajent-social/ajent/internal/version"
)

func init() {
	cli.Register(cli.Command{
		Name:    "version",
		Summary: "Print the client's version",
		Usage:   "ajent version [--json]",
		Flags:   versionFlags,
		Run:     runVersion,
	})
}

// newVersionFlagSet builds version's FlagSet and returns the --json flag's
// value alongside it, so runVersion and versionFlags share one definition.
func newVersionFlagSet() (*flag.FlagSet, *bool) {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	// --json here is a boolean output switch, the one documented exception
	// to the shared --json convention (a JSON-object argument string,
	// internal/cli's package comment): "version" takes no structured
	// arguments, so --json only chooses the output format.
	jsonOut := fs.Bool("json", false, "print version.Get() as JSON instead of the one-line summary")
	return fs, jsonOut
}

// versionFlags returns a fresh FlagSet with version's flag definitions,
// used by "ajent help version".
func versionFlags() *flag.FlagSet {
	fs, _ := newVersionFlagSet()
	return fs
}

func runVersion(_ context.Context, env *cli.Env, args []string) error {
	fs, jsonOut := newVersionFlagSet()
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	info := version.Get()
	if *jsonOut {
		return cli.PrintJSON(env.Stdout, info)
	}
	_, err := fmt.Fprintf(env.Stdout, "ajent %s (%s, %s)\n", info.Version, info.Commit, info.Date)
	return err
}
