// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"

	"github.com/ajent-social/ajent-client/internal/api"
	"github.com/ajent-social/ajent-client/internal/cli"
)

func init() {
	cli.Register(cli.Command{
		Name:    "login",
		Summary: "Print a short-lived link to sign in to Ajent in your browser",
		Usage:   "ajent login [--open]",
		Flags:   loginFlags,
		Run:     runLogin,
	})
}

// errLoginUnexpectedLink is returned when the service's browser-login
// response does not point back at the root credential's own server origin
// (docs/tasks/T4.10.md step 4, ADR 006).
var errLoginUnexpectedLink = errors.New("login: the service returned an unexpected link")

// loginOpenFailedMessage is printed to env.Stderr when --open could not
// launch a browser. The command still succeeds: the printed link on stdout
// remains usable (docs/tasks/T4.10.md step 6).
const loginOpenFailedMessage = "Could not open a browser; open the link above."

// loginResponse is the decoded POST /v1/browser-login response body
// (docs/contract.md "POST /v1/browser-login").
type loginResponse struct {
	URL string `json:"url"`
}

// newLoginFlagSet builds "login"'s FlagSet and returns the --open value.
func newLoginFlagSet() (fs *flag.FlagSet, open *bool) {
	fs = flag.NewFlagSet("login", flag.ContinueOnError)
	open = fs.Bool("open", false, "also open the link in the default browser")
	return fs, open
}

// loginFlags returns a fresh FlagSet with "login"'s flag definitions, used
// by "ajent help login".
func loginFlags() *flag.FlagSet {
	fs, _ := newLoginFlagSet()
	return fs
}

// runLogin implements "ajent login [--open]": it asks the service for a
// single-use browser sign-in URL with the root credential and prints it.
// With --open it also opens the link in the default browser
// (docs/tasks/T4.10.md).
func runLogin(ctx context.Context, env *cli.Env, args []string) error {
	fs, open := newLoginFlagSet()
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	root, err := rootCredential(env)
	if err != nil {
		return err
	}

	var out loginResponse
	req := api.Request{Method: "POST", Route: "/v1/browser-login", Body: map[string]any{}}
	if err := env.API.Do(ctx, root, req, &out); err != nil {
		return err
	}

	if err := loginValidateURL(out.URL, root.Server); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(env.Stdout, out.URL)

	if *open {
		if err := openBrowser(ctx, out.URL); err != nil {
			_, _ = fmt.Fprintln(env.Stderr, loginOpenFailedMessage)
		}
	}
	return nil
}

// loginValidateURL reports errLoginUnexpectedLink unless raw parses as a
// URL whose scheme is https (or http only when server's own origin is
// loopback http) and whose scheme and host exactly equal server's
// (docs/tasks/T4.10.md step 4, ADR 006: exact-origin pinning applies to
// the link the service hands back, not only to outgoing requests).
func loginValidateURL(raw, server string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errLoginUnexpectedLink
	}
	origin, err := url.Parse(server)
	if err != nil {
		return errLoginUnexpectedLink
	}

	loopbackHTTP := origin.Scheme == "http" && loginIsLoopbackHost(origin.Hostname())
	httpsScheme := u.Scheme == "https"
	loopbackHTTPScheme := u.Scheme == "http" && loopbackHTTP
	if !httpsScheme && !loopbackHTTPScheme {
		return errLoginUnexpectedLink
	}
	if u.Scheme != origin.Scheme || u.Host != origin.Host {
		return errLoginUnexpectedLink
	}
	return nil
}

// loginIsLoopbackHost reports whether host is one of the loopback
// hostnames accepted for plain http (docs/contract.md section 6). This
// package cannot reuse internal/api's private check: that one reads
// $AJENT_URL, while this one classifies the root credential's own server
// field.
func loginIsLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// browserCommand returns the executable name and argument vector used to
// open a URL in the operating system's default browser. Tests replace it
// with a recorder (docs/tasks/T4.10.md step 7).
var browserCommand = defaultBrowserCommand

// defaultBrowserCommand chooses the per-GOOS browser-opening command. u is
// always returned as a single argv element, never interpolated into a
// shell string (ADR 006, docs/tasks/T4.10.md step 6).
func defaultBrowserCommand(u string) (name string, args []string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{u}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", u}
	default:
		return "xdg-open", []string{u}
	}
}

// openBrowser starts browserCommand(u) without a shell and without waiting
// for it to exit (docs/tasks/T4.10.md step 6).
func openBrowser(ctx context.Context, u string) error {
	name, args := browserCommand(u)
	//nolint:gosec // G204: name and args come only from browserCommand, a
	// fixed per-GOOS argv vector, never a shell string or user input.
	return exec.CommandContext(ctx, name, args...).Start()
}
