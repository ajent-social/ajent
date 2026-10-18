// SPDX-License-Identifier: Apache-2.0

// Package cli is the command framework shared by every ajent subcommand:
// the Env every command runs with, the Command registry, and Main, which
// dispatches os.Args[1:] to a registered command and turns its error into
// an exit code.
//
// Commands register themselves from init() functions in files under
// cmd/ajent (package main), one file per task, so parallel tasks never
// collide (docs/tasks/OWNERSHIP.md).
//
// Each command builds its own flag.NewFlagSet(name, flag.ContinueOnError)
// with SetOutput(env.Stderr), so flag.Parse's own diagnostics (including a
// requested -h/--help, which returns flag.ErrHelp after the FlagSet already
// printed usage) land on stderr in the flag package's own words. A command
// that finds its own usage error after parsing (for example a required
// positional argument is missing) returns it wrapped with ErrUsage, for
// example:
//
//	return fmt.Errorf("missing QUERY argument: %w", cli.ErrUsage)
//
// Main strips the ErrUsage sentinel text and prints the remaining message
// followed by the failing command's one-line Usage.
//
// Shared flag names that every command reuses exactly (docs/contract.md
// section 2, docs/interfaces.md):
//
//   - --profile (string, default "terminal")
//   - --project-profile (bool)
//   - --json (string, default "{}", structured arguments, not an output
//     switch -- "version" is the one documented exception)
//   - --key
//   - --group
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/ajent-social/ajent-client/internal/api"
	"github.com/ajent-social/ajent-client/internal/credentials"
)

// Env carries every dependency a command needs, so commands never read the
// operating system or the network directly and tests can inject fakes.
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	Cwd, Home      string
	// ConfigDir is always set by DefaultEnv ($AJENT_CONFIG_DIR or
	// <Home>/.aj) but not created. Read-only code may use it directly; code
	// that writes under it calls Dir first.
	ConfigDir string
	API       *api.Client
	// IsTerminal reports whether fd refers to a terminal.
	IsTerminal func(fd uintptr) bool
	// OpenTTY opens the controlling terminal for interactive prompts:
	// /dev/tty on Unix, or CONIN$ for reading plus CONOUT$ for writing,
	// combined into one ReadWriteCloser, on Windows. Tests return an error
	// for a non-interactive environment.
	OpenTTY func() (io.ReadWriteCloser, error)
	Now     func() time.Time
	// Resolve returns the credential for a tool profile (lazily enrolled).
	// cmd/ajent wires it to profiles.Enroller (T3.6, wire_profiles.go);
	// command tests inject a stub, so command tasks do not wait on T3.6.
	Resolve func(ctx context.Context, profile string) (credentials.Credential, error)
}

// Credential returns the credential for profile, through e.Resolve. It
// returns credentials.ErrNoCredential when e.Resolve is nil.
func (e *Env) Credential(ctx context.Context, profile string) (credentials.Credential, error) {
	if e.Resolve == nil {
		return credentials.Credential{}, credentials.ErrNoCredential
	}
	return e.Resolve(ctx, profile)
}

// Command describes one ajent subcommand.
type Command struct {
	Name    string
	Aliases []string
	Summary string
	// Usage is one line, for example "ajent search [--group UUID] QUERY...".
	Usage  string
	Hidden bool
	// Flags optionally returns a fresh FlagSet with the command's flag
	// definitions, used only to print flag defaults for "ajent help
	// <command>". A command's Run builds its own FlagSet separately to
	// parse args.
	Flags func() *flag.FlagSet
	Run   func(ctx context.Context, env *Env, args []string) error
}

// commands maps a command's primary Name to itself. aliases maps an alias
// to the primary Name it resolves to. Both are populated only by Register,
// normally from init() functions at startup.
var (
	commands = make(map[string]Command)
	aliases  = make(map[string]string)
)

// Register adds a command, panicking if its name or any of its aliases is
// already registered as either a command name or an alias. This is a
// programming error, caught by tests, not a runtime condition.
func Register(c Command) {
	if _, exists := commands[c.Name]; exists {
		panic(fmt.Sprintf("cli: command %q is already registered", c.Name))
	}
	if _, exists := aliases[c.Name]; exists {
		panic(fmt.Sprintf("cli: command name %q collides with an existing alias", c.Name))
	}
	for _, a := range c.Aliases {
		if _, exists := commands[a]; exists {
			panic(fmt.Sprintf("cli: alias %q collides with an existing command", a))
		}
		if _, exists := aliases[a]; exists {
			panic(fmt.Sprintf("cli: alias %q is already registered", a))
		}
	}

	commands[c.Name] = c
	for _, a := range c.Aliases {
		aliases[a] = c.Name
	}
}

// lookup finds a command by its primary name or one of its aliases.
func lookup(name string) (Command, bool) {
	if c, ok := commands[name]; ok {
		return c, true
	}
	if target, ok := aliases[name]; ok {
		c, ok := commands[target]
		return c, ok
	}
	return Command{}, false
}

// Commands returns the non-hidden registered commands, sorted by Name.
func Commands() []Command {
	out := make([]Command, 0, len(commands))
	for _, c := range commands {
		if c.Hidden {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ErrUsage is wrapped by a command's Run to report a usage mistake found
// after its flags parsed successfully (for example a missing positional
// argument). Main reports it as a usage error (exit 2) alongside the
// command's Usage line.
var ErrUsage = errors.New("usage")

// UserError is a validation message shown to the user verbatim after the
// "ajent: " prefix, with exit code 1. Commands use it for the exact,
// sentence-case messages fixed in testdata/contract/commands.json, which Go's
// error-string conventions would otherwise lowercase:
//
//	return cli.UserError("Supply a goal UUID.")
type UserError string

func (e UserError) Error() string { return string(e) }

// Main dispatches args (typically os.Args[1:]) to the registered command,
// prints diagnostics to env.Stderr (or the usage listing to env.Stdout on
// success), and returns the process exit code: 0 on success, 1 for a
// runtime error, 2 for a usage error.
func Main(ctx context.Context, env *Env, args []string) int {
	if len(args) == 0 || (len(args) == 1 && isTopLevelHelp(args[0])) {
		writeUsageListing(env.Stdout)
		return 0
	}

	name := args[0]
	cmd, ok := lookup(name)
	if !ok {
		_, _ = fmt.Fprintf(env.Stderr, "ajent: unknown command %q\n", name)
		writeUsageListing(env.Stderr)
		return 2
	}

	err := cmd.Run(ctx, env, args[1:])
	if err == nil {
		return 0
	}

	if errors.Is(err, flag.ErrHelp) {
		// The FlagSet already printed its usage to env.Stderr.
		return 2
	}
	if errors.Is(err, ErrUsage) {
		_, _ = fmt.Fprintf(env.Stderr, "ajent: %s\n", stripUsageSentinel(err))
		_, _ = fmt.Fprintf(env.Stderr, "Usage: %s\n", cmd.Usage)
		return 2
	}

	var apiErr *api.Error
	switch {
	case errors.As(err, &apiErr):
		_, _ = fmt.Fprintln(env.Stderr, apiErr.Error())
	case strings.HasPrefix(err.Error(), "ajent:"):
		_, _ = fmt.Fprintln(env.Stderr, err.Error())
	default:
		_, _ = fmt.Fprintln(env.Stderr, "ajent: "+err.Error())
	}
	return 1
}

// isTopLevelHelp reports whether s is one of the top-level help tokens
// Main recognizes on its own: "-h", "--help" or "help".
func isTopLevelHelp(s string) bool {
	return s == "-h" || s == "--help" || s == "help"
}

// stripUsageSentinel removes the wrapped ErrUsage sentinel text (and the
// wrapping separator a command adds with fmt.Errorf("...: %w", ErrUsage))
// from err's message, leaving the command-supplied context.
func stripUsageSentinel(err error) string {
	msg := strings.TrimSuffix(err.Error(), ErrUsage.Error())
	return strings.TrimRight(msg, ": ")
}

// writeUsageListing writes the top-level usage listing to w: "Usage: ajent
// <command> [flags]", a blank line, each non-hidden command as its name
// padded to 14 columns followed by two spaces and its Summary, then a
// pointer to "ajent help <command>".
func writeUsageListing(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage: ajent <command> [flags]")
	_, _ = fmt.Fprintln(w)
	for _, c := range Commands() {
		_, _ = fmt.Fprintf(w, "%-14s  %s\n", c.Name, c.Summary)
	}
	_, _ = fmt.Fprintln(w, "Run 'ajent help <command>' for details.")
}
