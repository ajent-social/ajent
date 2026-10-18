// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/ajent-social/ajent/internal/cli"
	"github.com/ajent-social/ajent/internal/credentials"
	"github.com/ajent-social/ajent/internal/testutil/fakeserver"
	"github.com/ajent-social/ajent/internal/version"
)

// readTestFixturesDir is testdata/contract, relative to this package
// directory (cmd/ajent).
const readTestFixturesDir = "../../testdata/contract"

// readTestResponseFixture reads testdata/contract/responses/<name> and
// returns it re-indented two spaces per level with a trailing newline, the
// same shape getAndPrint (via cli.PrintRawJSON) writes to stdout.
func readTestResponseFixture(t *testing.T, name string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(readTestFixturesDir, "responses", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	// The fixture file itself ends with a trailing newline, but the
	// api.Client decodes each response into a json.RawMessage that holds
	// only the JSON value's own bytes, with no trailing whitespace. Trim it
	// here too, so this matches what getAndPrint actually indents.
	raw = bytes.TrimSpace(raw)
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		t.Fatalf("indenting fixture %s: %v", name, err)
	}
	buf.WriteByte('\n')
	return buf.String()
}

// readTestErrorEntry is one row of testdata/contract/errors.json
// "statuses". Status is kept raw because some rows use "5xx" or "other"
// instead of a numeric status.
type readTestErrorEntry struct {
	Status json.RawMessage `json:"status"`
	Code   string          `json:"code"`
	Action string          `json:"action"`
}

// readTestErrorAction returns errors.json's code and action for status,
// matched by its exact JSON-encoded form (for example "401").
func readTestErrorAction(t *testing.T, status int) readTestErrorEntry {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(readTestFixturesDir, "errors.json"))
	if err != nil {
		t.Fatalf("reading errors.json: %v", err)
	}
	var doc struct {
		Statuses []readTestErrorEntry `json:"statuses"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing errors.json: %v", err)
	}

	want := strconv.Itoa(status)
	for _, e := range doc.Statuses {
		if string(e.Status) == want {
			return e
		}
	}
	t.Fatalf("no errors.json entry for status %d", status)
	return readTestErrorEntry{}
}

// readTestLastKey returns the last top-level key of the JSON object raw,
// decoded through json.Decoder tokens so key order (not map iteration) is
// what is observed.
func readTestLastKey(t *testing.T, raw []byte) string {
	t.Helper()

	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("reading opening token: %v", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		t.Fatalf("first token = %v, want '{'", tok)
	}

	var lastKey string
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			t.Fatalf("reading key token: %v", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			t.Fatalf("key token = %v (%T), want string", keyTok, keyTok)
		}
		lastKey = key

		var discard json.RawMessage
		if err := dec.Decode(&discard); err != nil {
			t.Fatalf("decoding value for key %q: %v", key, err)
		}
	}
	return lastKey
}

// readCommandCase is one TestReadCommands table row for a data command
// that uses the tool (profile) credential.
type readCommandCase struct {
	name         string
	args         []string
	wantPath     string
	wantQuery    string
	responseFile string
}

func TestReadCommands(t *testing.T) {
	cases := []readCommandCase{
		{name: "me", args: []string{"me"}, wantPath: "/v1/me", wantQuery: "", responseFile: "me.json"},
		{name: "feed", args: []string{"feed"}, wantPath: "/v1/feed", wantQuery: "", responseFile: "feed.json"},
		{name: "groups", args: []string{"groups"}, wantPath: "/v1/groups", wantQuery: "", responseFile: "groups.json"},
		{name: "agents", args: []string{"agents"}, wantPath: "/v1/agents", wantQuery: "", responseFile: "agents.json"},
		{name: "inbox", args: []string{"inbox"}, wantPath: "/v1/inbox", wantQuery: "", responseFile: "inbox.json"},
		{
			name:         "search_default",
			args:         []string{"search", "flaky", "test"},
			wantPath:     "/v1/search",
			wantQuery:    "q=flaky+test",
			responseFile: "search.json",
		},
		{
			name:         "search_group",
			args:         []string{"search", "--group", "ABCDEF12-3456-7890-ABCD-EF1234567890", "flaky"},
			wantPath:     "/v1/search",
			wantQuery:    "group_id=abcdef12-3456-7890-abcd-ef1234567890&q=flaky",
			responseFile: "search.json",
		},
		{name: "events_default", args: []string{"events"}, wantPath: "/v1/events", wantQuery: "after=0", responseFile: "events.json"},
		{name: "events_after", args: []string{"events", "5"}, wantPath: "/v1/events", wantQuery: "after=5", responseFile: "events.json"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := fakeserver.New(t)
			env, stdout, _, profiles := newTestEnv(t, srv)

			if code := run(t, env, tc.args...); code != 0 {
				t.Fatalf("run(%v) = %d, want 0", tc.args, code)
			}

			reqs := srv.Requests()
			if len(reqs) != 1 {
				t.Fatalf("requests = %d, want 1: %+v", len(reqs), reqs)
			}
			if reqs[0].Method != http.MethodGet || reqs[0].Path != tc.wantPath || reqs[0].RawQuery != tc.wantQuery {
				t.Errorf("request = %s %s?%s, want GET %s?%s", reqs[0].Method, reqs[0].Path, reqs[0].RawQuery, tc.wantPath, tc.wantQuery)
			}

			if want := readTestResponseFixture(t, tc.responseFile); stdout.String() != want {
				t.Errorf("stdout = %q, want %q", stdout.String(), want)
			}

			root := credentials.ProjectRoot(env.Cwd, env.Home)
			want := credentials.ProjectProfile("terminal", root)
			got := profiles()
			if len(got) != 1 || got[0] != want {
				t.Errorf("resolved profiles = %v, want [%q]", got, want)
			}
		})
	}

	t.Run("doctor", func(t *testing.T) {
		srv := fakeserver.New(t)
		env, stdout, _, _ := newTestEnv(t, srv)
		writeRoot(t, env, srv.Credential())

		if code := run(t, env, "doctor"); code != 0 {
			t.Fatalf("run(doctor) = %d, want 0", code)
		}

		reqs := srv.Requests()
		if len(reqs) != 1 {
			t.Fatalf("requests = %d, want 1: %+v", len(reqs), reqs)
		}
		if reqs[0].Method != http.MethodGet || reqs[0].Path != "/v1/reliability" || reqs[0].RawQuery != "" {
			t.Errorf("request = %s %s?%s, want GET /v1/reliability?", reqs[0].Method, reqs[0].Path, reqs[0].RawQuery)
		}

		raw, err := os.ReadFile(filepath.Join(readTestFixturesDir, "responses", "reliability.json"))
		if err != nil {
			t.Fatalf("reading reliability.json: %v", err)
		}
		augmented, err := cli.AddField(raw, "local_client_version", version.Version)
		if err != nil {
			t.Fatalf("cli.AddField: %v", err)
		}
		var want bytes.Buffer
		if err := json.Indent(&want, augmented, "", "  "); err != nil {
			t.Fatalf("json.Indent: %v", err)
		}
		want.WriteByte('\n')
		if stdout.String() != want.String() {
			t.Errorf("stdout = %q, want %q", stdout.String(), want.String())
		}
	})
}

// TestDoctorUsesRootAndAddsVersion checks that doctor uses the root
// credential directly, never resolving a tool profile, and that
// local_client_version is appended as the response's last key.
func TestDoctorUsesRootAndAddsVersion(t *testing.T) {
	srv := fakeserver.New(t)
	env, stdout, _, _ := newTestEnv(t, srv)
	writeRoot(t, env, srv.Credential())
	env.Resolve = func(context.Context, string) (credentials.Credential, error) {
		t.Fatal("doctor must not resolve a tool profile")
		return credentials.Credential{}, nil
	}

	if code := run(t, env, "doctor"); code != 0 {
		t.Fatalf("run(doctor) = %d, want 0", code)
	}

	if lastKey := readTestLastKey(t, stdout.Bytes()); lastKey != "local_client_version" {
		t.Fatalf("last key = %q, want %q", lastKey, "local_client_version")
	}

	var got struct {
		LocalClientVersion string `json:"local_client_version"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshaling stdout: %v", err)
	}
	if got.LocalClientVersion != version.Version {
		t.Errorf("local_client_version = %q, want %q", got.LocalClientVersion, version.Version)
	}
}

// TestSearchInvalidGroup checks that an invalid --group is rejected before
// any request is sent.
func TestSearchInvalidGroup(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)

	if code := run(t, env, "search", "--group", "not-a-uuid", "hello"); code != 1 {
		t.Fatalf("run(search --group not-a-uuid) = %d, want 1", code)
	}

	want := "ajent: invalid --group: api: invalid UUID\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	if got := len(srv.Requests()); got != 0 {
		t.Errorf("requests = %d, want 0", got)
	}
}

// TestReadHTTPError checks that an API error is reported as
// "ajent: <code>. <action>" and exits 1.
func TestReadHTTPError(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)
	srv.Handle(http.MethodGet, "/v1/me", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	if code := run(t, env, "me"); code != 1 {
		t.Fatalf("run(me) = %d, want 1", code)
	}

	entry := readTestErrorAction(t, http.StatusUnauthorized)
	want := "ajent: " + entry.Code + ". " + entry.Action + "\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

// TestProfileFlag checks that --profile changes which profile is resolved.
func TestProfileFlag(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, _, profiles := newTestEnv(t, srv)

	if code := run(t, env, "me", "--profile", "codex"); code != 0 {
		t.Fatalf("run(me --profile codex) = %d, want 0", code)
	}

	root := credentials.ProjectRoot(env.Cwd, env.Home)
	want := credentials.ProjectProfile("codex", root)
	got := profiles()
	if len(got) != 1 || got[0] != want {
		t.Fatalf("resolved profiles = %v, want [%q]", got, want)
	}
}
