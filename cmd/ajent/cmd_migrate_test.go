// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ajent-social/ajent-client/internal/credentials"
	"github.com/ajent-social/ajent-client/internal/integrations"
	"github.com/ajent-social/ajent-client/internal/testutil/fakeserver"
)

func TestMigrateCommandNoPython(t *testing.T) {
	env, stdout, stderr, _ := newTestEnv(t, fakeserver.New(t))

	if code := run(t, env, "migrate"); code != 0 {
		t.Fatalf("migrate exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No earlier Python installation found.") {
		t.Errorf("stdout = %q, want the no-python message", stdout.String())
	}
}

func TestMigrateCommandDryRunThenMigrate(t *testing.T) {
	prevBinary := integrations.BinaryOverride
	integrations.BinaryOverride = "/opt/ajent/bin/ajent"
	t.Cleanup(func() { integrations.BinaryOverride = prevBinary })

	prevLookPath := integrations.LookPath
	integrations.LookPath = func(string) (string, error) {
		return "", fmt.Errorf("migrate command test: %w", fs.ErrNotExist)
	}
	t.Cleanup(func() { integrations.LookPath = prevLookPath })

	srv := fakeserver.New(t)
	env, stdout, stderr, _ := newTestEnv(t, srv)

	if err := os.WriteFile(filepath.Join(env.ConfigDir, "client.py"), []byte("# earlier client placeholder\n"), 0o600); err != nil {
		t.Fatalf("write client.py: %v", err)
	}
	if err := credentials.CreateRoot(env.ConfigDir, srv.Credential()); err != nil {
		t.Fatalf("CreateRoot: %v", err)
	}

	if code := run(t, env, "migrate", "--dry-run"); code != 0 {
		t.Fatalf("migrate --dry-run exit = %d, stderr = %s", code, stderr.String())
	}
	dryOut := stdout.String()
	if !strings.Contains(dryOut, "Planned changes:") {
		t.Errorf("dry-run stdout missing header: %s", dryOut)
	}
	if !strings.Contains(dryOut, "- keep Python client files in "+env.ConfigDir) {
		t.Errorf("dry-run stdout missing the keep-files line: %s", dryOut)
	}
	if _, err := os.Stat(filepath.Join(env.ConfigDir, "migration.json")); !os.IsNotExist(err) {
		t.Errorf("--dry-run wrote migration.json (stat err = %v)", err)
	}

	stdout.Reset()
	if code := run(t, env, "migrate"); code != 0 {
		t.Fatalf("migrate exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Migration complete. Roll back with: ajent rollback ") {
		t.Errorf("migrate stdout missing the completion line: %s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(env.ConfigDir, "migration.json")); err != nil {
		t.Errorf("migration.json missing after migrate: %v", err)
	}
}
