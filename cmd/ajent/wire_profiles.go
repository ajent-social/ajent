// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"sync"
	"time"

	"github.com/ajent-social/ajent/internal/cli"
	"github.com/ajent-social/ajent/internal/credentials"
	"github.com/ajent-social/ajent/internal/profiles"
)

func init() {
	wire = append(wire, wireProfiles)
}

// wireProfiles sets env.Resolve to a closure that resolves a tool
// profile's credential through a profiles.Enroller, so both CLI commands
// and the MCP server lazily enroll and re-sync profiles the same way
// (docs/tasks/T3.6.md).
func wireProfiles(env *cli.Env) {
	var (
		once     sync.Once
		enroller *profiles.Enroller
	)

	env.Resolve = func(ctx context.Context, profile string) (credentials.Credential, error) {
		// env.ConfigDir may still be empty at wiring time (DefaultEnv sets
		// it but never creates it, and a caller may leave it empty on
		// purpose); env.Dir() is the only correct way to resolve and
		// create it.
		dir, err := env.Dir()
		if err != nil {
			return credentials.Credential{}, err
		}

		root, err := credentials.LoadRoot(dir)
		if err != nil {
			return credentials.Credential{}, err
		}

		once.Do(func() {
			enroller = &profiles.Enroller{Dir: dir, API: env.API, Now: time.Now}
		})

		projectRoot := credentials.ProjectRoot(env.Cwd, env.Home)
		return enroller.Resolve(ctx, root, profile, projectRoot)
	}
}
