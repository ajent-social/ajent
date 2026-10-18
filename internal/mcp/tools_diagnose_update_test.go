// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"net/http"
	"testing"

	"github.com/ajent-social/ajent-client/internal/testutil/fakeserver"
	"github.com/ajent-social/ajent-client/internal/update"
	"github.com/ajent-social/ajent-client/internal/version"
)

// diagnoseUpdateTestSetVersion sets version.Version to v for the duration
// of the test, restoring the original value on cleanup.
// readToolsDiagnose reads version.Version as the "current" version
// update.DiagnoseFields compares the cached latest release against; the
// unset default "dev" never parses as a semantic version, so
// update.Newer would always report false without this override.
func diagnoseUpdateTestSetVersion(t *testing.T, v string) {
	t.Helper()
	original := version.Version
	version.Version = v
	t.Cleanup(func() { version.Version = original })
}

// TestDiagnoseIncludesUpdateFields proves ajent_diagnose merges the cached
// update fields from update.DiagnoseFields into its response, and that a
// newer cached release reports update_available as true
// (docs/tasks/T6.5.md step 4).
func TestDiagnoseIncludesUpdateFields(t *testing.T) {
	diagnoseUpdateTestSetVersion(t, "0.7.0")
	srv := fakeserver.New(t)
	s := readToolsTestSession(t, srv)

	if err := update.SaveState(s.Dir, update.State{Latest: "99.0.0"}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	got, err := readToolsDiagnose(context.Background(), s, map[string]any{})
	if err != nil {
		t.Fatalf("readToolsDiagnose: %v", err)
	}

	resp, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("readToolsDiagnose returned %T, want map[string]any", got)
	}

	if available, ok := resp["update_available"].(bool); !ok || !available {
		t.Errorf("update_available = %v, want true", resp["update_available"])
	}
	if latest, ok := resp["latest_version"].(string); !ok || latest != "99.0.0" {
		t.Errorf("latest_version = %v, want %q", resp["latest_version"], "99.0.0")
	}
	if _, ok := resp["upgrade_command"].(string); !ok {
		t.Errorf("upgrade_command = %v, want a string", resp["upgrade_command"])
	}

	// The route-level and client-version fields T3.3 already tests must
	// still be present: DiagnoseFields only adds keys, never overwrites.
	if _, ok := resp["local_client_version"]; !ok {
		t.Errorf("local_client_version missing from response")
	}
}

// TestDiagnoseNeverOverwritesExistingKeys proves the merge from
// update.DiagnoseFields never replaces a key the /v1/reliability response
// or the handler itself already set, even if the field names collide
// (docs/tasks/T6.5.md step 4: "Keep the existing keys and never overwrite
// them").
func TestDiagnoseNeverOverwritesExistingKeys(t *testing.T) {
	srv := fakeserver.New(t)
	srv.Handle("GET", "^/v1/reliability$", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"update_available":"server-said-so"}`))
	})
	s := readToolsTestSession(t, srv)

	if err := update.SaveState(s.Dir, update.State{Latest: "99.0.0"}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	got, err := readToolsDiagnose(context.Background(), s, map[string]any{})
	if err != nil {
		t.Fatalf("readToolsDiagnose: %v", err)
	}

	resp, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("readToolsDiagnose returned %T, want map[string]any", got)
	}

	if resp["update_available"] != "server-said-so" {
		t.Errorf("update_available = %v, want the server's original value to survive the merge", resp["update_available"])
	}
}
