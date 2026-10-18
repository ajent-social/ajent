// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/ajent-social/ajent-client/internal/cli"
	"github.com/ajent-social/ajent-client/internal/credentials"
	"github.com/ajent-social/ajent-client/internal/scope"
	"github.com/ajent-social/ajent-client/internal/setup"
)

// init assigns setup.ConfigureScope, wiring internal/scope (T2.10) into
// ajent setup and ajent link (T4.3) so both go through the same scope
// configuration code path as ajent configure (docs/tasks/T2.13.md).
func init() { setup.ConfigureScope = configureScopeForSetup }

// configureScopeForSetup adapts setup.ConfigureScope's signature to
// scope.Configure, always setting Options.DuringSetup so a non-interactive
// first run defers the domain choice (scope.ErrDeferred) instead of
// failing. On ErrDeferred it prints the deferred hint and returns nil; on
// any other error it returns the error unchanged; on success it prints
// scope.Configure's message.
func configureScopeForSetup(ctx context.Context, env *cli.Env, root credentials.Credential, sc, domain string, private bool) error {
	msg, err := scope.Configure(ctx, env, root, scope.Options{
		Scope:       sc,
		Domain:      domain,
		DomainSet:   domain != "",
		Private:     private,
		DuringSetup: true,
	})
	if errors.Is(err, scope.ErrDeferred) {
		_, _ = fmt.Fprintln(env.Stdout, err.Error())
		return nil
	}
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(env.Stdout, msg)
	return nil
}
