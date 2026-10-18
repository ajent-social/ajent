// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/ajent-social/ajent-client/internal/cli"
	"github.com/ajent-social/ajent-client/internal/credentials"
	"github.com/ajent-social/ajent-client/internal/mcp"
)

func init() {
	cli.Register(cli.Command{
		Name:    "mcp",
		Summary: "Run the MCP server over stdio",
		Usage:   "ajent mcp [--profile NAME] [--project-profile]",
		Flags:   mcpFlags,
		Run:     runMCP,
	})
}

// newMCPFlagSet builds mcp's FlagSet and returns the --profile and
// --project-profile flag values alongside it, so runMCP and mcpFlags share
// one definition. Unlike profileFlags (helpers.go), --project-profile's
// value is used: mcp applies the project hash only when the flag is given
// (docs/contract.md section 3), instead of always.
func newMCPFlagSet() (fs *flag.FlagSet, profile *string, projectProfile *bool) {
	fs = flag.NewFlagSet("mcp", flag.ContinueOnError)
	profile = fs.String("profile", "terminal", "tool profile name")
	projectProfile = fs.Bool("project-profile", false, "apply the project hash to --profile")
	return fs, profile, projectProfile
}

// mcpFlags returns a fresh FlagSet with mcp's flag definitions, used by
// "ajent help mcp".
func mcpFlags() *flag.FlagSet {
	fs, _, _ := newMCPFlagSet()
	return fs
}

// runMCP resolves the tool profile and root credential, then serves MCP
// over stdio on env.Stdin/env.Stdout until EOF. Every failure before
// mcp.Serve starts is written to env.Stderr directly, so stdout carries no
// diagnostics even when this function is called outside cli.Main.
func runMCP(ctx context.Context, env *cli.Env, args []string) error {
	fs, profile, projectProfile := newMCPFlagSet()
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	root := credentials.ProjectRoot(env.Cwd, env.Home)
	name := *profile
	if *projectProfile {
		name = credentials.ProjectProfile(name, root)
	}
	if !credentials.ValidProfile(name) {
		_, _ = fmt.Fprintf(env.Stderr, "ajent: %s\n", credentials.ErrInvalidProfile)
		return credentials.ErrInvalidProfile
	}

	rootCred, err := credentials.LoadRoot(env.ConfigDir)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "ajent: %s\n", err)
		return err
	}

	// The MCP server always identifies itself as "mcp", regardless of what
	// client kind the rest of the CLI would send.
	apiClient := *env.API
	apiClient.ClientKind = "mcp"

	cache := mcp.OpenCache(env.ConfigDir, name)
	defer func() { _ = cache.Close() }()

	s := &mcp.Session{
		Profile:     name,
		Dir:         env.ConfigDir,
		Root:        rootCred,
		API:         &apiClient,
		Resolve:     env.Resolve,
		Cache:       cache,
		ProjectRoot: root,
	}

	return mcp.Serve(ctx, env.Stdin, env.Stdout, s)
}
