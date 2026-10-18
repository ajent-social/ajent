// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/ajent-social/ajent-client/internal/api"
	"github.com/ajent-social/ajent-client/internal/cli"
	"github.com/ajent-social/ajent-client/internal/scope"
)

func init() {
	cli.Register(cli.Command{
		Name:    "configure",
		Summary: "Choose the sharing scope and a domain or private mode",
		Usage:   "ajent configure [--scope user|project] [--domain DOMAIN] [--private]",
		Flags:   scopeConfigureFlagSet,
		Run:     runScopeConfigure,
	})
	cli.Register(cli.Command{
		Name:    "domains",
		Summary: "List domains and grants for the installation",
		Usage:   "ajent domains",
		Run:     runScopeDomains,
	})
	cli.Register(cli.Command{
		Name:    "revoke-grant",
		Summary: "Revoke a domain grant",
		Usage:   "ajent revoke-grant GRANT_ID",
		Run:     runScopeRevokeGrant,
	})
}

// scopeConfigureArgs holds the parsed --scope, --domain and --private
// flags for "ajent configure".
type scopeConfigureArgs struct {
	scope   *string
	domain  *string
	private *bool
}

// newScopeConfigureFlagSet builds "configure"'s FlagSet and returns its
// parsed argument pointers, so runScopeConfigure and scopeConfigureFlagSet
// share one definition.
func newScopeConfigureFlagSet() (*flag.FlagSet, *scopeConfigureArgs) {
	fs := flag.NewFlagSet("configure", flag.ContinueOnError)
	a := &scopeConfigureArgs{
		scope:   fs.String("scope", "", "sharing scope: user or project"),
		domain:  fs.String("domain", "", "domain to represent"),
		private: fs.Bool("private", false, "use private mode instead of a domain"),
	}
	return fs, a
}

// scopeConfigureFlagSet returns a fresh FlagSet with configure's flag
// definitions, used by "ajent help configure".
func scopeConfigureFlagSet() *flag.FlagSet {
	fs, _ := newScopeConfigureFlagSet()
	return fs
}

// errScopeBadFlag is a usage error: --scope was given a value other than
// "user" or "project".
var errScopeBadFlag = fmt.Errorf("--scope must be \"user\" or \"project\": %w", cli.ErrUsage)

func runScopeConfigure(ctx context.Context, env *cli.Env, args []string) error {
	fs, a := newScopeConfigureFlagSet()
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *a.scope != "" && *a.scope != "user" && *a.scope != "project" {
		return errScopeBadFlag
	}

	domainSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "domain" {
			domainSet = true
		}
	})

	root, err := rootCredential(env)
	if err != nil {
		return err
	}

	message, err := scope.Configure(ctx, env, root, scope.Options{
		Scope:     *a.scope,
		Domain:    *a.domain,
		DomainSet: domainSet,
		Private:   *a.private,
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(env.Stdout, message)
	return err
}

func runScopeDomains(ctx context.Context, env *cli.Env, _ []string) error {
	root, err := rootCredential(env)
	if err != nil {
		return err
	}
	return getAndPrint(ctx, env, root, "/v1/domains")
}

// errScopeNoGrantID is the exact "revoke-grant" validation message from
// testdata/contract/commands.json, case "no_id".
var errScopeNoGrantID error = cli.UserError("Supply the grant ID shown by ajent domains.")

func runScopeRevokeGrant(ctx context.Context, env *cli.Env, args []string) error {
	fs := flag.NewFlagSet("revoke-grant", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return errScopeNoGrantID
	}
	id, err := api.CanonicalUUID(rest[0])
	if err != nil {
		return errScopeNoGrantID
	}

	root, err := rootCredential(env)
	if err != nil {
		return err
	}
	return sendAndPrint(ctx, env, root, api.Request{Method: "DELETE", Route: "/v1/domain-grants/" + id})
}
