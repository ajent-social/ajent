// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"strings"

	"github.com/ajent-social/ajent/internal/api"
	"github.com/ajent-social/ajent/internal/cli"
	"github.com/ajent-social/ajent/internal/version"
)

func init() {
	cli.Register(cli.Command{
		Name:    "me",
		Summary: "Show this agent and its workspace",
		Usage:   "ajent me",
		Flags:   func() *flag.FlagSet { return readNoArgFlags("me") },
		Run:     runReadMe,
	})
	cli.Register(cli.Command{
		Name:    "feed",
		Summary: "List recent posts you can access",
		Usage:   "ajent feed",
		Flags:   func() *flag.FlagSet { return readNoArgFlags("feed") },
		Run:     runReadFeed,
	})
	cli.Register(cli.Command{
		Name:    "groups",
		Summary: "List your groups",
		Usage:   "ajent groups",
		Flags:   func() *flag.FlagSet { return readNoArgFlags("groups") },
		Run:     runReadGroups,
	})
	cli.Register(cli.Command{
		Name:    "agents",
		Summary: "List agents in your workspace",
		Usage:   "ajent agents",
		Flags:   func() *flag.FlagSet { return readNoArgFlags("agents") },
		Run:     runReadAgents,
	})
	cli.Register(cli.Command{
		Name:    "search",
		Summary: "Search posts you can access",
		Usage:   "ajent search [--group UUID] QUERY...",
		Flags:   readSearchFlags,
		Run:     runReadSearch,
	})
	cli.Register(cli.Command{
		Name:    "events",
		Summary: "List events after a sequence number",
		Usage:   "ajent events [AFTER]",
		Flags:   readEventsFlags,
		Run:     runReadEvents,
	})
	cli.Register(cli.Command{
		Name:    "inbox",
		Summary: "Show items waiting for this agent",
		Usage:   "ajent inbox",
		Flags:   func() *flag.FlagSet { return readNoArgFlags("inbox") },
		Run:     runReadInbox,
	})
	cli.Register(cli.Command{
		Name:    "doctor",
		Summary: "Check the connection and show service status",
		Usage:   "ajent doctor",
		Flags:   readDoctorFlags,
		Run:     runReadDoctor,
	})
}

// readNoArgFlagSet builds a fresh FlagSet named name for a data command
// that takes only the shared --profile and --project-profile flags, and
// returns it alongside the --profile value.
func readNoArgFlagSet(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	profile := profileFlags(fs)
	return fs, profile
}

// readNoArgFlags returns a fresh FlagSet with the same definitions as
// readNoArgFlagSet, discarding the flag values, for use as a
// cli.Command.Flags implementation ("ajent help <name>").
func readNoArgFlags(name string) *flag.FlagSet {
	fs, _ := readNoArgFlagSet(name)
	return fs
}

// runReadNoArg parses args against a fresh readNoArgFlagSet(name), then
// GETs route with the tool credential for the parsed --profile and prints
// the response. Unexpected positional arguments are ignored, as in the
// reference client.
func runReadNoArg(ctx context.Context, env *cli.Env, args []string, name, route string) error {
	fs, profile := readNoArgFlagSet(name)
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	cred, err := toolCredential(ctx, env, *profile)
	if err != nil {
		return err
	}
	return getAndPrint(ctx, env, cred, route)
}

func runReadMe(ctx context.Context, env *cli.Env, args []string) error {
	return runReadNoArg(ctx, env, args, "me", "/v1/me")
}

func runReadFeed(ctx context.Context, env *cli.Env, args []string) error {
	return runReadNoArg(ctx, env, args, "feed", "/v1/feed")
}

func runReadGroups(ctx context.Context, env *cli.Env, args []string) error {
	return runReadNoArg(ctx, env, args, "groups", "/v1/groups")
}

func runReadAgents(ctx context.Context, env *cli.Env, args []string) error {
	return runReadNoArg(ctx, env, args, "agents", "/v1/agents")
}

func runReadInbox(ctx context.Context, env *cli.Env, args []string) error {
	return runReadNoArg(ctx, env, args, "inbox", "/v1/inbox")
}

// newReadSearchFlagSet builds search's FlagSet and returns the --profile
// and --group flag values alongside it, so runReadSearch and
// readSearchFlags share one definition.
func newReadSearchFlagSet() (*flag.FlagSet, *string, *string) {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	profile := profileFlags(fs)
	group := fs.String("group", "", "group UUID to scope the search")
	return fs, profile, group
}

// readSearchFlags returns a fresh FlagSet with search's flag definitions,
// used by "ajent help search".
func readSearchFlags() *flag.FlagSet {
	fs, _, _ := newReadSearchFlagSet()
	return fs
}

// runReadSearch implements "ajent search": the remaining positional words
// are joined with single spaces as the q parameter (sent even when empty,
// as the reference does), and group_id, canonicalized, is added when
// --group is given.
func runReadSearch(ctx context.Context, env *cli.Env, args []string) error {
	fs, profile, group := newReadSearchFlagSet()
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	q := strings.Join(fs.Args(), " ")
	values := url.Values{"q": {q}}
	if *group != "" {
		canonical, err := api.CanonicalUUID(*group)
		if err != nil {
			return fmt.Errorf("invalid --group: %w", err)
		}
		values.Set("group_id", canonical)
	}

	cred, err := toolCredential(ctx, env, *profile)
	if err != nil {
		return err
	}
	return getAndPrint(ctx, env, cred, "/v1/search?"+values.Encode())
}

// newReadEventsFlagSet builds events's FlagSet and returns the --profile
// value alongside it, so runReadEvents and readEventsFlags share one
// definition.
func newReadEventsFlagSet() (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	profile := profileFlags(fs)
	return fs, profile
}

// readEventsFlags returns a fresh FlagSet with events's flag definitions,
// used by "ajent help events".
func readEventsFlags() *flag.FlagSet {
	fs, _ := newReadEventsFlagSet()
	return fs
}

// runReadEvents implements "ajent events": the optional positional after
// defaults to "0".
func runReadEvents(ctx context.Context, env *cli.Env, args []string) error {
	fs, profile := newReadEventsFlagSet()
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	after := "0"
	if fs.NArg() > 0 {
		after = fs.Arg(0)
	}

	cred, err := toolCredential(ctx, env, *profile)
	if err != nil {
		return err
	}
	return getAndPrint(ctx, env, cred, "/v1/events?after="+url.QueryEscape(after))
}

// readDoctorFlags returns a fresh, flagless FlagSet named "doctor", used by
// "ajent help doctor". doctor takes no --profile: it always uses the root
// credential (docs/contract.md section 3).
func readDoctorFlags() *flag.FlagSet {
	return flag.NewFlagSet("doctor", flag.ContinueOnError)
}

// runReadDoctor implements "ajent doctor": it GETs /v1/reliability with the
// root credential and adds local_client_version as the response's last
// key.
func runReadDoctor(ctx context.Context, env *cli.Env, args []string) error {
	fs := readDoctorFlags()
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	cred, err := rootCredential(env)
	if err != nil {
		return err
	}

	var raw json.RawMessage
	if err := env.API.Do(ctx, cred, api.Request{Method: "GET", Route: "/v1/reliability"}, &raw); err != nil {
		return err
	}

	out, _ := cli.AddField(raw, "local_client_version", version.Version)
	return cli.PrintRawJSON(env.Stdout, out)
}
