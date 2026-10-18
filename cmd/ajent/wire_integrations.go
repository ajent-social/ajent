// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"

	"github.com/ajent-social/ajent-client/internal/cli"
	"github.com/ajent-social/ajent-client/internal/setup"
)

// init assigns setup.Integrate (docs/tasks/T4.6.md step 6), reusing
// "ajent integrations sync"'s own printing path so `ajent setup` and
// `ajent integrations sync` behave identically.
func init() {
	setup.Integrate = func(ctx context.Context, env *cli.Env) error {
		return runIntegrationsSync(ctx, env)
	}
}
