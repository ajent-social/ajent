// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ajent-social/ajent/internal/testutil/fakeserver"
)

// goalsTestID is a canonical UUID used across the goals command tests. It
// matches the id baked into testdata/contract responses/goal*.json, but the
// tests never depend on that coincidence.
const goalsTestID = "00000000-0000-4000-8000-000000000051"

// goalsIndentJSON reformats raw (compact JSON) the same way
// cli.PrintRawJSON does: json.Indent with a two-space step, plus a trailing
// newline. Tests use it to build the expected stdout for a data command
// without depending on the exact byte layout of the testdata fixture file.
func goalsIndentJSON(t *testing.T, raw string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(raw), "", "  "); err != nil {
		t.Fatalf("goalsIndentJSON(%q): %v", raw, err)
	}
	buf.WriteByte('\n')
	return buf.String()
}

// goalsCompactJSON compacts raw JSON bytes for a body comparison that does
// not depend on incidental whitespace.
func goalsCompactJSON(t *testing.T, raw []byte) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatalf("goalsCompactJSON(%q): %v", raw, err)
	}
	return buf.String()
}

// Raw (compact) bodies of the default testdata/contract/responses fixtures
// the fake server answers goals routes with.
const (
	goalsListRawJSON   = `{"goals":[{"id":"00000000-0000-4000-8000-000000000051","title":"Example goal","state":"open","version":3,"changed":true}],"next_before":null}`
	goalReadRawJSON    = `{"goal":{"id":"00000000-0000-4000-8000-000000000051","title":"Example goal","outcome":"Example outcome.","state":"open","version":3},"events":[{"id":"00000000-0000-4000-8000-000000000052","kind":"message","body":"Example event.","created_at":"2026-01-01T00:00:00Z"}],"next_before":null}`
	goalEventRawJSON   = `{"goal_id":"00000000-0000-4000-8000-000000000051","version":4,"event_id":"00000000-0000-4000-8000-000000000053"}`
	goalTrackedRawJSON = `{"id":"00000000-0000-4000-8000-000000000051","version":1,"tracked_by_agent":true}`
)

func TestGoalsRequests(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantMethod string
		wantPath   string
		wantQuery  string
		wantBody   string // "" means no body is expected (GET requests)
		wantKey    string // "" means no Idempotency-Key header is expected
		wantStdout string
	}{
		{
			name:       "goals",
			args:       []string{"goals"},
			wantMethod: http.MethodGet,
			wantPath:   "/v1/goals",
			wantStdout: goalsIndentJSON(t, goalsListRawJSON),
		},
		{
			name:       "goals before",
			args:       []string{"goals", "2026-01-01T00:00:00Z"},
			wantMethod: http.MethodGet,
			wantPath:   "/v1/goals",
			wantQuery:  "before=2026-01-01T00%3A00%3A00Z",
			wantStdout: goalsIndentJSON(t, goalsListRawJSON),
		},
		{
			name:       "goal",
			args:       []string{"goal", goalsTestID},
			wantMethod: http.MethodGet,
			wantPath:   "/v1/goals/" + goalsTestID,
			wantStdout: goalsIndentJSON(t, goalReadRawJSON),
		},
		{
			name:       "goal before",
			args:       []string{"goal", goalsTestID, "42"},
			wantMethod: http.MethodGet,
			wantPath:   "/v1/goals/" + goalsTestID,
			wantQuery:  "before=42",
			wantStdout: goalsIndentJSON(t, goalReadRawJSON),
		},
		{
			name:       "goal-update",
			args:       []string{"goal-update", "--key", "k", "--json", `{"kind":"progress","note":"x"}`, goalsTestID},
			wantMethod: http.MethodPost,
			wantPath:   "/v1/goals/" + goalsTestID + "/events",
			wantBody:   `{"kind":"progress","note":"x"}`,
			wantKey:    "k",
			wantStdout: goalsIndentJSON(t, goalEventRawJSON),
		},
		{
			name:       "goal-track",
			args:       []string{"goal-track", "--key", "k", "--json", `{"title":"t"}`},
			wantMethod: http.MethodPost,
			wantPath:   "/v1/goals",
			wantBody:   `{"title":"t"}`,
			wantKey:    "k",
			wantStdout: goalsIndentJSON(t, goalTrackedRawJSON),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := fakeserver.New(t)
			env, stdout, stderr, _ := newTestEnv(t, srv)

			if code := run(t, env, tt.args...); code != 0 {
				t.Fatalf("run(%v) = %d, want 0; stderr = %q", tt.args, code, stderr.String())
			}

			reqs := srv.Requests()
			if len(reqs) == 0 {
				t.Fatalf("no requests recorded")
			}
			got := reqs[len(reqs)-1]

			if got.Method != tt.wantMethod {
				t.Errorf("Method = %q, want %q", got.Method, tt.wantMethod)
			}
			if got.Path != tt.wantPath {
				t.Errorf("Path = %q, want %q", got.Path, tt.wantPath)
			}
			if got.RawQuery != tt.wantQuery {
				t.Errorf("RawQuery = %q, want %q", got.RawQuery, tt.wantQuery)
			}

			if tt.wantBody != "" {
				gotBody := goalsCompactJSON(t, got.Body)
				wantBody := goalsCompactJSON(t, []byte(tt.wantBody))
				if gotBody != wantBody {
					t.Errorf("body = %s, want %s", gotBody, wantBody)
				}
			} else if len(bytes.TrimSpace(got.Body)) != 0 {
				t.Errorf("body = %s, want empty", got.Body)
			}

			if gotKey := got.Header.Get("Idempotency-Key"); gotKey != tt.wantKey {
				t.Errorf("Idempotency-Key = %q, want %q", gotKey, tt.wantKey)
			}

			if stdout.String() != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.wantStdout)
			}
		})
	}
}

// goalsCapabilityAbsentHandler answers GET /v1/reliability with a
// capabilities list that does not include "goals".
func goalsCapabilityAbsentHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"capabilities":["other"]}`))
}

func TestGoalsCapabilityAbsent(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "goals", args: []string{"goals"}},
		{name: "goal", args: []string{"goal", goalsTestID}},
		{name: "goal-update", args: []string{"goal-update", "--key", "k", "--json", `{"a":1}`, goalsTestID}},
		{name: "goal-track", args: []string{"goal-track", "--key", "k", "--json", `{"a":1}`}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := fakeserver.New(t)
			srv.Handle(http.MethodGet, "/v1/reliability", goalsCapabilityAbsentHandler)
			env, _, stderr, _ := newTestEnv(t, srv)

			code := run(t, env, tt.args...)
			if code != 1 {
				t.Fatalf("run(%v) = %d, want 1; stderr = %q", tt.args, code, stderr.String())
			}
			if !strings.Contains(stderr.String(), "unsupported_capability") {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), "unsupported_capability")
			}

			reqs := srv.Requests()
			if len(reqs) != 1 || reqs[0].Path != "/v1/reliability" {
				t.Errorf("Requests() = %+v, want exactly one request to /v1/reliability", reqs)
			}
		})
	}
}

func TestGoalUpdateRequiresKey(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)

	code := run(t, env, "goal-update", "--json", `{"a":1}`, goalsTestID)
	if code != 1 {
		t.Fatalf("run() = %d, want 1; stderr = %q", code, stderr.String())
	}
	if stderr.String() != "ajent: Supply a stable --key for retries.\n" {
		t.Errorf("stderr = %q, want %q", stderr.String(), "ajent: Supply a stable --key for retries.\n")
	}

	for _, req := range srv.Requests() {
		if req.Method == http.MethodPost {
			t.Errorf("unexpected POST request: %+v", req)
		}
	}
}

func TestGoalBadBefore(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)

	code := run(t, env, "goal", goalsTestID, "abc")
	if code != 1 {
		t.Fatalf("run() = %d, want 1; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "before must be an integer event sequence") {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), "before must be an integer event sequence")
	}
}

func TestGoalJSONMustBeObject(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)

	code := run(t, env, "goal-update", "--key", "k", "--json", "[1]", goalsTestID)
	if code != 1 {
		t.Fatalf("run() = %d, want 1; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--json must be a JSON object") {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), "--json must be a JSON object")
	}
}

func TestGoalFixtureMessages(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"goal no id", []string{"goal"}, "ajent: Supply a goal UUID.\n"},
		{"goal-track no key", []string{"goal-track", goalsTestID}, "ajent: Supply a stable --key for retries.\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := fakeserver.New(t)
			env, _, stderr, _ := newTestEnv(t, srv)
			if code := run(t, env, tt.args...); code != 1 || stderr.String() != tt.want {
				t.Errorf("run(%v) = %d, stderr %q; want 1, %q", tt.args, code, stderr.String(), tt.want)
			}
		})
	}
}
