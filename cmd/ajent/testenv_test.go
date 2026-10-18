// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ajent-social/ajent-client/internal/api"
	"github.com/ajent-social/ajent-client/internal/cli"
	"github.com/ajent-social/ajent-client/internal/credentials"
	"github.com/ajent-social/ajent-client/internal/testutil/fakeserver"
)

// newTestEnv builds a cli.Env wired to srv for command tests: buffered
// Stdout and Stderr, fresh t.TempDir()s for Cwd, Home and ConfigDir, a
// fixed Now, a non-interactive terminal, and Resolve stubbed to return
// srv.Credential() for any requested profile while recording it. profiles
// returns a copy of the profiles Resolve has been asked for, in order.
//
// Every command task (T2.7 to T2.11 and later) reuses this helper and must
// not modify this file.
func newTestEnv(t *testing.T, srv *fakeserver.Server) (env *cli.Env, stdout, stderr *bytes.Buffer, profiles func() []string) {
	t.Helper()

	stdout = &bytes.Buffer{}
	stderr = &bytes.Buffer{}

	var resolved []string
	env = &cli.Env{
		Stdin:      strings.NewReader(""),
		Stdout:     stdout,
		Stderr:     stderr,
		Getenv:     os.Getenv,
		Cwd:        t.TempDir(),
		Home:       t.TempDir(),
		ConfigDir:  t.TempDir(),
		API:        &api.Client{Sleep: func(time.Duration) {}},
		IsTerminal: func(uintptr) bool { return false },
		OpenTTY: func() (io.ReadWriteCloser, error) {
			return nil, errors.New("testenv: no controlling terminal")
		},
		Now: func() time.Time {
			return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		},
		Resolve: func(_ context.Context, profile string) (credentials.Credential, error) {
			resolved = append(resolved, profile)
			return srv.Credential(), nil
		},
	}

	profiles = func() []string {
		out := make([]string, len(resolved))
		copy(out, resolved)
		return out
	}
	return env, stdout, stderr, profiles
}

// run calls cli.Main with env and args and returns the exit code.
func run(t *testing.T, env *cli.Env, args ...string) int {
	t.Helper()
	return cli.Main(context.Background(), env, args)
}

// writeRoot writes c as the root credential file directly at
// <env.ConfigDir>/credential.json, with mode 0600. It uses os.WriteFile
// rather than credentials.SaveRoot, which belongs to T2.3.
func writeRoot(t *testing.T, env *cli.Env, c credentials.Credential) {
	t.Helper()

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("writeRoot: marshaling credential: %v", err)
	}
	path := filepath.Join(env.ConfigDir, "credential.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writeRoot: %v", err)
	}
}
