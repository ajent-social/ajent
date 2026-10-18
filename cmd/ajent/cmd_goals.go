// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/url"
	"strconv"

	"github.com/ajent-social/ajent/internal/api"
	"github.com/ajent-social/ajent/internal/cli"
)

func init() {
	cli.Register(cli.Command{
		Name:    "goals",
		Summary: "List assigned goals",
		Usage:   "ajent goals [--profile NAME] [BEFORE]",
		Flags:   goalsFlags,
		Run:     runGoals,
	})
	cli.Register(cli.Command{
		Name:    "goal",
		Summary: "Read a goal and its events",
		Usage:   "ajent goal [--profile NAME] GOAL_UUID [BEFORE]",
		Flags:   goalFlags,
		Run:     runGoal,
	})
	cli.Register(cli.Command{
		Name:    "goal-update",
		Summary: "Record a goal event",
		Usage:   "ajent goal-update [--profile NAME] --key KEY [--json JSON] GOAL_UUID",
		Flags:   goalUpdateFlags,
		Run:     runGoalUpdate,
	})
	cli.Register(cli.Command{
		Name:    "goal-track",
		Summary: "Track already-authorized work as a goal",
		Usage:   "ajent goal-track [--profile NAME] --key KEY [--json JSON]",
		Flags:   goalTrackFlags,
		Run:     runGoalTrack,
	})
}

// errGoalNoID is returned by "goal" when it is given no positional goal ID
// (docs/tasks/T2.9.md step 3).
var errGoalNoID error = cli.UserError("Supply a goal UUID.")

// errGoalBadBefore is returned by "goal" when its optional second
// positional argument does not parse as an integer event sequence.
var errGoalBadBefore = errors.New("before must be an integer event sequence")

// errGoalNoKey is returned by "goal-update" and "goal-track" when --key is
// empty.
var errGoalNoKey error = cli.UserError("Supply a stable --key for retries.")

// errGoalJSONNotObject is returned by goalJSONBody when its input does not
// decode to a JSON object.
var errGoalJSONNotObject = errors.New("--json must be a JSON object")

// newGoalsFlagSet builds "goals"'s FlagSet and returns the --profile value.
func newGoalsFlagSet() (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("goals", flag.ContinueOnError)
	profile := profileFlags(fs)
	return fs, profile
}

// goalsFlags returns a fresh FlagSet with "goals"'s flag definitions, used
// by "ajent help goals".
func goalsFlags() *flag.FlagSet {
	fs, _ := newGoalsFlagSet()
	return fs
}

// runGoals implements "ajent goals [before]": GET /v1/goals, optionally
// filtered with ?before=<query-escaped before> (docs/tasks/T2.9.md step 2).
func runGoals(ctx context.Context, env *cli.Env, args []string) error {
	fs, profile := newGoalsFlagSet()
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	cred, err := toolCredential(ctx, env, *profile)
	if err != nil {
		return err
	}
	if err := env.API.Require(ctx, cred, "goals"); err != nil {
		return err
	}

	route := "/v1/goals"
	if fs.NArg() > 0 {
		route += "?before=" + url.QueryEscape(fs.Arg(0))
	}
	return getAndPrint(ctx, env, cred, route)
}

// newGoalFlagSet builds "goal"'s FlagSet and returns the --profile value.
func newGoalFlagSet() (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("goal", flag.ContinueOnError)
	profile := profileFlags(fs)
	return fs, profile
}

// goalFlags returns a fresh FlagSet with "goal"'s flag definitions, used by
// "ajent help goal".
func goalFlags() *flag.FlagSet {
	fs, _ := newGoalFlagSet()
	return fs
}

// runGoal implements "ajent goal <id> [before]": GET /v1/goals/<id>,
// optionally filtered with ?before=<event sequence> (docs/tasks/T2.9.md
// step 3).
func runGoal(ctx context.Context, env *cli.Env, args []string) error {
	fs, profile := newGoalFlagSet()
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	cred, err := toolCredential(ctx, env, *profile)
	if err != nil {
		return err
	}
	if err := env.API.Require(ctx, cred, "goals"); err != nil {
		return err
	}

	if fs.NArg() == 0 {
		return errGoalNoID
	}
	id, err := api.CanonicalUUID(fs.Arg(0))
	if err != nil {
		return err
	}

	route := "/v1/goals/" + id
	if fs.NArg() > 1 {
		before := fs.Arg(1)
		if _, err := strconv.Atoi(before); err != nil {
			return errGoalBadBefore
		}
		route += "?before=" + before
	}
	return getAndPrint(ctx, env, cred, route)
}

// newGoalWriteFlagSet builds a FlagSet named name with the flags shared by
// "goal-update" and "goal-track": --profile, --key and --json.
func newGoalWriteFlagSet(name string) (fs *flag.FlagSet, profile, key, jsonArg *string) {
	fs = flag.NewFlagSet(name, flag.ContinueOnError)
	profile = profileFlags(fs)
	key = fs.String("key", "", "stable idempotency key for retries")
	jsonArg = fs.String("json", "{}", "JSON object with the request body")
	return fs, profile, key, jsonArg
}

// goalJSONBody validates s (a --json flag value) as a JSON object and
// returns it as a json.RawMessage, so it is sent as the request body with
// its key order preserved exactly as given (docs/tasks/T2.9.md step 4).
func goalJSONBody(s string) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace([]byte(s))
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return nil, errGoalJSONNotObject
	}
	return json.RawMessage(trimmed), nil
}

// goalUpdateFlags returns a fresh FlagSet with "goal-update"'s flag
// definitions, used by "ajent help goal-update".
func goalUpdateFlags() *flag.FlagSet {
	fs, _, _, _ := newGoalWriteFlagSet("goal-update")
	return fs
}

// runGoalUpdate implements "ajent goal-update --key K [--json JSON] <id>":
// POST /v1/goals/<id>/events with the decoded --json object as the body
// (docs/tasks/T2.9.md step 4).
func runGoalUpdate(ctx context.Context, env *cli.Env, args []string) error {
	fs, profile, key, jsonArg := newGoalWriteFlagSet("goal-update")
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	cred, err := toolCredential(ctx, env, *profile)
	if err != nil {
		return err
	}
	if err := env.API.Require(ctx, cred, "goals"); err != nil {
		return err
	}

	if *key == "" {
		return errGoalNoKey
	}
	id, err := api.CanonicalUUID(fs.Arg(0))
	if err != nil {
		return err
	}
	body, err := goalJSONBody(*jsonArg)
	if err != nil {
		return err
	}

	req := api.Request{Method: "POST", Route: "/v1/goals/" + id + "/events", Body: body, Key: *key}
	return sendAndPrint(ctx, env, cred, req)
}

// goalTrackFlags returns a fresh FlagSet with "goal-track"'s flag
// definitions, used by "ajent help goal-track".
func goalTrackFlags() *flag.FlagSet {
	fs, _, _, _ := newGoalWriteFlagSet("goal-track")
	return fs
}

// runGoalTrack implements "ajent goal-track --key K [--json JSON]": POST
// /v1/goals with the decoded --json object as the body (docs/tasks/T2.9.md
// step 5).
func runGoalTrack(ctx context.Context, env *cli.Env, args []string) error {
	fs, profile, key, jsonArg := newGoalWriteFlagSet("goal-track")
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	cred, err := toolCredential(ctx, env, *profile)
	if err != nil {
		return err
	}
	if err := env.API.Require(ctx, cred, "goals"); err != nil {
		return err
	}

	if *key == "" {
		return errGoalNoKey
	}
	body, err := goalJSONBody(*jsonArg)
	if err != nil {
		return err
	}

	req := api.Request{Method: "POST", Route: "/v1/goals", Body: body, Key: *key}
	return sendAndPrint(ctx, env, cred, req)
}
