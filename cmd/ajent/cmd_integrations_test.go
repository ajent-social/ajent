// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ajent-social/ajent/internal/integrations"
	"github.com/ajent-social/ajent/internal/testutil/fakeserver"
)

// integrationsFakeWriter is a minimal Writer used only by
// TestIntegrationsCommandStatusSyncRollback: it always detects, and its
// Apply writes a small marker file under the config dir.
type integrationsFakeWriter struct{ name string }

func (w integrationsFakeWriter) Target() integrations.Target {
	return integrations.Target{Name: w.name}
}

func (w integrationsFakeWriter) Detect(integrations.Params) bool { return true }

func (w integrationsFakeWriter) Paths(p integrations.Params) []string {
	return []string{filepath.Join(p.ConfigDir, w.name+"-config.json")}
}

func (w integrationsFakeWriter) Apply(_ context.Context, p integrations.Params) (integrations.Result, error) {
	path := w.Paths(p)[0]
	if err := os.WriteFile(path, []byte(`{"configured":true}`), 0o644); err != nil {
		return integrations.Result{}, err
	}
	return integrations.Result{Name: w.name, Status: "configured"}, nil
}

func (w integrationsFakeWriter) Remove(context.Context, integrations.Params) (integrations.Result, error) {
	return integrations.Result{Name: w.name, Status: "removed"}, nil
}

// integrationsSyncBackupLine extracts the backup ID printed by
// "ajent integrations sync"'s "Integration backup: <ID>. Roll back with:
// ajent rollback <ID>" line.
var integrationsSyncBackupLine = regexp.MustCompile(`Integration backup: (\S+)\. Roll back with: ajent rollback (\S+)`)

func TestIntegrationsCommandStatusSyncRollback(t *testing.T) {
	prevBinary := integrations.BinaryOverride
	integrations.BinaryOverride = "/usr/local/bin/ajent"
	t.Cleanup(func() { integrations.BinaryOverride = prevBinary })

	defer integrations.ResetForTest()()
	integrations.Register(integrationsFakeWriter{name: "claude"})

	srv := fakeserver.New(t)
	env, stdout, stderr, _ := newTestEnv(t, srv)

	// status before any sync: the fake writer is always detected, and
	// there is no recorded sync yet.
	if code := run(t, env, "integrations", "status"); code != 0 {
		t.Fatalf("integrations status exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"claude"`) {
		t.Fatalf("status output missing detected claude: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"last": null`) {
		t.Fatalf("status output missing null last: %s", stdout.String())
	}

	// sync applies the fake writer (creating its config file) and prints
	// the backup ID.
	stdout.Reset()
	if code := run(t, env, "integrations", "sync"); code != 0 {
		t.Fatalf("integrations sync exit = %d, stderr = %s", code, stderr.String())
	}
	syncOut := stdout.String()
	if !strings.Contains(syncOut, "claude: configured") {
		t.Fatalf("sync output missing claude result: %s", syncOut)
	}
	if !strings.Contains(syncOut, "Reload MCP connections or start new agent sessions to pick up the integration.") {
		t.Fatalf("sync output missing reload line: %s", syncOut)
	}
	m := integrationsSyncBackupLine.FindStringSubmatch(syncOut)
	if m == nil {
		t.Fatalf("sync output missing backup line: %s", syncOut)
	}
	backupID := m[1]

	configPath := filepath.Join(env.ConfigDir, "claude-config.json")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("writer config file missing after sync: %v", err)
	}

	// Confirm status now reports the recorded sync.
	stdout.Reset()
	if code := run(t, env, "integrations", "status"); code != 0 {
		t.Fatalf("integrations status exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), backupID) {
		t.Fatalf("status output missing recorded backup id %s: %s", backupID, stdout.String())
	}

	// Mutate the writer's config file, then roll back to the pre-sync
	// snapshot, which recorded the file as absent.
	if err := os.WriteFile(configPath, []byte(`{"mutated":true}`), 0o644); err != nil {
		t.Fatalf("mutate config: %v", err)
	}

	stdout.Reset()
	if code := run(t, env, "rollback", backupID); code != 0 {
		t.Fatalf("rollback exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Restored installation backup "+backupID) {
		t.Fatalf("rollback output = %s", stdout.String())
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("rollback did not remove the file Apply created, stat err = %v", err)
	}

	// A bad subcommand is a usage error, exit 2.
	stdout.Reset()
	stderr.Reset()
	if code := run(t, env, "integrations", "bogus"); code != 2 {
		t.Fatalf("integrations bogus exit = %d, want 2, stderr = %s", code, stderr.String())
	}
}

func TestIntegrationsCommandDisabledByEnv(t *testing.T) {
	prevBinary := integrations.BinaryOverride
	integrations.BinaryOverride = "/usr/local/bin/ajent"
	t.Cleanup(func() { integrations.BinaryOverride = prevBinary })

	defer integrations.ResetForTest()()
	integrations.Register(integrationsFakeWriter{name: "claude"})

	tests := []struct {
		name   string
		action string
	}{
		{"sync", "sync"},
		{"remove", "remove"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, stdout, stderr, _ := newTestEnv(t, fakeserver.New(t))
			getenv := env.Getenv
			env.Getenv = func(k string) string {
				if k == "AJENT_INTEGRATIONS" {
					return "0"
				}
				return getenv(k)
			}
			if code := run(t, env, "integrations", tt.action); code != 0 {
				t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if !strings.Contains(stderr.String(), "AJENT_INTEGRATIONS=0") {
				t.Errorf("stderr = %q, want the skip note", stderr.String())
			}
			for _, name := range []string{"claude-config.json", "backups", "integration-status.json"} {
				if _, err := os.Stat(filepath.Join(env.ConfigDir, name)); !os.IsNotExist(err) {
					t.Errorf("%s exists (err %v), want nothing written", name, err)
				}
			}
		})
	}
}
