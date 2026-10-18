// SPDX-License-Identifier: Apache-2.0

package setup

import (
	"context"
	"time"

	"github.com/ajent-social/ajent/internal/cli"
	"github.com/ajent-social/ajent/internal/credentials"
)

// ConfigureScope writes user or project scope settings. Assigned by T2.13
// from cmd/ajent/wire_scope.go. When nil, Run prints a hint to run `ajent
// configure` and continues.
var ConfigureScope func(ctx context.Context, env *cli.Env, root credentials.Credential, scope, domain string, private bool) error

// Integrate configures detected coding tools. Assigned by T4.6 from
// cmd/ajent/wire_integrations.go. When nil, Run skips integrations.
var Integrate func(ctx context.Context, env *cli.Env) error

// PreSetup runs before enrollment, in order; Run returns the first
// non-nil error and stops. T4.9 appends the Python-migration offer from
// internal/setup/migrate.go.
var PreSetup []func(ctx context.Context, env *cli.Env, opts Options) error

// pollInterval, linkTimeout and sleep are test seams for Link's approval
// polling loop (docs/contract.md, "Setup and link flow"). Tests shorten
// pollInterval and linkTimeout and restore both with t.Cleanup; sleep is
// used as given so a short real interval still exercises real timing.
var (
	pollInterval = 2 * time.Second
	linkTimeout  = 10 * time.Minute
	sleep        = func(ctx context.Context, d time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
			return nil
		}
	}
)
