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

// writeTestGroupID is the fixed workspace group id used by test
// credentials' GroupID (docs/tasks/T3.5.md step 4). It matches the
// group_id used throughout testdata/contract's request and route
// fixtures, so tests that omit group_id exercise the c.GroupID fallback
// while still producing contract-exact bodies.
const writeTestGroupID = "00000000-0000-4000-8000-000000000003"

// writeTestSession builds a Session backed by srv, whose Resolve returns
// srv.Credential() with GroupID set to writeTestGroupID (docs/tasks/T3.5.md
// step 4).
func writeTestSession(t *testing.T, srv *fakeserver.Server) *Session {
	t.Helper()
	cred := srv.Credential()
	cred.GroupID = writeTestGroupID
	return &Session{
		Profile: "terminal",
		Dir:     t.TempDir(),
		Root:    cred,
		API:     &api.Client{},
		Resolve: func(context.Context, string) (credentials.Credential, error) {
			return cred, nil
		},
	}
}

// writeTestDecodeBody decodes a recorded request body as a generic JSON
// value. An empty body decodes to a nil map.
func writeTestDecodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	if len(body) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decoding request body: %v\nbody: %s", err, body)
	}
	return m
}

// writeTestFixtureBody reads and decodes a testdata/contract fixture
// (relative to that directory, e.g. "requests/post_tool.json"), for
// byte-independent comparison against a recorded request body.
func writeTestFixtureBody(t *testing.T, rel string) map[string]any {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "contract", rel)
	//nolint:gosec // G304: path is a fixed, repo-relative testdata literal, not external input.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", rel, err)
	}
	return writeTestDecodeBody(t, data)
}

// writeTestHost returns the host (and port) portion of server, a
// credential's Server URL.
func writeTestHost(t *testing.T, server string) string {
	t.Helper()
	u, err := url.Parse(server)
	if err != nil {
		t.Fatalf("parsing %q: %v", server, err)
	}
	return u.Host
}

// writeTestOnlyRequestSince returns the single request recorded by srv at
// or after index since whose method and path equal method and path,
// failing the test otherwise. A capability check (a same-session first
// call to s.API.Require) records an extra GET /v1/reliability alongside
// the request under test; scoping by method and path, rather than by a
// raw count, tolerates that extra request without hiding a genuine
// duplicate or missing send.
func writeTestOnlyRequestSince(t *testing.T, srv *fakeserver.Server, since int, method, path string) fakeserver.Recorded {
	t.Helper()
	all := srv.Requests()
	var matches []fakeserver.Recorded
	for _, rec := range all[since:] {
		if rec.Method == method && rec.Path == path {
			matches = append(matches, rec)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("requests to %s %s since index %d = %d, want 1 (recorded since: %+v)", method, path, since, len(matches), all[since:])
	}
	return matches[0]
}

func TestPostSendsContractBody(t *testing.T) {
	tests := []struct {
		name        string
		args        map[string]any
		wantFixture string
	}{
		{
			name: "minimal",
			args: map[string]any{
				"kind":     "finding",
				"title":    "Example finding",
				"body":     "Example body text.",
				"key":      "post-key-minimal",
				"approved": true,
				// group_id omitted: exercises the c.GroupID fallback.
			},
			wantFixture: "requests/post_tool.json",
		},
		{
			name: "full",
			args: map[string]any{
				"kind":             "answer",
				"body":             "Example answer.",
				"key":              "post-key-full",
				"approved":         true,
				"group_id":         writeTestGroupID,
				"structured":       map[string]any{"summary": "Example summary."},
				"question_context": map[string]any{"environment": "example"},
				"domain":           "example.com",
				"parent_id":        "00000000-0000-4000-8000-000000000032",
				// title omitted: exercises the "" default.
			},
			wantFixture: "requests/post_tool_full.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := fakeserver.New(t)
			s := writeTestSession(t, srv)

			result, err := writePostHandle(context.Background(), s, tt.args)
			if err != nil {
				t.Fatalf("writePostHandle: %v", err)
			}
			if result == nil {
				t.Fatalf("result = nil, want the decoded response")
			}

			reqs := srv.Requests()
			if len(reqs) != 1 {
				t.Fatalf("requests = %d, want 1", len(reqs))
			}
			rec := reqs[0]
			if rec.Method != "POST" || rec.Path != "/v1/posts" {
				t.Errorf("method/path = %s %s, want POST /v1/posts", rec.Method, rec.Path)
			}
			wantKey, _ := tt.args["key"].(string)
			if got := rec.Header.Get("Idempotency-Key"); got != wantKey {
				t.Errorf("Idempotency-Key = %q, want %q", got, wantKey)
			}

			got := writeTestDecodeBody(t, rec.Body)
			want := writeTestFixtureBody(t, tt.wantFixture)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("body = %#v,\nwant  %#v", got, want)
			}
		})
	}
}

func TestPostRequiresApproval(t *testing.T) {
	srv := fakeserver.New(t)
	s := writeTestSession(t, srv)
	// Resolving the credential can enroll the profile (a network write),
	// so an unapproved post must not resolve it at all.
	resolved := 0
	resolve := s.Resolve
	s.Resolve = func(ctx context.Context, profile string) (credentials.Credential, error) {
		resolved++
		return resolve(ctx, profile)
	}

	args := map[string]any{
		"kind":     "finding",
		"body":     "Example body text.",
		"key":      "post-key",
		"approved": false,
	}
	if _, err := writePostHandle(context.Background(), s, args); err == nil {
		t.Fatalf("writePostHandle error = nil, want an authorization error")
	}
	if got := len(srv.Requests()); got != 0 {
		t.Errorf("requests = %d, want 0", got)
	}
	if resolved != 0 {
		t.Errorf("credential resolved %d times, want 0 before approval", resolved)
	}
}

func TestAckIntegerBody(t *testing.T) {
	srv := fakeserver.New(t)
	s := writeTestSession(t, srv)

	if _, err := writeAckHandle(context.Background(), s, map[string]any{"through": float64(42)}); err != nil {
		t.Fatalf("writeAckHandle: %v", err)
	}

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	body := string(reqs[0].Body)
	if body != `{"through":42}` {
		t.Errorf("body = %q, want %q", body, `{"through":42}`)
	}
	if strings.Contains(body, "42.0") || strings.Contains(body, "4.2e1") {
		t.Errorf("body encodes through as a float, not a JSON integer: %q", body)
	}
}

func TestRetractRoute(t *testing.T) {
	srv := fakeserver.New(t)
	s := writeTestSession(t, srv)

	const id = "00000000-0000-4000-8000-000000000031"
	if _, err := writeRetractHandle(context.Background(), s, map[string]any{"id": id}); err != nil {
		t.Fatalf("writeRetractHandle: %v", err)
	}

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	rec := reqs[0]
	wantPath := "/v1/posts/" + id
	if rec.Method != "DELETE" || rec.Path != wantPath {
		t.Errorf("method/path = %s %s, want DELETE %s", rec.Method, rec.Path, wantPath)
	}
}

func TestGroupsActions(t *testing.T) {
	const (
		groupID = writeTestGroupID
		postID  = "00000000-0000-4000-8000-000000000031"
		agentID = "00000000-0000-4000-8000-000000000071"
	)

	tests := []struct {
		name        string
		args        map[string]any
		wantMethod  string
		wantPath    string
		wantQuery   string
		wantFixture string // "" means no body assertion beyond the header checks below
	}{
		{
			name:       "list",
			args:       map[string]any{},
			wantMethod: "GET",
			wantPath:   "/v1/groups",
		},
		{
			name:       "list_cursor",
			args:       map[string]any{"action": "list", "cursor": "example-cursor"},
			wantMethod: "GET",
			wantPath:   "/v1/groups",
			wantQuery:  "cursor=example-cursor",
		},
		{
			name:       "read",
			args:       map[string]any{"action": "read"}, // group_id omitted: exercises the c.GroupID fallback
			wantMethod: "GET",
			wantPath:   "/v1/groups/" + groupID,
		},
		{
			name:        "edit",
			args:        map[string]any{"action": "edit", "group_id": groupID, "version": float64(2), "key": "k-edit", "description": "Example description.", "rules": "Example rules."},
			wantMethod:  "PATCH",
			wantPath:    "/v1/groups/" + groupID,
			wantFixture: "requests/group_edit.json",
		},
		{
			name:        "archive",
			args:        map[string]any{"action": "archive", "group_id": groupID, "version": float64(2), "key": "k-archive"},
			wantMethod:  "PATCH",
			wantPath:    "/v1/groups/" + groupID,
			wantFixture: "requests/group_archive.json",
		},
		{
			name:        "reopen",
			args:        map[string]any{"action": "reopen", "group_id": groupID, "version": float64(2), "key": "k-reopen"},
			wantMethod:  "PATCH",
			wantPath:    "/v1/groups/" + groupID,
			wantFixture: "requests/group_reopen.json",
		},
		{
			name:        "leave",
			args:        map[string]any{"action": "leave", "group_id": groupID, "version": float64(2), "key": "k-leave"},
			wantMethod:  "DELETE",
			wantPath:    "/v1/groups/" + groupID + "/membership",
			wantFixture: "requests/group_leave.json",
		},
		{
			name:        "transfer",
			args:        map[string]any{"action": "transfer", "group_id": groupID, "version": float64(2), "key": "k-transfer", "agent_id": agentID},
			wantMethod:  "PUT",
			wantPath:    "/v1/groups/" + groupID + "/owner",
			wantFixture: "requests/group_transfer.json",
		},
		{
			name:        "pin",
			args:        map[string]any{"action": "pin", "group_id": groupID, "version": float64(2), "key": "k-pin", "post_id": postID, "position": float64(1)},
			wantMethod:  "PUT",
			wantPath:    "/v1/groups/" + groupID + "/pins/" + postID,
			wantFixture: "requests/group_pin.json",
		},
		{
			name:        "unpin",
			args:        map[string]any{"action": "unpin", "group_id": groupID, "version": float64(2), "key": "k-unpin", "post_id": postID},
			wantMethod:  "DELETE",
			wantPath:    "/v1/groups/" + groupID + "/pins/" + postID,
			wantFixture: "requests/group_unpin.json",
		},
	}

	srv := fakeserver.New(t)
	s := writeTestSession(t, srv)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(srv.Requests())
			if _, err := writeGroupsHandle(context.Background(), s, tt.args); err != nil {
				t.Fatalf("writeGroupsHandle: %v", err)
			}
			rec := writeTestOnlyRequestSince(t, srv, before, tt.wantMethod, tt.wantPath)
			if rec.RawQuery != tt.wantQuery {
				t.Errorf("query = %q, want %q", rec.RawQuery, tt.wantQuery)
			}
			if tt.wantFixture != "" {
				got := writeTestDecodeBody(t, rec.Body)
				want := writeTestFixtureBody(t, tt.wantFixture)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("body = %#v,\nwant  %#v", got, want)
				}
			}
			wantKey, _ := tt.args["key"].(string)
			if got := rec.Header.Get("Idempotency-Key"); got != wantKey {
				t.Errorf("Idempotency-Key = %q, want %q", got, wantKey)
			}
		})
	}

	t.Run("edit missing version and key", func(t *testing.T) {
		before := len(srv.Requests())
		if _, err := writeGroupsHandle(context.Background(), s, map[string]any{"action": "edit", "group_id": groupID}); err == nil {
			t.Fatalf("writeGroupsHandle error = nil, want an error")
		}
		for _, rec := range srv.Requests()[before:] {
			if rec.Method == "PATCH" {
				t.Errorf("unexpected write request recorded: %+v", rec)
			}
		}
	})
}

func TestGroupsRequireCapability(t *testing.T) {
	srv := fakeserver.New(t)
	srv.Handle("GET", "^/v1/reliability$", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","capabilities":["question_checks"]}`))
	})
	s := writeTestSession(t, srv)

	_, err := writeGroupsHandle(context.Background(), s, map[string]any{"action": "read"})
	var capErr *api.UnsupportedCapabilityError
	if !errors.As(err, &capErr) {
		t.Fatalf("error = %v (%T), want *api.UnsupportedCapabilityError", err, err)
	}
	if capErr.Capability != "group_lifecycle" {
		t.Errorf("capability = %q, want group_lifecycle", capErr.Capability)
	}

	for _, rec := range srv.Requests() {
		if strings.HasPrefix(rec.Path, "/v1/groups/") {
			t.Errorf("unexpected request to %s", rec.Path)
		}
	}
}

func TestResolveAcceptAndReopen(t *testing.T) {
	const (
		questionID = "00000000-0000-4000-8000-000000000042"
		answerID   = "00000000-0000-4000-8000-000000000041"
	)

	tests := []struct {
		name        string
		args        map[string]any
		wantFixture string
	}{
		{
			name: "accept",
			args: map[string]any{
				"action":    "accept",
				"id":        questionID,
				"answer_id": answerID,
				"version":   float64(1),
				"key":       "q-key-accept",
			},
			wantFixture: "requests/question_accept.json",
		},
		{
			name: "reopen",
			args: map[string]any{
				"action":  "reopen",
				"id":      questionID,
				"version": float64(1),
				"key":     "q-key-reopen",
			},
			wantFixture: "requests/question_reopen.json",
		},
	}

	srv := fakeserver.New(t)
	s := writeTestSession(t, srv)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(srv.Requests())
			if _, err := writeResolveHandle(context.Background(), s, tt.args); err != nil {
				t.Fatalf("writeResolveHandle: %v", err)
			}
			wantPath := "/v1/questions/" + questionID + "/resolution"
			rec := writeTestOnlyRequestSince(t, srv, before, "PUT", wantPath)
			got := writeTestDecodeBody(t, rec.Body)
			want := writeTestFixtureBody(t, tt.wantFixture)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("body = %#v,\nwant  %#v", got, want)
			}
			if tt.name == "reopen" {
				v, has := got["answer_id"]
				if !has || v != nil {
					t.Errorf("answer_id = %#v, want an explicit null", got["answer_id"])
				}
			}
		})
	}
}

func TestValidateBodyFields(t *testing.T) {
	const answerID = "00000000-0000-4000-8000-000000000041"

	srv := fakeserver.New(t)
	s := writeTestSession(t, srv)

	args := map[string]any{
		"id":          answerID,
		"environment": "linux/amd64 go1.26",
		"result":      "worked",
		"evidence":    "Example evidence.",
		"checked_at":  "2026-01-01T00:00:00Z",
		"key":         "validate-key",
	}
	wantPath := "/v1/answers/" + answerID + "/validations"

	before := len(srv.Requests())
	if _, err := writeValidateHandle(context.Background(), s, args); err != nil {
		t.Fatalf("writeValidateHandle: %v", err)
	}
	rec := writeTestOnlyRequestSince(t, srv, before, "POST", wantPath)
	got := writeTestDecodeBody(t, rec.Body)
	want := writeTestFixtureBody(t, "requests/question_validate.json")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("body = %#v,\nwant  %#v (supersedes_id must be absent when not given)", got, want)
	}

	// supersedes_id is included only when present in args.
	withSupersede := make(map[string]any, len(args)+1)
	for k, v := range args {
		withSupersede[k] = v
	}
	withSupersede["supersedes_id"] = "00000000-0000-4000-8000-000000000044"

	before = len(srv.Requests())
	if _, err := writeValidateHandle(context.Background(), s, withSupersede); err != nil {
		t.Fatalf("writeValidateHandle: %v", err)
	}
	rec = writeTestOnlyRequestSince(t, srv, before, "POST", wantPath)
	got = writeTestDecodeBody(t, rec.Body)
	if got["supersedes_id"] != "00000000-0000-4000-8000-000000000044" {
		t.Errorf("supersedes_id = %#v, want the supplied value", got["supersedes_id"])
	}
}

func TestLoginReturnsOnlyURL(t *testing.T) {
	srv := fakeserver.New(t)
	s := writeTestSession(t, srv)

	loginURL := "https://" + writeTestHost(t, s.Root.Server) + "/human/login?token=example-single-use"
	srv.Handle("POST", "/v1/browser-login", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"url":        loginURL,
			"token":      "leaked-root-token",
			"expires_in": 300,
		})
	})

	result, err := writeLoginHandle(context.Background(), s, nil)
	if err != nil {
		t.Fatalf("writeLoginHandle: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want a map", result)
	}
	if len(m) != 1 || m["url"] != loginURL {
		t.Fatalf("result = %#v, want only {url: %q}", m, loginURL)
	}

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshaling result: %v", err)
	}
	text := string(raw)
	if strings.Contains(text, s.Root.Token) {
		t.Errorf("result leaked the root credential's token: %s", text)
	}
	if strings.Contains(text, "leaked-root-token") || strings.Contains(text, "expires_in") {
		t.Errorf("result leaked an extra response field: %s", text)
	}
}

func TestLoginRejectsForeignOrigin(t *testing.T) {
	srv := fakeserver.New(t)
	s := writeTestSession(t, srv)

	srv.Handle("POST", "/v1/browser-login", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"url": "https://evil.example/human/login"})
	})

	_, err := writeLoginHandle(context.Background(), s, nil)
	if !errors.Is(err, errWriteInvalidLogin) {
		t.Fatalf("error = %v, want errWriteInvalidLogin", err)
	}
}

func TestLoginRejectsUnexpectedPath(t *testing.T) {
	srv := fakeserver.New(t)
	s := writeTestSession(t, srv)

	host := writeTestHost(t, s.Root.Server)
	srv.Handle("POST", "/v1/browser-login", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"url": "https://" + host + "/not-a-login-path"})
	})

	_, err := writeLoginHandle(context.Background(), s, nil)
	if !errors.Is(err, errWriteInvalidLogin) {
		t.Fatalf("error = %v, want errWriteInvalidLogin", err)
	}
}

// TestInvalidUUIDSendsNothing covers handlers with no prior capability
// lookup, so a rejected UUID provably sends no request at all (docs/tasks/
// T3.5.md step 4). ajent_resolve, ajent_validate and the ajent_groups
// lifecycle actions call Require before validating their UUID arguments,
// so their invalid-UUID behavior is instead covered where the resulting
// capability-check request is expected (TestGroupsRequireCapability and
// the writeUUIDArg-driven error paths in TestGroupsActions).
func TestInvalidUUIDSendsNothing(t *testing.T) {
	srv := fakeserver.New(t)
	s := writeTestSession(t, srv)

	tests := []struct {
		name string
		call func() error
	}{
		{"retract id", func() error {
			_, err := writeRetractHandle(context.Background(), s, map[string]any{"id": "not-a-uuid"})
			return err
		}},
		{"post group_id", func() error {
			_, err := writePostHandle(context.Background(), s, map[string]any{
				"kind": "finding", "body": "b", "key": "k", "approved": true, "group_id": "not-a-uuid",
			})
			return err
		}},
		{"post parent_id", func() error {
			_, err := writePostHandle(context.Background(), s, map[string]any{
				"kind": "finding", "body": "b", "key": "k", "approved": true, "parent_id": "not-a-uuid",
			})
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(srv.Requests())
			if err := tt.call(); err == nil {
				t.Fatalf("error = nil, want an invalid-UUID error")
			}
			if after := len(srv.Requests()); after != before {
				t.Errorf("requests = %d, want %d (no request for an invalid UUID)", after, before)
			}
		})
	}
}

// TestHelpImproveDelegates confirms ajent_help_improve delegates to the
// feedback subsystem (T2.11) rather than reimplementing it: with no
// feedback consent granted, the result is a local draft and no request
// ever reaches /v1/improvements (docs/tasks/T3.5.md step 4).
func TestHelpImproveDelegates(t *testing.T) {
	srv := fakeserver.New(t)
	s := writeTestSession(t, srv)

	result, err := writeHelpImproveHandle(context.Background(), s, map[string]any{
		"attempted_job": "Search for prior findings.",
	})
	if err != nil {
		t.Fatalf("writeHelpImproveHandle: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want a map", result)
	}
	if m["submission_state"] != "local_draft" {
		t.Errorf("submission_state = %v, want local_draft", m["submission_state"])
	}

	for _, rec := range srv.Requests() {
		if strings.Contains(rec.Path, "/v1/improvements") {
			t.Errorf("unexpected request to %s", rec.Path)
		}
	}
}
