// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/ajent-social/ajent/internal/api"
	"github.com/ajent-social/ajent/internal/credentials"
	"github.com/ajent-social/ajent/internal/testutil/fakeserver"
)

// readToolsTestSession builds a Session wired to srv, with a fixed profile
// credential carrying a group_id and agent_id so every read tool's request
// path can be built.
func readToolsTestSession(t *testing.T, srv *fakeserver.Server) *Session {
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

// TestReadToolsRequests proves each of the six read tools sends the exact
// method, path and query the contract specifies (docs/tasks/T3.3.md step 9;
// testdata/contract/tools.json).
func TestReadToolsRequests(t *testing.T) {
	tests := []struct {
		name       string
		call       func(s *Session) (any, error)
		wantMethod string
		wantPath   string
		wantQuery  url.Values
	}{
		{
			name:       "ajent_me",
			call:       func(s *Session) (any, error) { return readToolsMe(context.Background(), s, map[string]any{}) },
			wantMethod: "GET",
			wantPath:   "/v1/me",
		},
		{
			name: "ajent_search query",
			call: func(s *Session) (any, error) {
				return readToolsSearch(context.Background(), s, map[string]any{"query": "example query"})
			},
			wantMethod: "GET",
			wantPath:   "/v1/search",
			wantQuery:  url.Values{"q": {"example query"}, "limit": {"5"}},
		},
		{
			name: "ajent_search filters",
			call: func(s *Session) (any, error) {
				return readToolsSearch(context.Background(), s, map[string]any{
					"query":          "example query",
					"limit":          float64(3),
					"group_id":       "00000000-0000-4000-8000-000000000003",
					"kind":           "question",
					"question_state": "open",
				})
			},
			wantMethod: "GET",
			wantPath:   "/v1/search",
			wantQuery: url.Values{
				"q":              {"example query"},
				"limit":          {"3"},
				"group_id":       {"00000000-0000-4000-8000-000000000003"},
				"kind":           {"question"},
				"question_state": {"open"},
			},
		},
		{
			name:       "ajent_feed workspace",
			call:       func(s *Session) (any, error) { return readToolsFeed(context.Background(), s, map[string]any{}) },
			wantMethod: "GET",
			wantPath:   "/v1/groups/00000000-0000-4000-8000-000000000003/posts",
		},
		{
			name: "ajent_feed cursor",
			call: func(s *Session) (any, error) {
				return readToolsFeed(context.Background(), s, map[string]any{"cursor": "example-cursor"})
			},
			wantMethod: "GET",
			wantPath:   "/v1/groups/00000000-0000-4000-8000-000000000003/posts",
			wantQuery:  url.Values{"before": {"example-cursor"}},
		},
		{
			name: "ajent_read",
			call: func(s *Session) (any, error) {
				return readToolsRead(context.Background(), s, map[string]any{"id": "00000000-0000-4000-8000-000000000031"})
			},
			wantMethod: "GET",
			wantPath:   "/v1/posts/00000000-0000-4000-8000-000000000031",
		},
		{
			name: "ajent_read offset",
			call: func(s *Session) (any, error) {
				return readToolsRead(context.Background(), s, map[string]any{
					"id":     "00000000-0000-4000-8000-000000000031",
					"offset": float64(1800),
				})
			},
			wantMethod: "GET",
			wantPath:   "/v1/posts/00000000-0000-4000-8000-000000000031",
			wantQuery:  url.Values{"offset": {"1800"}},
		},
		{
			name:       "ajent_inbox",
			call:       func(s *Session) (any, error) { return readToolsInbox(context.Background(), s, map[string]any{}) },
			wantMethod: "GET",
			wantPath:   "/v1/inbox",
		},
		{
			name:       "ajent_diagnose",
			call:       func(s *Session) (any, error) { return readToolsDiagnose(context.Background(), s, map[string]any{}) },
			wantMethod: "GET",
			wantPath:   "/v1/reliability",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := fakeserver.New(t)
			s := readToolsTestSession(t, srv)

			if _, err := tt.call(s); err != nil {
				t.Fatalf("handler error = %v", err)
			}

			// A capability-gated case (question_state) issues a
			// /v1/reliability capability probe before the tool's own
			// request, so assert on the last request recorded rather than
			// requiring exactly one.
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
		})
	}
}

// TestSearchQuestionStateRequiresCapability proves ajent_search checks the
// question_checks capability before sending a request when question_state
// is set, and never calls /v1/search when the capability is absent
// (docs/tasks/T3.3.md step 9).
func TestSearchQuestionStateRequiresCapability(t *testing.T) {
	srv := fakeserver.New(t)
	srv.Handle("GET", "^/v1/reliability$", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","capabilities":[]}`))
	})
	s := readToolsTestSession(t, srv)

	_, err := readToolsSearch(context.Background(), s, map[string]any{
		"query":          "example",
		"question_state": "open",
	})

	var capErr *api.UnsupportedCapabilityError
	if !errors.As(err, &capErr) {
		t.Fatalf("error = %v (%T), want *api.UnsupportedCapabilityError", err, err)
	}
	if capErr.Capability != "question_checks" {
		t.Errorf("capability = %q, want question_checks", capErr.Capability)
	}

	for _, r := range srv.Requests() {
		if r.Path == "/v1/search" {
			t.Fatalf("unexpected /v1/search request: %+v", r)
		}
	}
}
