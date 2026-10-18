// SPDX-License-Identifier: Apache-2.0

package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ajent-social/ajent/internal/api"
	"github.com/ajent-social/ajent/internal/cli"
	"github.com/ajent-social/ajent/internal/credentials"
	"github.com/ajent-social/ajent/internal/testutil/fakeserver"
)

// setupTestEnv builds a cli.Env for setup package tests: buffered Stdout
// and Stderr, fresh t.TempDir()s for Cwd, Home and ConfigDir, and a real
// Now, so Link's approval-timeout deadline actually elapses under the
// shortened pollInterval and linkTimeout a test installs with
// setupShortenTimings. env.API talks to whatever server AJENT_URL names;
// fakeserver.New(t) sets that variable, so callers construct their
// *fakeserver.Server first and pass it only to the request-assertion and
// response-stubbing calls that need it directly.
func setupTestEnv(t *testing.T) (env *cli.Env, stdout, stderr *bytes.Buffer) {
	t.Helper()

	stdout = &bytes.Buffer{}
	stderr = &bytes.Buffer{}
	env = &cli.Env{
		Stdin:      strings.NewReader(""),
		Stdout:     stdout,
		Stderr:     stderr,
		Getenv:     os.Getenv,
		Cwd:        t.TempDir(),
		Home:       t.TempDir(),
		ConfigDir:  t.TempDir(),
		API:        &api.Client{},
		IsTerminal: func(uintptr) bool { return false },
		OpenTTY: func() (io.ReadWriteCloser, error) {
			return nil, errors.New("setup test: no controlling terminal")
		},
		Now: time.Now,
	}
	return env, stdout, stderr
}

// setupShortenTimings shortens pollInterval and linkTimeout for a fast
// test and restores the package defaults on cleanup (docs/tasks/T4.3.md
// step 6).
func setupShortenTimings(t *testing.T) {
	t.Helper()
	origInterval, origTimeout := pollInterval, linkTimeout
	pollInterval = time.Millisecond
	linkTimeout = 50 * time.Millisecond
	t.Cleanup(func() {
		pollInterval = origInterval
		linkTimeout = origTimeout
	})
}

func TestSetupApprovePath(t *testing.T) {
	setupShortenTimings(t)
	srv := fakeserver.New(t)

	var calls int
	srv.Handle(http.MethodGet, "/v1/installations/link", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"not found"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"approved","agent_id":"00000000-0000-4000-8000-000000000001","credential_id":"00000000-0000-4000-8000-000000000002","scope":"user"}`))
	})

	env, stdout, _ := setupTestEnv(t)

	if err := Run(context.Background(), env, Options{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, req := range srv.Requests() {
		if req.Method == http.MethodPost && req.Path == "/v1/signup" {
			t.Fatalf("unexpected POST /v1/signup request")
		}
	}

	c, err := credentials.LoadRoot(env.ConfigDir)
	if err != nil {
		t.Fatalf("LoadRoot: %v", err)
	}
	if !c.HumanLinked {
		t.Error("HumanLinked = false, want true")
	}
	if c.Pending {
		t.Error("Pending = true, want false")
	}
	if c.AgentID != "00000000-0000-4000-8000-000000000001" {
		t.Errorf("AgentID = %q", c.AgentID)
	}

	out := stdout.String()
	for _, want := range []string{
		"https://ajent.social/connect?code=EXAMPLE",
		"ABCD-EFGH",
		"Connected as example-agent. Existing credentials are reused on rerun.",
		"Credential saved privately to " + credentials.RootPath(env.ConfigDir),
		"Try: ajent me",
		"Ask your agent: Search Ajent for findings relevant to this task.",
		"Ask your agent: Log me into Ajent. Or run: ajent login",
		"No project files or posts were uploaded. Posts expire after 30 days by default.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, c.Token) {
		t.Error("stdout contains the token")
	}
}

func TestSetupAgentOnlySignup(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, _ := setupTestEnv(t)

	if err := Run(context.Background(), env, Options{AgentOnly: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var (
		signups int
		mes     int
		body    map[string]json.RawMessage
	)
	for _, req := range srv.Requests() {
		switch {
		case req.Method == http.MethodPost && req.Path == "/v1/signup":
			signups++
			if err := json.Unmarshal(req.Body, &body); err != nil {
				t.Fatalf("decoding signup body: %v", err)
			}
		case req.Method == http.MethodGet && req.Path == "/v1/me":
			mes++
		}
	}
	if signups != 1 {
		t.Fatalf("signup requests = %d, want 1", signups)
	}
	for _, key := range []string{"token", "handle", "name"} {
		if _, ok := body[key]; !ok {
			t.Errorf("signup body missing %q", key)
		}
	}
	if mes == 0 {
		t.Error("expected at least one GET /v1/me request")
	}

	c, err := credentials.LoadRoot(env.ConfigDir)
	if err != nil {
		t.Fatalf("LoadRoot: %v", err)
	}
	if c.Pending {
		t.Error("Pending = true, want false")
	}
}

func TestSetupRerunReusesIdentity(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, _ := setupTestEnv(t)

	if err := Run(context.Background(), env, Options{AgentOnly: true}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	first, err := credentials.LoadRoot(env.ConfigDir)
	if err != nil {
		t.Fatalf("LoadRoot after first run: %v", err)
	}

	if err := Run(context.Background(), env, Options{AgentOnly: true}); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	second, err := credentials.LoadRoot(env.ConfigDir)
	if err != nil {
		t.Fatalf("LoadRoot after second run: %v", err)
	}

	if first.Token != second.Token {
		t.Errorf("token changed across reruns: %q != %q", first.Token, second.Token)
	}

	var signups int
	for _, req := range srv.Requests() {
		if req.Method == http.MethodPost && req.Path == "/v1/signup" {
			signups++
		}
	}
	if signups != 1 {
		t.Fatalf("signup requests across both runs = %d, want 1", signups)
	}
}

func TestSetupNoWait(t *testing.T) {
	srv := fakeserver.New(t)
	env, stdout, _ := setupTestEnv(t)

	var linkPOSTs int
	srv.Handle(http.MethodPost, "/v1/installations/link", func(w http.ResponseWriter, _ *http.Request) {
		linkPOSTs++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"url":"https://ajent.social/connect?code=EXAMPLE","code":"ABCD-EFGH","status":"pending"}`))
	})

	if err := Run(context.Background(), env, Options{NoWait: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "https://ajent.social/connect?code=EXAMPLE") {
		t.Errorf("stdout missing the approval URL: %s", out)
	}
	if !strings.Contains(out, "After approval, rerun ajent setup to finish linking.") {
		t.Errorf("stdout missing the rerun hint: %s", out)
	}

	c, err := credentials.LoadRoot(env.ConfigDir)
	if err != nil {
		t.Fatalf("LoadRoot: %v", err)
	}
	if !c.HumanLinkPending {
		t.Error("HumanLinkPending = false, want true")
	}

	// A second run, with the server now reporting approval, must complete
	// without starting a new link (no second POST).
	srv.Handle(http.MethodGet, "/v1/installations/link", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"approved","agent_id":"00000000-0000-4000-8000-000000000001","credential_id":"00000000-0000-4000-8000-000000000002","scope":"user"}`))
	})

	if err := Run(context.Background(), env, Options{}); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if linkPOSTs != 1 {
		t.Errorf("link POST requests = %d, want 1 (no second POST)", linkPOSTs)
	}

	c2, err := credentials.LoadRoot(env.ConfigDir)
	if err != nil {
		t.Fatalf("LoadRoot after second run: %v", err)
	}
	if !c2.HumanLinked {
		t.Error("HumanLinked = false after second run, want true")
	}
}

func TestSetupPollErrorFails(t *testing.T) {
	setupShortenTimings(t)
	srv := fakeserver.New(t)
	env, _, _ := setupTestEnv(t)

	var calls int
	srv.Handle(http.MethodGet, "/v1/installations/link", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"not found"}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail":"server error"}`))
	})

	err := Run(context.Background(), env, Options{})
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("Run error = %v, want *api.Error", err)
	}
	if apiErr.Status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", apiErr.Status)
	}
	if calls != 2 {
		t.Errorf("link GET requests = %d, want 2 (the initial GET plus exactly one poll, no retry)", calls)
	}

	c, err := credentials.LoadRoot(env.ConfigDir)
	if err != nil {
		t.Fatalf("LoadRoot: %v", err)
	}
	if !c.HumanLinkPending {
		t.Error("HumanLinkPending = false, want true")
	}
}

func TestSetupUnknownStatusKeepsPolling(t *testing.T) {
	setupShortenTimings(t)
	srv := fakeserver.New(t)
	env, _, _ := setupTestEnv(t)

	var calls int
	srv.Handle(http.MethodGet, "/v1/installations/link", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch {
		case calls == 1:
			_, _ = w.Write([]byte(`{"status":"pending"}`))
		case calls <= 3:
			_, _ = w.Write([]byte(`{"status":"denied"}`))
		default:
			_, _ = w.Write([]byte(`{"status":"approved","agent_id":"00000000-0000-4000-8000-000000000001","credential_id":"00000000-0000-4000-8000-000000000002","scope":"user"}`))
		}
	})

	if err := Run(context.Background(), env, Options{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	c, err := credentials.LoadRoot(env.ConfigDir)
	if err != nil {
		t.Fatalf("LoadRoot: %v", err)
	}
	if !c.HumanLinked {
		t.Error("HumanLinked = false, want true")
	}
}

func TestSetupTimeout(t *testing.T) {
	setupShortenTimings(t)
	srv := fakeserver.New(t)
	env, _, _ := setupTestEnv(t)

	srv.Handle(http.MethodGet, "/v1/installations/link", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"pending"}`))
	})

	err := Run(context.Background(), env, Options{})
	if !errors.Is(err, errLinkTimeout) {
		t.Fatalf("Run error = %v, want errLinkTimeout", err)
	}
	// Exact stderr text (without the "ajent: " prefix Main adds), matching
	// testdata/contract/commands.json's "setup" command, "link_timeout" case.
	if err.Error() != "Approval timed out. Rerun link to start again." {
		t.Errorf("error text = %q", err.Error())
	}

	c, err := credentials.LoadRoot(env.ConfigDir)
	if err != nil {
		t.Fatalf("LoadRoot: %v", err)
	}
	if !c.HumanLinkPending {
		t.Error("HumanLinkPending = false, want true")
	}
}

func TestSetupContextCancel(t *testing.T) {
	setupShortenTimings(t)
	srv := fakeserver.New(t)
	env, _, _ := setupTestEnv(t)

	srv.Handle(http.MethodGet, "/v1/installations/link", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"pending"}`))
	})

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(5*time.Millisecond, cancel)

	err := Run(ctx, env, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}

	c, err := credentials.LoadRoot(env.ConfigDir)
	if err != nil {
		t.Fatalf("LoadRoot: %v", err)
	}
	if c.Token == "" {
		t.Error("credential.json lost its token")
	}
}

func TestSetupHooksSuccess(t *testing.T) {
	fakeserver.New(t) // sets AJENT_URL so the root credential's server is trusted
	env, _, _ := setupTestEnv(t)

	var (
		scopeCalls      int
		scopeArgScope   string
		scopeArgDomain  string
		scopeArgPrivate bool
		integrateCalls  int
	)
	origConfigureScope, origIntegrate := ConfigureScope, Integrate
	t.Cleanup(func() {
		ConfigureScope = origConfigureScope
		Integrate = origIntegrate
	})
	ConfigureScope = func(_ context.Context, _ *cli.Env, _ credentials.Credential, scope, domain string, private bool) error {
		scopeCalls++
		scopeArgScope = scope
		scopeArgDomain = domain
		scopeArgPrivate = private
		return nil
	}
	Integrate = func(context.Context, *cli.Env) error {
		integrateCalls++
		return nil
	}

	opts := Options{AgentOnly: true, Scope: "project", Domain: "example.com"}
	if err := Run(context.Background(), env, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if scopeCalls != 1 {
		t.Fatalf("ConfigureScope calls = %d, want 1", scopeCalls)
	}
	if scopeArgScope != "project" || scopeArgDomain != "example.com" || scopeArgPrivate {
		t.Errorf("ConfigureScope args = (%q, %q, %v), want (\"project\", \"example.com\", false)", scopeArgScope, scopeArgDomain, scopeArgPrivate)
	}
	if integrateCalls != 1 {
		t.Fatalf("Integrate calls = %d, want 1", integrateCalls)
	}
}

func TestSetupHooksPreSetupError(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, _ := setupTestEnv(t)

	origPreSetup := PreSetup
	t.Cleanup(func() { PreSetup = origPreSetup })
	wantErr := errors.New("presetup: boom")
	PreSetup = []func(ctx context.Context, env *cli.Env, opts Options) error{
		func(context.Context, *cli.Env, Options) error { return wantErr },
	}

	err := Run(context.Background(), env, Options{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run error = %v, want %v", err, wantErr)
	}
	if len(srv.Requests()) != 0 {
		t.Errorf("expected no HTTP requests before PreSetup runs, got %d", len(srv.Requests()))
	}
}

func TestSetupRefusesForeignServer(t *testing.T) {
	fakeserver.New(t) // sets AJENT_URL; the pre-written credential names a different server
	env, _, _ := setupTestEnv(t)

	existing := credentials.Credential{Server: "https://example.invalid", Token: "aj_0000000000000000_existingtoken"}
	if err := credentials.CreateRoot(env.ConfigDir, existing); err != nil {
		t.Fatalf("CreateRoot: %v", err)
	}

	err := Run(context.Background(), env, Options{})
	if !errors.Is(err, errForeignServer) {
		t.Fatalf("Run error = %v, want errForeignServer", err)
	}
}
