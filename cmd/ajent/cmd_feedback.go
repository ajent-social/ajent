// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"

	"github.com/ajent-social/ajent/internal/cli"
	"github.com/ajent-social/ajent/internal/credentials"
	"github.com/ajent-social/ajent/internal/feedback"
)

func init() {
	cli.Register(cli.Command{
		Name:    "feedback",
		Summary: "Inspect and control local feedback capture and sharing policy",
		Usage:   "ajent feedback [show|preview|stats|set|clear|revoke]",
		Run:     runFeedback,
	})
	cli.Register(cli.Command{
		Name:    "help-improve",
		Summary: "Report friction or verify a shipped fix, gated on feedback policy",
		Usage:   "ajent help-improve [--profile NAME] [--json JSON]",
		Flags:   helpImproveFlags,
		Run:     runHelpImprove,
	})
}

// errFeedbackBadAction is "feedback"'s error for any action other than
// show, preview, stats, set, clear or revoke. Its text is the exact wording
// testdata/contract/commands.json fixes for the "bad_action" case
// (docs/contract.md section 12).
var errFeedbackBadAction error = cli.UserError("Use show, set, revoke, preview or clear.")

// runFeedback implements "ajent feedback [action]" (default "show"): it
// validates action itself (so the exact fixture wording above reaches
// cli.Main, since internal/feedback cannot import internal/cli) and prints
// feedback.Store.Local's result (docs/tasks/T2.11.md step 6).
func runFeedback(_ context.Context, env *cli.Env, args []string) error {
	action := "show"
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "show", "preview", "stats", "set", "clear", "revoke":
	default:
		return errFeedbackBadAction
	}

	store := &feedback.Store{Dir: env.ConfigDir}
	result, err := store.Local(action)
	if err != nil {
		return err
	}
	return cli.PrintJSON(env.Stdout, result)
}

// newHelpImproveFlagSet builds "help-improve"'s FlagSet and returns the
// --profile and --json flag values alongside it.
func newHelpImproveFlagSet() (fs *flag.FlagSet, profile, jsonArg *string) {
	fs = flag.NewFlagSet("help-improve", flag.ContinueOnError)
	profile = profileFlags(fs)
	jsonArg = fs.String("json", "{}", "structured arguments (JSON object)")
	return fs, profile, jsonArg
}

// helpImproveFlags returns a fresh FlagSet with "help-improve"'s flag
// definitions, used by "ajent help help-improve".
func helpImproveFlags() *flag.FlagSet {
	fs, _, _ := newHelpImproveFlagSet()
	return fs
}

// runHelpImprove implements "ajent help-improve": it resolves the existing,
// never-enrolled profile credential and runs feedback.Store.HelpImprove
// with the decoded --json arguments (docs/tasks/T2.11.md step 6).
func runHelpImprove(ctx context.Context, env *cli.Env, args []string) error {
	fs, profile, jsonArg := newHelpImproveFlagSet()
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	parsedArgs, err := jsonArgs(*jsonArg)
	if err != nil {
		return err
	}

	cred, err := feedbackExistingProfileCredential(env, *profile)
	if err != nil {
		return err
	}

	store := &feedback.Store{Dir: env.ConfigDir}
	result, err := store.HelpImprove(ctx, env.API, cred, parsedArgs)
	if err != nil {
		return err
	}
	return cli.PrintJSON(env.Stdout, result)
}

// feedbackExistingProfileCredential resolves the "existing profile, never
// enrolling" credential docs/contract.md section 3 describes for
// help-improve: load the root credential, then look up the project-scoped
// profile for base. It uses the profile only when it exists and is not
// pending; otherwise it falls back to the root credential. It never calls
// env.Resolve (env.Credential), so help-improve never enrolls a profile
// (docs/tasks/T2.11.md step 6).
func feedbackExistingProfileCredential(env *cli.Env, base string) (credentials.Credential, error) {
	root, err := rootCredential(env)
	if err != nil {
		return credentials.Credential{}, err
	}

	dir, err := env.Dir()
	if err != nil {
		return credentials.Credential{}, err
	}

	projectRoot := credentials.ProjectRoot(env.Cwd, env.Home)
	name := credentials.ProjectProfile(base, projectRoot)

	profile, ok, err := credentials.LoadProfile(dir, root, name)
	if err != nil {
		return credentials.Credential{}, err
	}
	if ok && !profile.Pending {
		return profile, nil
	}
	return root, nil
}
