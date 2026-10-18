// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ajent-social/ajent-client/internal/testutil/fakeserver"
)

// loginBrowserCall records one (name, args) pair a stand-in browserCommand
// computed for an openBrowser call, captured by loginBrowserRecorder.
type loginBrowserCall struct {
	name string
	args []string
}

// loginNoopBrowser is a deliberately nonexistent executable name.
// loginBrowserRecorder returns it in place of the real per-GOOS command so
// openBrowser's exec.CommandContext(...).Start() always fails the PATH
// lookup instead of spawning a real browser: tests must never have a side
// effect on the host (AGENTS.md, HOST SAFETY).
const loginNoopBrowser = "ajent-login-test-nonexistent-browser"

// loginBrowserRecorder returns a browserCommand replacement that appends
// the real per-GOOS (name, args) defaultBrowserCommand chooses for u to
// *calls, then substitutes loginNoopBrowser so no real browser ever opens
// during the test (docs/tasks/T4.10.md step 7).
func loginBrowserRecorder(calls *[]loginBrowserCall) func(string) (string, []string) {
	return func(u string) (string, []string) {
		name, args := defaultBrowserCommand(u)
		*calls = append(*calls, loginBrowserCall{name: name, args: args})
		return loginNoopBrowser, nil
	}
}

// loginWithBrowserRecorder swaps browserCommand for a recorder for the
// duration of one test and restores the original on cleanup.
func loginWithBrowserRecorder(t *testing.T) *[]loginBrowserCall {
	t.Helper()
	calls := &[]loginBrowserCall{}
	orig := browserCommand
	browserCommand = loginBrowserRecorder(calls)
	t.Cleanup(func() { browserCommand = orig })
	return calls
}

// loginWantCommand computes, independently of defaultBrowserCommand's own
// switch, the (name, args) this test's runtime.GOOS should produce for u,
// so a regression in the production switch is caught rather than
// tautologically confirmed (docs/tasks/T4.10.md step 6).
func loginWantCommand(u string) (name string, args []string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{u}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", u}
	default:
		return "xdg-open", []string{u}
	}
}

// loginRespondURL registers a handler answering POST /v1/browser-login
// with {"url": url}.
func loginRespondURL(srv *fakeserver.Server, url string) {
	srv.Handle(http.MethodPost, "/v1/browser-login", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"` + url + `"}`))
	})
}

func TestLoginPrintsURL(t *testing.T) {
	srv := fakeserver.New(t)
	wantURL := srv.URL + "/human/login?token=example-single-use"
	loginRespondURL(srv, wantURL)

	env, stdout, stderr, _ := newTestEnv(t, srv)
	root := srv.Credential()
	writeRoot(t, env, root)

	if code := run(t, env, "login"); code != 0 {
		t.Fatalf("run() = %d, want 0; stderr = %q", code, stderr.String())
	}

	if got := stdout.String(); got != wantURL+"\n" {
		t.Errorf("stdout = %q, want %q", got, wantURL+"\n")
	}
	if strings.Contains(stdout.String(), root.Token) {
		t.Fatalf("stdout leaked the token: %q", stdout.String())
	}
	if strings.Contains(stderr.String(), root.Token) {
		t.Fatalf("stderr leaked the token: %q", stderr.String())
	}

	reqs := srv.Requests()
	if len(reqs) == 0 {
		t.Fatalf("no requests recorded")
	}
	got := reqs[len(reqs)-1]
	if got.Method != http.MethodPost || got.Path != "/v1/browser-login" {
		t.Fatalf("request = %s %s, want POST /v1/browser-login", got.Method, got.Path)
	}
	if strings.TrimSpace(string(got.Body)) != "{}" {
		t.Errorf("body = %s, want {}", got.Body)
	}
	if auth := got.Header.Get("Authorization"); auth != "Bearer "+root.Token {
		t.Errorf("Authorization = %q, want %q", auth, "Bearer "+root.Token)
	}
}

func TestLoginOpen(t *testing.T) {
	calls := loginWithBrowserRecorder(t)

	srv := fakeserver.New(t)
	wantURL := srv.URL + "/human/login?token=example-single-use"
	loginRespondURL(srv, wantURL)

	env, stdout, stderr, _ := newTestEnv(t, srv)
	writeRoot(t, env, srv.Credential())

	if code := run(t, env, "login", "--open"); code != 0 {
		t.Fatalf("run() = %d, want 0; stderr = %q", code, stderr.String())
	}
	if got := stdout.String(); got != wantURL+"\n" {
		t.Errorf("stdout = %q, want %q", got, wantURL+"\n")
	}

	if len(*calls) != 1 {
		t.Fatalf("browserCommand calls = %d, want 1", len(*calls))
	}
	wantName, wantArgs := loginWantCommand(wantURL)
	got := (*calls)[0]
	if got.name != wantName || !reflect.DeepEqual(got.args, wantArgs) {
		t.Errorf("browserCommand call = (%q, %v), want (%q, %v)", got.name, got.args, wantName, wantArgs)
	}
	if !browserArgsEndInURL(got.args, wantURL) {
		t.Errorf("browser args = %v, want the URL as the final argument", got.args)
	}
}

func TestLoginRejectsForeignURL(t *testing.T) {
	calls := loginWithBrowserRecorder(t)

	srv := fakeserver.New(t)
	loginRespondURL(srv, "https://evil.example/x")

	env, stdout, stderr, _ := newTestEnv(t, srv)
	writeRoot(t, env, srv.Credential())

	code := run(t, env, "login", "--open")
	if code != 1 {
		t.Fatalf("run() = %d, want 1; stderr = %q", code, stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unexpected link") {
		t.Errorf("stderr = %q, want it to mention an unexpected link", stderr.String())
	}
	if len(*calls) != 0 {
		t.Errorf("browserCommand calls = %v, want none opened", *calls)
	}
}

func TestLoginNoCredential(t *testing.T) {
	srv := fakeserver.New(t)
	env, stdout, stderr, _ := newTestEnv(t, srv)

	code := run(t, env, "login")
	if code != 1 {
		t.Fatalf("run() = %d, want 1; stderr = %q", code, stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "ajent setup") {
		t.Errorf("stderr = %q, want it to mention %q", stderr.String(), "ajent setup")
	}

	for _, req := range srv.Requests() {
		if req.Method == http.MethodPost {
			t.Errorf("unexpected POST request with no credential: %+v", req)
		}
	}
}

func TestLoginURLWithShellMetacharacters(t *testing.T) {
	calls := loginWithBrowserRecorder(t)

	srv := fakeserver.New(t)
	wantURL := srv.URL + "/l?a=1&b=$(x)"
	loginRespondURL(srv, wantURL)

	env, stdout, stderr, _ := newTestEnv(t, srv)
	writeRoot(t, env, srv.Credential())

	if code := run(t, env, "login", "--open"); code != 0 {
		t.Fatalf("run() = %d, want 0; stderr = %q", code, stderr.String())
	}
	if got := stdout.String(); got != wantURL+"\n" {
		t.Errorf("stdout = %q, want %q", got, wantURL+"\n")
	}

	if len(*calls) != 1 {
		t.Fatalf("browserCommand calls = %d, want 1", len(*calls))
	}
	args := (*calls)[0].args
	if !browserArgsEndInURL(args, wantURL) {
		t.Errorf("browser args = %v, want the URL as the final argument, unchanged", args)
	}
}

func browserArgsEndInURL(args []string, wantURL string) bool {
	return len(args) > 0 && args[len(args)-1] == wantURL &&
		(len(args) == 1 || (runtime.GOOS == "windows" && len(args) == 2 && args[0] == "url.dll,FileProtocolHandler"))
}
