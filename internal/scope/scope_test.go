// SPDX-License-Identifier: Apache-2.0

package scope

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ajent-social/ajent-client/internal/api"
	"github.com/ajent-social/ajent-client/internal/cli"
	"github.com/ajent-social/ajent-client/internal/config"
	"github.com/ajent-social/ajent-client/internal/credentials"
	"github.com/ajent-social/ajent-client/internal/testutil/fakeserver"
)

// scopeTestEnv builds a non-interactive cli.Env with fresh t.TempDir()s for
// Cwd, Home and ConfigDir. Tests that need an interactive terminal override
// env.OpenTTY with scopeInteractiveTTY.
func scopeTestEnv(t *testing.T) (env *cli.Env, stdout *bytes.Buffer) {
	t.Helper()

	stdout = &bytes.Buffer{}
	env = &cli.Env{
		Stdin:     strings.NewReader(""),
		Stdout:    stdout,
		Stderr:    &bytes.Buffer{},
		Getenv:    os.Getenv,
		Cwd:       t.TempDir(),
		Home:      t.TempDir(),
		ConfigDir: t.TempDir(),
		API:       &api.Client{Sleep: func(time.Duration) {}},
		OpenTTY: func() (io.ReadWriteCloser, error) {
			return nil, errors.New("scope test: no controlling terminal")
		},
		Now: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}
	return env, stdout
}

// scopeFakeTTY is an OpenTTY double: Read is served from a pipe fed by a
// background goroutine with scripted answer lines (one Write per line, so
// each prompt's fresh bufio.Reader sees exactly one line per underlying
// Read, matching io.Pipe's one-write-per-read semantics), and Write is
// captured in a buffer for assertions.
type scopeFakeTTY struct {
	r *io.PipeReader
	w *bytes.Buffer
}

func (f *scopeFakeTTY) Read(p []byte) (int, error)  { return f.r.Read(p) }
func (f *scopeFakeTTY) Write(p []byte) (int, error) { return f.w.Write(p) }
func (f *scopeFakeTTY) Close() error                { return f.r.Close() }

// scopeInteractiveTTY returns an OpenTTY double that answers each prompt
// with the next of lines, in order, and a buffer capturing everything
// written to it (the prompt text itself).
func scopeInteractiveTTY(t *testing.T, lines ...string) (io.ReadWriteCloser, *bytes.Buffer) {
	t.Helper()
	pr, pw := io.Pipe()
	go func() {
		for _, line := range lines {
			_, _ = io.WriteString(pw, line)
		}
		_ = pw.Close()
	}()
	return &scopeFakeTTY{r: pr, w: &bytes.Buffer{}}, nil
}

func TestConfigureUserVerifiedDomain(t *testing.T) {
	srv := fakeserver.New(t)
	env, _ := scopeTestEnv(t)
	root := srv.Credential()

	var grantBody map[string]any
	srv.Handle(http.MethodGet, "/v1/domains", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domains":[{"domain":"example.com","verified":true}]}`))
	})
	srv.Handle(http.MethodPost, "/v1/domain-grants", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&grantBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"00000000-0000-4000-8000-000000000010","domain":"example.com"}`))
	})

	dir, err := env.Dir()
	if err != nil {
		t.Fatalf("env.Dir: %v", err)
	}
	if err := config.WriteJSON(filepath.Join(dir, "config.json"), map[string]any{"note": "keep me"}); err != nil {
		t.Fatalf("seeding config.json: %v", err)
	}

	msg, err := Configure(context.Background(), env, root, Options{
		Scope:     "user",
		Domain:    "example.com",
		DomainSet: true,
	})
	if err != nil {
		t.Fatalf("Configure: %v", err)
	}
	wantMsg := "example.com saved for all your projects. New and existing agents inherit these settings on their next operation."
	if msg != wantMsg {
		t.Errorf("message = %q, want %q", msg, wantMsg)
	}
	if grantBody["domain"] != "example.com" || grantBody["scope"] != "user" {
		t.Errorf("grant request body = %v", grantBody)
	}

	var settings map[string]any
	if err := config.ReadJSON(filepath.Join(dir, "config.json"), &settings); err != nil {
		t.Fatalf("reading config.json: %v", err)
	}
	want := map[string]any{"version": float64(1), "domain": "example.com", "private_only": false, "note": "keep me"}
	if !reflect.DeepEqual(settings, want) {
		t.Errorf("config.json = %v, want %v", settings, want)
	}

	var bindings map[string]map[string][]map[string]any
	if err := config.ReadJSON(filepath.Join(dir, "scope-grants.json"), &bindings); err != nil {
		t.Fatalf("reading scope-grants.json: %v", err)
	}
	grants := bindings[root.Identity()]["user"]
	wantGrants := []map[string]any{{"id": "00000000-0000-4000-8000-000000000010", "domain": "example.com"}}
	if !reflect.DeepEqual(grants, wantGrants) {
		t.Errorf("scope-grants.json[identity][user] = %v, want %v", grants, wantGrants)
	}

	for _, r := range srv.Requests() {
		if r.Path == "/v1/domains/proof" {
			t.Errorf("unexpected proof request: %+v", r)
		}
	}
}

func TestConfigureProjectPrivate(t *testing.T) {
	srv := fakeserver.New(t)
	env, _ := scopeTestEnv(t)
	root := srv.Credential()

	msg, err := Configure(context.Background(), env, root, Options{
		Scope:   "project",
		Private: true,
	})
	if err != nil {
		t.Fatalf("Configure: %v", err)
	}
	wantMsg := "Private mode saved for this project. New and existing agents inherit these settings on their next operation."
	if msg != wantMsg {
		t.Errorf("message = %q, want %q", msg, wantMsg)
	}

	projectRoot := credentials.ProjectRoot(env.Cwd, env.Home)
	target := filepath.Join(projectRoot, ".aj", "config.json")
	var settings map[string]any
	if err := config.ReadJSON(target, &settings); err != nil {
		t.Fatalf("reading %s: %v", target, err)
	}
	want := map[string]any{"version": float64(1), "domain": "", "private_only": true}
	if !reflect.DeepEqual(settings, want) {
		t.Errorf("settings = %v, want %v", settings, want)
	}

	if reqs := srv.Requests(); len(reqs) != 0 {
		t.Errorf("requests = %+v, want none", reqs)
	}

	dir, err := env.Dir()
	if err != nil {
		t.Fatalf("env.Dir: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "scope-grants.json")); !os.IsNotExist(statErr) {
		t.Errorf("scope-grants.json stat error = %v, want IsNotExist", statErr)
	}
}

func TestConfigureUnverifiedRunsProof(t *testing.T) {
	srv := fakeserver.New(t)
	env, stdout := scopeTestEnv(t)
	root := srv.Credential()

	srv.Handle(http.MethodGet, "/v1/domains", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domains":[]}`))
	})
	var order []string
	srv.Handle(http.MethodPost, "/v1/domains/proof", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		order = append(order, body["action"].(string))
		w.Header().Set("Content-Type", "application/json")
		if body["action"] == "challenge" {
			_, _ = w.Write([]byte(`{"domain":"example.com","name":"_ajent-challenge.example.com","value":"tok"}`))
			return
		}
		_, _ = w.Write([]byte(`{"domain":"example.com","verified":true}`))
	})
	srv.Handle(http.MethodPost, "/v1/domain-grants", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"00000000-0000-4000-8000-000000000010","domain":"example.com"}`))
	})

	if _, err := Configure(context.Background(), env, root, Options{
		Scope:     "user",
		Domain:    "example.com",
		DomainSet: true,
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	if want := []string{"challenge", "check"}; !reflect.DeepEqual(order, want) {
		t.Errorf("proof order = %v, want %v", order, want)
	}
	if !strings.Contains(stdout.String(), "Add DNS TXT _ajent-challenge.example.com = tok\n") {
		t.Errorf("stdout = %q, missing the TXT line", stdout.String())
	}
}

func TestConfigure403Message(t *testing.T) {
	srv := fakeserver.New(t)
	env, _ := scopeTestEnv(t)
	root := srv.Credential()

	srv.Handle(http.MethodGet, "/v1/domains", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domains":[{"domain":"example.com","verified":true}]}`))
	})
	srv.Handle(http.MethodPost, "/v1/domain-grants", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"permission_denied"}`))
	})

	_, err := Configure(context.Background(), env, root, Options{
		Scope: "user", Domain: "example.com", DomainSet: true,
	})
	if !errors.Is(err, errNeedsApproval) {
		t.Fatalf("err = %v, want errNeedsApproval", err)
	}
	wantText := "This scope needs human approval. Run ajent link, sign in and approve the domain scope."
	if err.Error() != wantText {
		t.Errorf("err.Error() = %q, want %q", err.Error(), wantText)
	}
}

func TestConfigureNonInteractiveNeedsScope(t *testing.T) {
	t.Run("missing scope", func(t *testing.T) {
		srv := fakeserver.New(t)
		env, _ := scopeTestEnv(t)
		_, err := Configure(context.Background(), env, srv.Credential(), Options{})
		if !errors.Is(err, errNoScope) {
			t.Fatalf("err = %v, want errNoScope", err)
		}
		if want := "Choose --scope user or --scope project."; err.Error() != want {
			t.Errorf("err.Error() = %q, want %q", err.Error(), want)
		}
	})
	t.Run("missing scope during setup", func(t *testing.T) {
		srv := fakeserver.New(t)
		env, _ := scopeTestEnv(t)
		_, err := Configure(context.Background(), env, srv.Credential(), Options{DuringSetup: true})
		if !errors.Is(err, ErrDeferred) {
			t.Fatalf("err = %v, want ErrDeferred", err)
		}
	})
	t.Run("missing domain choice", func(t *testing.T) {
		srv := fakeserver.New(t)
		env, _ := scopeTestEnv(t)
		_, err := Configure(context.Background(), env, srv.Credential(), Options{Scope: "user"})
		if !errors.Is(err, errNoDomainChoice) {
			t.Fatalf("err = %v, want errNoDomainChoice", err)
		}
		if want := "Choose --domain DOMAIN or --private."; err.Error() != want {
			t.Errorf("err.Error() = %q, want %q", err.Error(), want)
		}
	})
	t.Run("missing domain choice during setup", func(t *testing.T) {
		srv := fakeserver.New(t)
		env, _ := scopeTestEnv(t)
		_, err := Configure(context.Background(), env, srv.Credential(), Options{Scope: "user", DuringSetup: true})
		if !errors.Is(err, ErrDeferred) {
			t.Fatalf("err = %v, want ErrDeferred", err)
		}
	})
}

func TestConfigureInteractivePrompts(t *testing.T) {
	srv := fakeserver.New(t)
	env, _ := scopeTestEnv(t)
	tty, _ := scopeInteractiveTTY(t, "2\n", "\n")
	env.OpenTTY = func() (io.ReadWriteCloser, error) { return tty, nil }

	var grantBody map[string]any
	srv.Handle(http.MethodPost, "/v1/domain-grants", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&grantBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"00000000-0000-4000-8000-000000000010","domain":"example.com"}`))
	})

	if _, err := Configure(context.Background(), env, srv.Credential(), Options{}); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	wantScope := credentials.ProjectScope(credentials.ProjectRoot(env.Cwd, env.Home))
	if grantBody["scope"] != wantScope {
		t.Errorf("grant scope = %v, want %v", grantBody["scope"], wantScope)
	}
	if grantBody["domain"] != "example.com" {
		t.Errorf("grant domain = %v, want example.com", grantBody["domain"])
	}
}

func TestHostileProjectConfigGrantsNothing(t *testing.T) {
	srv := fakeserver.New(t)
	env, _ := scopeTestEnv(t)
	root := srv.Credential()

	projectRoot := credentials.ProjectRoot(env.Cwd, env.Home)
	ajDir := filepath.Join(projectRoot, ".aj")
	if err := os.MkdirAll(ajDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ajDir, "config.json"),
		[]byte(`{"version":1,"domain":"evil.example","grants":["00000000-0000-4000-8000-000000000099"],"private_only":false}`), 0o600); err != nil {
		t.Fatalf("writing project config.json: %v", err)
	}
	decoy := `{"` + root.Identity() + `":{"user":[{"id":"00000000-0000-4000-8000-000000000099","domain":"evil.example"}]}}`
	if err := os.WriteFile(filepath.Join(ajDir, "scope-grants.json"), []byte(decoy), 0o600); err != nil {
		t.Fatalf("writing decoy scope-grants.json: %v", err)
	}

	if _, err := Configure(context.Background(), env, root, Options{Scope: "project", Private: true}); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	dir, err := env.Dir()
	if err != nil {
		t.Fatalf("env.Dir: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "scope-grants.json")); !os.IsNotExist(statErr) {
		t.Errorf("user-level scope-grants.json stat error = %v, want IsNotExist", statErr)
	}

	for _, r := range srv.Requests() {
		if r.Path == "/v1/domain-grants" {
			t.Errorf("unexpected request to /v1/domain-grants: %+v", r)
		}
	}
}

func TestValidDomain(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"simple", "example.com", true},
		{"subdomain", "a.b.co", true},
		{"normalizes", "EXAMPLE.com.", true},
		{"scheme", "http://x.com", false},
		{"port", "x.com:80", false},
		{"no dot", "localhost", false},
		{"leading hyphen label", "-a.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidDomain(tt.in); got != tt.want {
				t.Errorf("ValidDomain(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
