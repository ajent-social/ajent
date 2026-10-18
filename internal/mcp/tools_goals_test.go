// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ajent-social/ajent-client/internal/api"
	"github.com/ajent-social/ajent-client/internal/credentials"
	"github.com/ajent-social/ajent-client/internal/testutil/fakeserver"
)

// goalsToolsTestID is the goal UUID used by every goals-tool test that
// reads or updates a single goal (matching testdata/contract/tools.json and
// its response fixtures).
const goalsToolsTestID = "00000000-0000-4000-8000-000000000051"

// goalsToolsTestSession builds a Session wired to srv, with a fixed profile
// credential carrying a group_id and agent_id, so every goals tool's
// request can be built (docs/tasks/T3.8.md step 7).
func goalsToolsTestSession(t *testing.T, srv *fakeserver.Server) *Session {
	t.Helper()
	cred := srv.Credential()
	cred.AgentID = "00000000-0000-4000-8000-000000000001"
	cred.GroupID = "00000000-0000-4000-8000-000000000003"
	return &Session{
		Profile: "terminal",
		Dir:     t.TempDir(),
		Root:    cred,
		API:     &api.Client{},
		Cache:   OpenCache(t.TempDir(), "terminal"),
		Resolve: func(context.Context, string) (credentials.Credential, error) {
			return cred, nil
		},
	}
}

// goalsToolsFixture reads and decodes a JSON object fixture from
// testdata/contract, relative to the module root.
func goalsToolsFixture(t *testing.T, relPath string) map[string]any {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "contract", relPath)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", relPath, err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("parsing fixture %s: %v", relPath, err)
	}
	return v
}

// goalsToolsAssertBody compares got, decoded, against the decoded fixture
// at relPath. relPath == "" asserts that got is empty.
func goalsToolsAssertBody(t *testing.T, got []byte, relPath string) {
	t.Helper()
	if relPath == "" {
		if len(got) != 0 {
			t.Errorf("body = %s, want none", got)
		}
		return
	}

	want := goalsToolsFixture(t, relPath)
	var gotValue map[string]any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decoding recorded body %s: %v", got, err)
	}
	if !reflect.DeepEqual(gotValue, want) {
		t.Errorf("body = %#v, want %#v", gotValue, want)
	}
}

// TestGoalsToolsRequests proves each of the four goals tools sends the
// exact method, path, query, Idempotency-Key and decoded body the contract
// specifies (docs/tasks/T3.8.md step 7; testdata/contract/tools.json).
func TestGoalsToolsRequests(t *testing.T) {
	tests := []struct {
		name        string
		call        func(s *Session) (any, error)
		wantMethod  string
		wantPath    string
		wantQuery   url.Values
		wantKey     string
		wantFixture string // "" = no request body expected
	}{
		{
			name:       "ajent_goals list",
			call:       func(s *Session) (any, error) { return goalsToolsList(context.Background(), s, map[string]any{}) },
			wantMethod: "GET",
			wantPath:   "/v1/goals",
		},
		{
			name: "ajent_goals before",
			call: func(s *Session) (any, error) {
				return goalsToolsList(context.Background(), s, map[string]any{"before": "example-cursor"})
			},
			wantMethod: "GET",
			wantPath:   "/v1/goals",
			wantQuery:  url.Values{"before": {"example-cursor"}},
		},
		{
			name: "ajent_goal read",
			call: func(s *Session) (any, error) {
				return goalsToolsRead(context.Background(), s, map[string]any{"id": goalsToolsTestID})
			},
			wantMethod: "GET",
			wantPath:   "/v1/goals/" + goalsToolsTestID,
		},
		{
			name: "ajent_goal before_version",
			call: func(s *Session) (any, error) {
				return goalsToolsRead(context.Background(), s, map[string]any{
					"id":      goalsToolsTestID,
					"before":  float64(4),
					"version": float64(3),
				})
			},
			wantMethod: "GET",
			wantPath:   "/v1/goals/" + goalsToolsTestID,
			wantQuery:  url.Values{"before": {"4"}, "version": {"3"}},
		},
		{
			name: "ajent_goal_update",
			call: func(s *Session) (any, error) {
				return goalsToolsUpdate(context.Background(), s, map[string]any{
					"id":        goalsToolsTestID,
					"version":   float64(3),
					"action":    "update",
					"body":      "Example progress.",
					"next_step": "Example next step.",
					"key":       "goal-update-key-1",
				})
			},
			wantMethod:  "POST",
			wantPath:    "/v1/goals/" + goalsToolsTestID + "/events",
			wantKey:     "goal-update-key-1",
			wantFixture: "requests/goal_update.json",
		},
		{
			name: "ajent_goal_track",
			call: func(s *Session) (any, error) {
				return goalsToolsTrack(context.Background(), s, map[string]any{
					"title":     "Example goal",
					"outcome":   "Example outcome.",
					"criteria":  "Example criteria.",
					"authority": "Requested by the human in this session.",
					"key":       "goal-track-key-1",
				})
			},
			wantMethod:  "POST",
			wantPath:    "/v1/goals",
			wantKey:     "goal-track-key-1",
			wantFixture: "requests/goal_track.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := fakeserver.New(t)
			s := goalsToolsTestSession(t, srv)

			if _, err := tt.call(s); err != nil {
				t.Fatalf("handler error = %v", err)
			}

			// The capability probe (GET /v1/reliability) is the first
			// request every goals handler issues; assert on the last
			// request recorded rather than requiring exactly one.
			reqs := srv.Requests()
			if len(reqs) == 0 {
				t.Fatalf("fakeserver recorded no requests")
			}
			got := reqs[len(reqs)-1]
			if got.Method != tt.wantMethod {
				t.Errorf("method = %q, want %q", got.Method, tt.wantMethod)
			}
			if got.Path != tt.wantPath {
				t.Errorf("path = %q, want %q", got.Path, tt.wantPath)
			}

			gotQuery, err := url.ParseQuery(got.RawQuery)
			if err != nil {
				t.Fatalf("parsing RawQuery %q: %v", got.RawQuery, err)
			}
			wantQuery := tt.wantQuery
			if wantQuery == nil {
				wantQuery = url.Values{}
			}
			if gotQuery.Encode() != wantQuery.Encode() {
				t.Errorf("query = %v, want %v", gotQuery, wantQuery)
			}

			if gotKey := got.Header.Get("Idempotency-Key"); gotKey != tt.wantKey {
				t.Errorf("Idempotency-Key = %q, want %q", gotKey, tt.wantKey)
			}

			goalsToolsAssertBody(t, got.Body, tt.wantFixture)
		})
	}
}

// TestGoalsCapabilityAbsent proves each of the four goals tools checks the
// goals capability before sending any /v1/goals request, and returns
// *api.UnsupportedCapabilityError when the server does not advertise it
// (docs/tasks/T3.8.md step 7; docs/contract.md section 9).
func TestGoalsCapabilityAbsent(t *testing.T) {
	tests := []struct {
		name string
		call func(s *Session) (any, error)
	}{
		{
			name: "ajent_goals",
			call: func(s *Session) (any, error) { return goalsToolsList(context.Background(), s, map[string]any{}) },
		},
		{
			name: "ajent_goal",
			call: func(s *Session) (any, error) {
				return goalsToolsRead(context.Background(), s, map[string]any{"id": goalsToolsTestID})
			},
		},
		{
			name: "ajent_goal_update",
			call: func(s *Session) (any, error) {
				return goalsToolsUpdate(context.Background(), s, map[string]any{
					"id":      goalsToolsTestID,
					"version": float64(3),
					"action":  "update",
					"body":    "Example progress.",
					"key":     "goal-update-key-1",
				})
			},
		},
		{
			name: "ajent_goal_track",
			call: func(s *Session) (any, error) {
				return goalsToolsTrack(context.Background(), s, map[string]any{
					"title":     "Example goal",
					"outcome":   "Example outcome.",
					"criteria":  "Example criteria.",
					"authority": "Requested by the human in this session.",
					"key":       "goal-track-key-1",
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := fakeserver.New(t)
			srv.Handle("GET", "^/v1/reliability$", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"ok","capabilities":[]}`))
			})
			s := goalsToolsTestSession(t, srv)

			_, err := tt.call(s)

			var capErr *api.UnsupportedCapabilityError
			if !errors.As(err, &capErr) {
				t.Fatalf("error = %v (%T), want *api.UnsupportedCapabilityError", err, err)
			}
			if capErr.Capability != "goals" {
				t.Errorf("capability = %q, want goals", capErr.Capability)
			}

			for _, r := range srv.Requests() {
				if strings.HasPrefix(r.Path, "/v1/goals") {
					t.Fatalf("unexpected %s request: %+v", r.Path, r)
				}
			}
		})
	}
}

// TestGoalTrackDefaultsPriority proves ajent_goal_track defaults priority
// to "normal" when the argument is absent, and leaves an explicit priority
// unchanged (docs/tasks/T3.8.md step 7).
func TestGoalTrackDefaultsPriority(t *testing.T) {
	tests := []struct {
		name         string
		args         map[string]any
		wantPriority string
	}{
		{
			name: "absent defaults to normal",
			args: map[string]any{
				"title": "Example goal", "outcome": "Example outcome.",
				"criteria": "Example criteria.", "authority": "Requested by the human in this session.",
				"key": "goal-track-key-1",
			},
			wantPriority: "normal",
		},
		{
			name: "explicit priority is preserved",
			args: map[string]any{
				"title": "Example goal", "outcome": "Example outcome.",
				"criteria": "Example criteria.", "authority": "Requested by the human in this session.",
				"key": "goal-track-key-2", "priority": "high",
			},
			wantPriority: "high",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := fakeserver.New(t)
			s := goalsToolsTestSession(t, srv)

			if _, err := goalsToolsTrack(context.Background(), s, tt.args); err != nil {
				t.Fatalf("handler error = %v", err)
			}

			reqs := srv.Requests()
			if len(reqs) == 0 {
				t.Fatalf("fakeserver recorded no requests")
			}
			got := reqs[len(reqs)-1]
			var body map[string]any
			if err := json.Unmarshal(got.Body, &body); err != nil {
				t.Fatalf("decoding body: %v", err)
			}
			if body["priority"] != tt.wantPriority {
				t.Errorf("priority = %v, want %q", body["priority"], tt.wantPriority)
			}
		})
	}
}

// goalsToolsLargeFixtureBody reads testdata/mcp/goal_large.json's raw
// bytes, for a fakeserver route that serves them unchanged.
func goalsToolsLargeFixtureBody(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "mcp", "goal_large.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading goal_large.json: %v", err)
	}
	return data
}

// TestGoalReadFragmentsCarrySnapshotVersion proves a large ajent_goal
// response is fragmented, the first fragment carries snapshot_version and
// the reread guidance, and following next_offset reconstructs the full
// text (docs/tasks/T3.8.md step 7; docs/contract.md section 11, "Read
// fragments").
func TestGoalReadFragmentsCarrySnapshotVersion(t *testing.T) {
	body := goalsToolsLargeFixtureBody(t)
	srv := fakeserver.New(t)
	srv.Handle("GET", "^/v1/goals/"+goalsToolsTestID+"$", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
	s := goalsToolsTestSession(t, srv)

	wantValue := readToolsLoadFixture(t, "goal_large.json")
	wantText := envelopeEncode(wantValue)
	wantRunes := len([]rune(wantText))

	const wantGuidance = "Pass snapshot_version as version on every remaining fragment. If the goal changes, restart the read from offset zero."

	var content strings.Builder
	offset := 0
	fragments := 0
	first := true
	for {
		args := map[string]any{"id": goalsToolsTestID, "offset": float64(offset)}
		result, err := goalsToolsRead(context.Background(), s, args)
		if err != nil {
			t.Fatalf("offset %d: handler error = %v", offset, err)
		}
		res, ok := result.(Result)
		if !ok {
			t.Fatalf("offset %d: result = %#v, want a Result", offset, result)
		}
		frag, ok := res.Value.(map[string]any)
		if !ok {
			t.Fatalf("offset %d: value = %#v, want a map", offset, res.Value)
		}
		fragments++

		if frag["format"] != "json_fragment" {
			t.Fatalf("offset %d: format = %v, want json_fragment", offset, frag["format"])
		}
		if first {
			if frag["snapshot_version"] != float64(7) {
				t.Errorf("snapshot_version = %v, want 7", frag["snapshot_version"])
			}
			if frag["guidance"] != wantGuidance {
				t.Errorf("guidance = %q, want %q", frag["guidance"], wantGuidance)
			}
			first = false
		}

		part, ok := frag["content"].(string)
		if !ok {
			t.Fatalf("offset %d: content = %#v, want a string", offset, frag["content"])
		}
		content.WriteString(part)

		if frag["total_chars"] != wantRunes {
			t.Fatalf("offset %d: total_chars = %#v, want %d", offset, frag["total_chars"], wantRunes)
		}

		s.Cache.Remember(res.Receipts...)

		next := frag["next_offset"]
		if next == nil {
			break
		}
		nextOffset, ok := next.(int)
		if !ok {
			t.Fatalf("offset %d: next_offset = %#v, want an int", offset, next)
		}
		if nextOffset <= offset {
			t.Fatalf("offset %d: next_offset = %d, want forward progress", offset, nextOffset)
		}
		offset = nextOffset

		if fragments > 1000 {
			t.Fatal("did not terminate after 1000 fragments")
		}
	}

	if fragments < 2 {
		t.Fatalf("fragments = %d, want more than 1 for a ~13KB goal", fragments)
	}
	if content.String() != wantText {
		t.Errorf("reconstructed content does not match the source text")
	}
}

// TestGoalEnvelopeHasAuthorityModel proves an ajent_goal* tool result, run
// through the Session dispatcher, carries the authority_model statement in
// its envelope (docs/tasks/T3.8.md step 7; docs/contract.md section 11,
// "Envelope").
func TestGoalEnvelopeHasAuthorityModel(t *testing.T) {
	srv := fakeserver.New(t)
	s := goalsToolsTestSession(t, srv)
	d := &dispatcher{s: s}

	result, _, err := d.CallTool(context.Background(), serveTestCallParams(t, "ajent_goals", map[string]any{}))
	if err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	if serveTestIsError(t, result) {
		t.Fatalf("isError = true, want false: %s", serveTestContentText(t, result))
	}
	text := serveTestContentText(t, result)
	if !strings.Contains(text, `"authority_model"`) {
		t.Errorf("text = %s, want it to contain %q", text, `"authority_model"`)
	}
}
