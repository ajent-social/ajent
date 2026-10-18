// SPDX-License-Identifier: Apache-2.0

package main

import (
	"github.com/ajent-social/ajent-client/internal/cli"
	"github.com/ajent-social/ajent-client/internal/feedback"
)

func init() {
	wire = append(wire, feedbackWireObserver)
}

// feedbackWireObserver sets env.API.Observer to a feedback.Store's
// Observer, rooted at env.ConfigDir. It does not call env.Dir(), so
// read-only commands such as "ajent version" create no filesystem state:
// the store creates its own directory lazily on first use
// (feedback.(*Store).with, called only when a request actually needs
// capturing) (docs/tasks/T2.11.md step 5).
func feedbackWireObserver(env *cli.Env) {
	env.API.Observer = (&feedback.Store{Dir: env.ConfigDir}).Observer()
}
