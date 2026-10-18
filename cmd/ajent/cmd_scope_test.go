// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/ajent-social/ajent/internal/credentials"
	"github.com/ajent-social/ajent/internal/testutil/fakeserver"
)

func TestDomainsUsesRoot(t *testing.T) {
	srv := fakeserver.New(t)
	env, stdout, stderr, _ := newTestEnv(t, srv)
	writeRoot(t, env, srv.Credential())
	env.Resolve = func(context.Context, string) (credentials.Credential, error) {
		t.Fatal("domains must not resolve a tool profile")
		return credentials.Credential{}, nil
	}

	code := run(t, env, "domains")
	if code != 0 {
		t.Fatalf("run() = %d, stderr = %q", code, stderr.String())
	}
	if stdout.Len() == 0 {
		t.Error("stdout is empty")
	}
	reqs := srv.Requests()
	if len(reqs) != 1 || reqs[0].Method != http.MethodGet || reqs[0].Path != "/v1/domains" {
		t.Errorf("requests = %+v, want one GET /v1/domains", reqs)
	}
}

func TestRevokeGrantValidation(t *testing.T) {
	t.Run("bad id", func(t *testing.T) {
		srv := fakeserver.New(t)
		env, _, stderr, _ := newTestEnv(t, srv)
		writeRoot(t, env, srv.Credential())

		code := run(t, env, "revoke-grant", "not-a-uuid")
		if code != 1 {
			t.Fatalf("run() = %d, stderr = %q", code, stderr.String())
		}
		if reqs := srv.Requests(); len(reqs) != 0 {
			t.Errorf("requests = %+v, want none", reqs)
		}
	})

	t.Run("good id", func(t *testing.T) {
		srv := fakeserver.New(t)
		env, stdout, stderr, _ := newTestEnv(t, srv)
		writeRoot(t, env, srv.Credential())

		code := run(t, env, "revoke-grant", "00000000-0000-4000-8000-0000000000AB")
		if code != 0 {
			t.Fatalf("run() = %d, stderr = %q", code, stderr.String())
		}
		reqs := srv.Requests()
		wantPath := "/v1/domain-grants/00000000-0000-4000-8000-0000000000ab"
		if len(reqs) != 1 || reqs[0].Method != http.MethodDelete || reqs[0].Path != wantPath {
			t.Errorf("requests = %+v, want one DELETE %s", reqs, wantPath)
		}
		if stdout.Len() == 0 {
			t.Error("stdout is empty")
		}
	})
}

func TestConfigureBadScopeFlag(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)
	writeRoot(t, env, srv.Credential())

	code := run(t, env, "configure", "--scope", "bogus")
	if code != 2 {
		t.Fatalf("run() = %d, stderr = %q, want 2", code, stderr.String())
	}
	if reqs := srv.Requests(); len(reqs) != 0 {
		t.Errorf("requests = %+v, want none", reqs)
	}
}

// TestScopeFixtureMessages asserts the exact stderr line for every
// testdata/contract/commands.json validation error case owned by T2.10:
// "configure" cases no_scope, no_domain, bad_domain and needs_approval, and
// "revoke-grant" case no_id.
func TestScopeFixtureMessages(t *testing.T) {
	t.Run("configure no_scope", func(t *testing.T) {
		srv := fakeserver.New(t)
		env, _, stderr, _ := newTestEnv(t, srv)
		writeRoot(t, env, srv.Credential())

		code := run(t, env, "configure")
		want := "ajent: Choose --scope user or --scope project.\n"
		if code != 1 || stderr.String() != want {
			t.Fatalf("run() = %d, stderr = %q; want 1, %q", code, stderr.String(), want)
		}
	})

	t.Run("configure no_domain", func(t *testing.T) {
		srv := fakeserver.New(t)
		env, _, stderr, _ := newTestEnv(t, srv)
		writeRoot(t, env, srv.Credential())

		code := run(t, env, "configure", "--scope", "user")
		want := "ajent: Choose --domain DOMAIN or --private.\n"
		if code != 1 || stderr.String() != want {
			t.Fatalf("run() = %d, stderr = %q; want 1, %q", code, stderr.String(), want)
		}
	})

	t.Run("configure bad_domain", func(t *testing.T) {
		srv := fakeserver.New(t)
		env, _, stderr, _ := newTestEnv(t, srv)
		writeRoot(t, env, srv.Credential())

		code := run(t, env, "configure", "--scope", "user", "--domain", "not_a_domain")
		want := "ajent: Enter a DNS domain without a URL or port.\n"
		if code != 1 || stderr.String() != want {
			t.Fatalf("run() = %d, stderr = %q; want 1, %q", code, stderr.String(), want)
		}
		if reqs := srv.Requests(); len(reqs) != 0 {
			t.Errorf("requests = %+v, want none", reqs)
		}
	})

	t.Run("configure needs_approval", func(t *testing.T) {
		srv := fakeserver.New(t)
		env, _, stderr, _ := newTestEnv(t, srv)
		writeRoot(t, env, srv.Credential())
		srv.Handle(http.MethodPost, "/v1/domain-grants", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"permission_denied"}`))
		})

		code := run(t, env, "configure", "--scope", "user", "--domain", "example.com")
		want := "ajent: This scope needs human approval. Run ajent link, sign in and approve the domain scope.\n"
		if code != 1 || stderr.String() != want {
			t.Fatalf("run() = %d, stderr = %q; want 1, %q", code, stderr.String(), want)
		}
	})

	t.Run("revoke-grant no_id", func(t *testing.T) {
		srv := fakeserver.New(t)
		env, _, stderr, _ := newTestEnv(t, srv)
		writeRoot(t, env, srv.Credential())

		code := run(t, env, "revoke-grant")
		want := "ajent: Supply the grant ID shown by ajent domains.\n"
		if code != 1 || stderr.String() != want {
			t.Fatalf("run() = %d, stderr = %q; want 1, %q", code, stderr.String(), want)
		}
	})
}
