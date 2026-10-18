// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"strings"

	"github.com/ajent-social/ajent-client/internal/api"
	"github.com/ajent-social/ajent-client/internal/cli"
	"github.com/ajent-social/ajent-client/internal/credentials"
)

// profileFlags defines --profile (default "terminal") on fs and returns
// its value. It also defines --project-profile as a bool that is accepted
// and ignored: the CLI always applies the project hash (docs/contract.md
// section 3).
func profileFlags(fs *flag.FlagSet) *string {
	profile := fs.String("profile", "terminal", "tool profile name")
	fs.Bool("project-profile", false, "accepted and ignored: the CLI always applies the project hash")
	return profile
}

// toolCredential resolves (lazily enrolling, through env.Credential) the
// project-scoped credential for base.
func toolCredential(ctx context.Context, env *cli.Env, base string) (credentials.Credential, error) {
	root := credentials.ProjectRoot(env.Cwd, env.Home)
	name := credentials.ProjectProfile(base, root)
	if !credentials.ValidProfile(name) {
		return credentials.Credential{}, credentials.ErrInvalidProfile
	}
	return env.Credential(ctx, name)
}

// rootCredential loads the installation's root credential.
func rootCredential(env *cli.Env) (credentials.Credential, error) {
	dir, err := env.Dir()
	if err != nil {
		return credentials.Credential{}, err
	}
	return credentials.LoadRoot(dir)
}

// getAndPrint issues a GET to route with cred and prints the response as
// indented JSON to env.Stdout.
func getAndPrint(ctx context.Context, env *cli.Env, cred credentials.Credential, route string) error {
	var raw json.RawMessage
	if err := env.API.Do(ctx, cred, api.Request{Method: "GET", Route: route}, &raw); err != nil {
		return err
	}
	return cli.PrintRawJSON(env.Stdout, raw)
}

// sendAndPrint issues r with cred and prints the response as indented JSON
// to env.Stdout.
func sendAndPrint(ctx context.Context, env *cli.Env, cred credentials.Credential, r api.Request) error {
	var raw json.RawMessage
	if err := env.API.Do(ctx, cred, r, &raw); err != nil {
		return err
	}
	return cli.PrintRawJSON(env.Stdout, raw)
}

// errJSONArgsNotObject is returned by jsonArgs when s does not decode to a
// JSON object.
var errJSONArgsNotObject = errors.New("--json must be a JSON object")

// jsonArgs decodes s (a --json flag value) as a JSON object, preserving
// numbers as json.Number. Anything that is not an object returns
// errJSONArgsNotObject.
func jsonArgs(s string) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()

	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, errJSONArgsNotObject
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errJSONArgsNotObject
	}
	return m, nil
}
