// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"context"
	"os/exec"
	"time"
)

// runTimeout bounds every vendor CLI invocation made through Run.
const runTimeout = 30 * time.Second

// Runner executes a vendor CLI and returns its combined output. Callers
// never print the output: vendor output can contain other MCP secrets.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Run is the exec seam. Tests replace it to avoid running real vendor CLIs.
var Run Runner = defaultRunner

// LookPath is the exec.LookPath seam. Tests replace it to simulate an
// installed or missing vendor CLI.
var LookPath = exec.LookPath

func defaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: writers pass a fixed vendor CLI name and literal arguments.
	return cmd.CombinedOutput()
}
