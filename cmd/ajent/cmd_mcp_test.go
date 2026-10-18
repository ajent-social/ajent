// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"testing"

	"github.com/ajent-social/ajent/internal/testutil/fakeserver"
)

// TestRunMCPStdoutPurity proves that a failure before mcp.Serve starts
// (here, no root credential on disk) never writes anything to stdout: MCP
// stdout carries JSON-RPC only (docs/design.md, "Invariants"), so even an
// early, pre-serve failure must report on stderr.
func TestRunMCPStdoutPurity(t *testing.T) {
	srv := fakeserver.New(t)
	env, stdout, stderr, _ := newTestEnv(t, srv)
	// env.ConfigDir is a fresh t.TempDir() with no credential.json.

	err := runMCP(context.Background(), env, nil)
	if err == nil {
		t.Fatalf("runMCP error = nil, want a non-nil error for a missing root credential")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if stderr.Len() == 0 {
		t.Errorf("stderr is empty, want a diagnostic line")
	}
}
