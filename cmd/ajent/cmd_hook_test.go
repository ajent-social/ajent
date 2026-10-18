// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ajent-social/ajent/internal/cli"
	"github.com/ajent-social/ajent/internal/credentials"
	"github.com/ajent-social/ajent/internal/testutil/fakeserver"
)

// hookWriteProfile saves profile as this project's claude-code profile
// credential for root, writing the file directly (as an already-enrolled
// profile) rather than through lazy enrollment, which the hook must never
// trigger.
func hookWriteProfile(t *testing.T, env *cli.Env, root, profile credentials.Credential) {
	t.Helper()

	projectRoot := credentials.ProjectRoot(env.Cwd, env.Home)
	name := credentials.ProjectProfile("claude-code", projectRoot)
	if err := credentials.SaveProfile(env.ConfigDir, root, name, profile); err != nil {
		t.Fatalf("hookWriteProfile: %v", err)
	}
}

func TestHookPrintsGuidanceWithoutCredential(t *testing.T) {
	srv := fakeserver.New(t)
	env, stdout, stderr, _ := newTestEnv(t, srv)

	code := run(t, env, "hook", "session-start")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if stdout.String() != sessionStartText+"\n" {
		t.Errorf("stdout = %q, want %q", stdout.String(), sessionStartText+"\n")
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	if got := len(srv.Requests()); got != 0 {
		t.Errorf("server received %d requests, want 0", got)
	}
}

func TestHookInboxLine(t *testing.T) {
	srv := fakeserver.New(t)
	srv.Handle(http.MethodGet, "/v1/inbox", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":"1"},{"id":"2"}],"history_gap":false}`))
	})
	env, stdout, stderr, _ := newTestEnv(t, srv)

	root := srv.Credential()
	writeRoot(t, env, root)
	hookWriteProfile(t, env, root, credentials.Credential{Server: srv.URL, Token: "aj_0000000000000001_profiletoken"})

	code := run(t, env, "hook", "session-start")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Ajent inbox: 2 pending item(s)") {
		t.Errorf("stdout = %q, want it to contain the inbox line", stdout.String())
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestHookUnreachableServerIsFast(t *testing.T) {
	srv := fakeserver.New(t)
	env, stdout, stderr, _ := newTestEnv(t, srv)

	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	t.Setenv("AJENT_URL", deadURL)

	root := srv.Credential()
	writeRoot(t, env, root)
	hookWriteProfile(t, env, root, credentials.Credential{Server: deadURL, Token: "aj_0000000000000002_deadtoken"})

	start := time.Now()
	code := run(t, env, "hook", "session-start")
	elapsed := time.Since(start)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if elapsed >= 3*time.Second {
		t.Fatalf("hook took %s, want under 3s", elapsed)
	}
	if stdout.String() != sessionStartText+"\n" {
		t.Errorf("stdout = %q, want only the guidance line", stdout.String())
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestHookSlowServerRespectsBudget(t *testing.T) {
	srv := fakeserver.New(t)
	srv.Handle(http.MethodGet, "/v1/inbox", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"history_gap":false}`))
	})
	env, stdout, stderr, _ := newTestEnv(t, srv)

	root := srv.Credential()
	writeRoot(t, env, root)
	hookWriteProfile(t, env, root, credentials.Credential{Server: srv.URL, Token: "aj_0000000000000003_slowtoken"})

	start := time.Now()
	code := run(t, env, "hook", "session-start")
	elapsed := time.Since(start)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if elapsed >= 1500*time.Millisecond {
		t.Fatalf("hook took %s, want under 1.5s", elapsed)
	}
	if stdout.String() != sessionStartText+"\n" {
		t.Errorf("stdout = %q, want only the guidance line (no inbox line)", stdout.String())
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestHookPendingProfileSkipsNetwork(t *testing.T) {
	srv := fakeserver.New(t)
	env, stdout, stderr, _ := newTestEnv(t, srv)

	root := srv.Credential()
	writeRoot(t, env, root)
	pending := credentials.Credential{Server: srv.URL, Token: "aj_0000000000000004_pendingtoken", Pending: true}
	hookWriteProfile(t, env, root, pending)

	code := run(t, env, "hook", "session-start")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if stdout.String() != sessionStartText+"\n" {
		t.Errorf("stdout = %q, want only the guidance line", stdout.String())
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	if got := len(srv.Requests()); got != 0 {
		t.Errorf("server received %d requests, want 0", got)
	}
}

func TestHookExtrasRecoverPanics(t *testing.T) {
	prev := sessionStartExtras
	t.Cleanup(func() { sessionStartExtras = prev })

	var ranSecond bool
	sessionStartExtras = []func(ctx context.Context, env *cli.Env){
		func(context.Context, *cli.Env) { panic("hookTestExtraPanic") },
		func(context.Context, *cli.Env) { ranSecond = true },
	}

	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)

	code := run(t, env, "hook", "session-start")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !ranSecond {
		t.Error("second sessionStartExtras function did not run after the first panicked")
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestHookUsage(t *testing.T) {
	srv := fakeserver.New(t)

	for _, args := range [][]string{
		{"hook"},
		{"hook", "other"},
	} {
		env, _, _, _ := newTestEnv(t, srv)
		if code := run(t, env, args...); code != 2 {
			t.Errorf("run(%v) = %d, want 2", args, code)
		}
	}
}
