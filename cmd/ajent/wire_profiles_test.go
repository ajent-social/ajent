// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ajent-social/ajent/internal/api"
	"github.com/ajent-social/ajent/internal/cli"
	"github.com/ajent-social/ajent/internal/testutil/fakeserver"
)

// TestWireProfilesSetsResolve confirms wireProfiles is registered in wire
// (docs/tasks/OWNERSHIP.md: cmd/ajent/wire_profiles.go is T3.6's seam into
// main()'s env wiring) and that calling it directly sets env.Resolve to a
// closure that resolves through env.Dir(), never env.ConfigDir directly:
// ConfigDir starts empty here and AJENT_CONFIG_DIR supplies the directory
// instead.
func TestWireProfilesSetsResolve(t *testing.T) {
	want := reflect.ValueOf(wireProfiles).Pointer()
	found := false
	for _, w := range wire {
		if reflect.ValueOf(w).Pointer() == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("wire does not contain wireProfiles")
	}

	srv := fakeserver.New(t)
	root := srv.Credential()

	configDir := t.TempDir()
	t.Setenv("AJENT_CONFIG_DIR", configDir)

	data, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("marshaling root credential: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "credential.json"), data, 0o600); err != nil {
		t.Fatalf("writing root credential: %v", err)
	}

	env := &cli.Env{
		Cwd:  t.TempDir(),
		Home: t.TempDir(),
		API:  &api.Client{Sleep: func(time.Duration) {}},
		Now:  time.Now,
		// ConfigDir is deliberately left empty: wireProfiles must resolve
		// it through env.Dir(), which falls back to AJENT_CONFIG_DIR.
	}

	wireProfiles(env)
	if env.Resolve == nil {
		t.Fatal("wireProfiles(env) left env.Resolve nil")
	}

	cred, err := env.Resolve(context.Background(), "wireprofilestest")
	if err != nil {
		t.Fatalf("env.Resolve() err = %v", err)
	}
	if cred.Server != root.Server {
		t.Errorf("resolved credential server = %q, want %q", cred.Server, root.Server)
	}
	if cred.Token == "" {
		t.Error("resolved credential has an empty token")
	}
	if cred.Pending {
		t.Error("resolved credential is still pending")
	}

	fleetRequests := 0
	for _, r := range srv.Requests() {
		if r.Method == http.MethodPost && r.Path == "/v1/fleet" {
			fleetRequests++
		}
	}
	if fleetRequests != 1 {
		t.Errorf("got %d POST /v1/fleet requests, want 1", fleetRequests)
	}
}
