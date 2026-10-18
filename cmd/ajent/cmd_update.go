// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/ajent-social/ajent-client/internal/cli"
	"github.com/ajent-social/ajent-client/internal/update"
	"github.com/ajent-social/ajent-client/internal/version"
)

// executablePath resolves the path to the running binary. newSource
// builds the release Source runUpdate queries. Both are seams so tests
// can point them at a temp copy of a built binary and a fakerelease
// server, without touching the real filesystem or network.
var (
	executablePath = os.Executable
	newSource      = update.DefaultSource
)

// startDetached and autoRun are shared seams other update-related rows
// attach through, declared here and never redeclared elsewhere
// (docs/tasks/OWNERSHIP.md): T6.4, T6.5 and T6.6 call startDetached, and
// T6.6 assigns autoRun from cmd/ajent/hook_autoupdate.go. autoRun is nil
// until then, so a build without T6.6 reports the hidden --auto-run flag
// as unavailable rather than panicking.
var (
	startDetached = update.StartDetached //nolint:unused // shared seam (docs/tasks/OWNERSHIP.md): T6.4, T6.5 and T6.6 call this from their own files; this row only declares it, so nothing in this lane's own build calls it yet.
	autoRun       func(ctx context.Context, env *cli.Env) error
)

func init() {
	if exe, err := executablePath(); err == nil {
		update.CleanupOld(exe)
	}

	cli.Register(cli.Command{
		Name:    "update",
		Summary: "Update ajent to the latest release",
		Usage:   "ajent update [--check] [--version VERSION] [--pre] [--auto on|off|status]",
		Flags:   updateFlags,
		Run:     runUpdate,
	})
}

// newUpdateFlagSet builds update's full FlagSet, including the hidden
// --auto-run flag runAutoUpdate (T6.6) triggers through.
func newUpdateFlagSet() (fs *flag.FlagSet, checkFlag, preFlag, autoRunFlag *bool, versionFlag, autoFlag *string) {
	fs = flag.NewFlagSet("update", flag.ContinueOnError)
	checkFlag = fs.Bool("check", false, "report whether an update is available without changing anything")
	versionFlag = fs.String("version", "", "pin a specific release version instead of the latest")
	preFlag = fs.Bool("pre", false, "allow prerelease versions")
	autoFlag = fs.String("auto", "", "set the automatic-update preference: on, off or status")
	autoRunFlag = fs.Bool("auto-run", false, "")
	return fs, checkFlag, preFlag, autoRunFlag, versionFlag, autoFlag
}

// updateFlags returns a fresh FlagSet with update's public flag
// definitions, used by "ajent help update". It deliberately omits the
// hidden --auto-run flag (docs/tasks/T6.3.md step 3), which exists only
// for the session-start hook's background auto-update run.
func updateFlags() *flag.FlagSet {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.Bool("check", false, "report whether an update is available without changing anything")
	fs.String("version", "", "pin a specific release version instead of the latest")
	fs.Bool("pre", false, "allow prerelease versions")
	fs.String("auto", "", "set the automatic-update preference: on, off or status")
	return fs
}

// updateCheckResult is the JSON body "ajent update --check" prints.
type updateCheckResult struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
	Method          string `json:"method"`
	Command         string `json:"command"`
}

func runUpdate(ctx context.Context, env *cli.Env, args []string) error {
	fs, checkFlag, preFlag, autoRunFlag, versionFlag, autoFlag := newUpdateFlagSet()
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *autoRunFlag {
		if autoRun == nil {
			return errors.New("automatic updates are not available in this build")
		}
		return autoRun(ctx, env)
	}

	exe, err := executablePath()
	if err != nil {
		return fmt.Errorf("update: resolving the running executable: %w", err)
	}
	method := update.Detect(exe, env.ConfigDir)

	if *autoFlag != "" {
		return runUpdateAuto(env, method, *autoFlag)
	}

	src := newSource(env.ConfigDir)

	var target update.Release
	if *versionFlag != "" {
		if !update.Valid(*versionFlag) {
			return fmt.Errorf("--version %q is not a valid version: %w", *versionFlag, cli.ErrUsage)
		}
		target, err = src.Get(ctx, *versionFlag)
	} else {
		target, err = src.Latest(ctx, *preFlag)
	}
	if err != nil {
		if errors.Is(err, update.ErrUnavailable) {
			return fmt.Errorf("update check unavailable: %w", err)
		}
		return err
	}

	current := version.Version
	available := update.Newer(target.Version, current) || (*versionFlag != "" && target.Version != current)

	if *checkFlag {
		return runUpdateCheck(env, target, method, available)
	}

	if !available {
		_, err := fmt.Fprintf(env.Stdout, "ajent %s is up to date\n", current)
		return err
	}

	if method.Managed() {
		_, err := fmt.Fprintf(env.Stdout, "ajent was installed with %s. Upgrade with: %s\n", method.Label(), update.UpgradeCommand(method))
		return err
	}

	if method == update.MethodUnknown {
		reinstall := "curl -fsSL https://ajent.social/install.sh | sh"
		if runtime.GOOS == "windows" {
			reinstall = "irm https://ajent.social/install.ps1 | iex"
		}
		_, err := fmt.Fprintf(env.Stdout, "ajent cannot tell how it was installed. Reinstall with: %s\n", reinstall)
		return err
	}

	// method == update.MethodScript: apply the verified release in place.
	if err := update.Apply(ctx, src, target, exe); err != nil {
		return err
	}

	dir, err := env.Dir()
	if err != nil {
		return err
	}
	st, err := update.LoadState(dir)
	if err != nil {
		return err
	}
	st.Latest = target.Version
	st.LastCheck = env.Now()
	if err := update.SaveState(dir, st); err != nil {
		return err
	}

	_, err = fmt.Fprintf(env.Stdout, "updated ajent %s -> %s\n", current, target.Version)
	return err
}

// runUpdateCheck implements "ajent update --check" (docs/tasks/T6.3.md
// step 3.5): it persists the refreshed state and prints the check result
// as JSON, without applying anything.
func runUpdateCheck(env *cli.Env, target update.Release, method update.Method, available bool) error {
	dir, err := env.Dir()
	if err != nil {
		return err
	}
	st, err := update.LoadState(dir)
	if err != nil {
		return err
	}
	st.LastCheck = env.Now()
	st.Latest = target.Version
	if err := update.SaveState(dir, st); err != nil {
		return err
	}

	return cli.PrintJSON(env.Stdout, updateCheckResult{
		Current:         version.Version,
		Latest:          target.Version,
		UpdateAvailable: available,
		Method:          string(method),
		Command:         update.UpgradeCommand(method),
	})
}

// runUpdateAuto implements "ajent update --auto on|off|status"
// (docs/tasks/T6.3.md step 3.1).
func runUpdateAuto(env *cli.Env, method update.Method, value string) error {
	switch value {
	case "on", "off", "status":
	default:
		return fmt.Errorf("--auto must be on, off or status: %w", cli.ErrUsage)
	}

	dir, err := env.Dir()
	if err != nil {
		return err
	}
	st, err := update.LoadState(dir)
	if err != nil {
		return err
	}

	if value == "on" || value == "off" {
		enabled := value == "on"
		st.Auto = &enabled
		if err := update.SaveState(dir, st); err != nil {
			return err
		}
	}

	reason := "script install"
	if method != update.MethodScript {
		reason = fmt.Sprintf("managed by %s; use %s", method.Label(), update.UpgradeCommand(method))
	}
	state := "off"
	if st.AutoEnabled(method) {
		state = "on"
	}

	if _, err := fmt.Fprintf(env.Stdout, "automatic updates: %s (%s)\n", state, reason); err != nil {
		return err
	}

	if env.Getenv("AJENT_AUTO_UPDATE") == "0" {
		if _, err := fmt.Fprintln(env.Stdout, "env AJENT_AUTO_UPDATE=0 overrides this"); err != nil {
			return err
		}
	}
	return nil
}
