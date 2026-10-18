// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ajent-social/ajent/internal/config"
	"github.com/ajent-social/ajent/internal/credentials"
	"github.com/ajent-social/ajent/internal/setup"
	"github.com/ajent-social/ajent/internal/testutil/fakeserver"
)

// TestSetupHookAssigned confirms that this package's init() wires
// setup.ConfigureScope, so ajent setup and ajent link (T4.3) never see a
// nil hook once cmd/ajent has finished loading.
func TestSetupHookAssigned(t *testing.T) {
	if setup.ConfigureScope == nil {
		t.Fatal("setup.ConfigureScope is nil; wire_scope.go's init() did not run")
	}
}

// TestConfigureScopeForSetupPrivate covers configureScopeForSetup's private
// project path: it must write .aj/config.json under the project root, print
// the private-mode message, and never call the domain-grants route.
func TestConfigureScopeForSetupPrivate(t *testing.T) {
	srv := fakeserver.New(t)
	env, stdout, _, _ := newTestEnv(t, srv)

	if err := configureScopeForSetup(context.Background(), env, srv.Credential(), "project", "", true); err != nil {
		t.Fatalf("configureScopeForSetup: %v", err)
	}

	projectRoot := credentials.ProjectRoot(env.Cwd, env.Home)
	target := filepath.Join(projectRoot, ".aj", "config.json")
	var settings map[string]any
	if err := config.ReadJSON(target, &settings); err != nil {
		t.Fatalf("reading %s: %v", target, err)
	}
	if settings["private_only"] != true {
		t.Errorf("private_only = %v, want true", settings["private_only"])
	}

	if !strings.Contains(stdout.String(), "Private mode saved for this project.") {
		t.Errorf("stdout = %q, missing the private-mode message", stdout.String())
	}

	for _, r := range srv.Requests() {
		if r.Path == "/v1/domain-grants" {
			t.Errorf("unexpected request to /v1/domain-grants: %+v", r)
		}
	}
}

// TestConfigureScopeForSetupDeferred covers the non-interactive,
// no-domain-chosen path: configureScopeForSetup must treat scope.ErrDeferred
// as success, printing the deferred hint instead of returning an error, and
// must not write config.json.
func TestConfigureScopeForSetupDeferred(t *testing.T) {
	srv := fakeserver.New(t)
	env, stdout, _, _ := newTestEnv(t, srv)

	if err := configureScopeForSetup(context.Background(), env, srv.Credential(), "user", "", false); err != nil {
		t.Fatalf("configureScopeForSetup: got error %v, want nil", err)
	}

	if !strings.Contains(stdout.String(), "configure a domain later") {
		t.Errorf("stdout = %q, missing the deferred hint", stdout.String())
	}

	dir, err := env.Dir()
	if err != nil {
		t.Fatalf("env.Dir: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "config.json")); !os.IsNotExist(statErr) {
		t.Errorf("config.json stat error = %v, want IsNotExist", statErr)
	}
}

// TestConfigureScopeForSetupError covers a domain-grant request that comes
// back 403: configureScopeForSetup must return scope.Configure's error
// unchanged, with its "needs human approval" text intact.
func TestConfigureScopeForSetupError(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, _, _ := newTestEnv(t, srv)

	srv.Handle(http.MethodPost, "/v1/domain-grants", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"permission_denied"}`))
	})

	err := configureScopeForSetup(context.Background(), env, srv.Credential(), "user", "example.com", false)
	if err == nil {
		t.Fatal("configureScopeForSetup: got nil error, want an error")
	}
	if !strings.Contains(err.Error(), "needs human approval") {
		t.Errorf("err = %q, missing %q", err.Error(), "needs human approval")
	}
}
