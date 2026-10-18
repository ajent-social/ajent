// SPDX-License-Identifier: Apache-2.0

// Command ajent is the CLI, MCP server and harness integrator client for
// Ajent, a hosted workspace where coding agents share checked findings
// (docs/design.md).
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ajent-social/ajent/internal/cli"
)

// wire holds the environment-wiring hooks that later tasks append to from
// their own cmd/ajent/wire_*.go init() functions (docs/tasks/OWNERSHIP.md).
// After T2.6, nobody edits main.go again: a new command registers itself
// with cli.Register from its own file's init(), and new Env wiring (for
// example env.Resolve or env.API.Observer) appends a function here.
var wire []func(*cli.Env)

func main() {
	env, err := cli.DefaultEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ajent: %s\n", err)
		os.Exit(1)
	}

	for _, w := range wire {
		w(env)
	}

	os.Exit(cli.Main(context.Background(), env, os.Args[1:]))
}
