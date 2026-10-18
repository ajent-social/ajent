// SPDX-License-Identifier: Apache-2.0

package feedback

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ajent-social/ajent/internal/api"
	"github.com/ajent-social/ajent/internal/testutil/fakeserver"
)

// feedbackTestClient returns an api.Client wired to store's Observer, with
// no real retry delays.
func feedbackTestClient(store *Store) *api.Client {
	return &api.Client{Sleep: func(time.Duration) {}, Observer: store.Observer()}
}

// TestObserverIgnoresUnregisteredRoutes confirms that a route with no
// capture template (/v1/fleet) and a route explicitly excluded
// (/v1/improvements) both produce no records and do not touch the
// counters (docs/contract.md section 12; docs/tasks/T2.11.md step 7).
func TestObserverIgnoresUnregisteredRoutes(t *testing.T) {
	srv := fakeserver.New(t)
	cred := srv.Credential()
	store := &Store{Dir: t.TempDir()}
	c := feedbackTestClient(store)

	srv.Handle("GET", "/v1/fleet", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv.Handle("POST", "/v1/improvements", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	_ = c.Do(context.Background(), cred, api.Request{Method: "GET", Route: "/v1/fleet", Attempts: 1}, nil)
	_ = c.Do(context.Background(), cred, api.Request{Method: "POST", Route: "/v1/improvements", Attempts: 1}, nil)

	result, err := store.Local("preview")
	if err != nil {
		t.Fatalf("Local(preview) = %v", err)
	}
	m := result.(map[string]any)
	if got := len(m["records"].([]record)); got != 0 {
		t.Fatalf("records = %d, want 0", got)
	}
	if got := m["counters"].(map[string]int)["logical_operations"]; got != 0 {
		t.Fatalf("logical_operations = %d, want 0", got)
	}
}

// TestPayloadHasNoQueryText captures a failed search carrying a query
// string, then reports it with consent, and confirms the query text never
// reaches the outbound POST body or the on-disk state file: only the route
// template is recorded, never the raw route (docs/tasks/T2.11.md step 7).
func TestPayloadHasNoQueryText(t *testing.T) {
	const secret = "secret-query-text"

	srv := fakeserver.New(t)
	cred := srv.Credential()
	store := &Store{Dir: t.TempDir()}
	c := feedbackTestClient(store)

	srv.Handle("GET", "/v1/search", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	if err := c.Do(context.Background(), cred, api.Request{Method: "GET", Route: "/v1/search?q=" + secret, Attempts: 1}, nil); err == nil {
		t.Fatalf("Do() = nil, want an error")
	}

	if _, err := store.Local("set"); err != nil {
		t.Fatalf("Local(set) = %v", err)
	}

	var postBody []byte
	srv.Handle("POST", "/v1/improvements", func(w http.ResponseWriter, r *http.Request) {
		postBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"receipt","submission_state":"received"}`))
	})

	result, err := store.HelpImprove(context.Background(), c, cred, map[string]any{"attempted_job": "search for prior findings"})
	if err != nil {
		t.Fatalf("HelpImprove() = %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want a map", result)
	}
	if m["id"] != "receipt" {
		t.Fatalf("result = %#v, want the server response", result)
	}

	if strings.Contains(string(postBody), secret) {
		t.Fatalf("POST body contains query text: %s", postBody)
	}

	stateBytes, err := os.ReadFile(filepath.Join(store.Dir, feedbackDirName, feedbackStateFileName))
	if err != nil {
		t.Fatalf("reading state.json: %v", err)
	}
	if strings.Contains(string(stateBytes), secret) {
		t.Fatalf("state.json contains query text: %s", stateBytes)
	}

	preview, err := store.Local("preview")
	if err != nil {
		t.Fatalf("Local(preview) = %v", err)
	}
	records := preview.(map[string]any)["records"].([]record)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	evidence, ok := records[0]["evidence"].(map[string]any)
	if !ok {
		t.Fatalf("evidence = %#v, want a map", records[0]["evidence"])
	}
	if evidence["route"] != "/v1/search" {
		t.Errorf("evidence route = %v, want /v1/search", evidence["route"])
	}
}

// TestObserverCountsSuccess exercises the success-path counters: a
// successful call bumps successes, an empty search bumps
// empty_search_observations, and a retried success bumps
// successful_after_retry (docs/contract.md section 12).
func TestObserverCountsSuccess(t *testing.T) {
	srv := fakeserver.New(t)
	cred := srv.Credential()
	store := &Store{Dir: t.TempDir()}
	c := feedbackTestClient(store)

	var attempt int
	srv.Handle("GET", "/v1/search", func(w http.ResponseWriter, _ *http.Request) {
		attempt++
		if attempt == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"posts":[]}`))
	})

	var out map[string]any
	if err := c.Do(context.Background(), cred, api.Request{Method: "GET", Route: "/v1/search?q=x"}, &out); err != nil {
		t.Fatalf("Do() = %v", err)
	}

	result, err := store.Local("stats")
	if err != nil {
		t.Fatalf("Local(stats) = %v", err)
	}
	counters := result.(map[string]any)["counters"].(map[string]int)
	if counters["successes"] != 1 {
		t.Errorf("successes = %d, want 1", counters["successes"])
	}
	if counters["empty_search_observations"] != 1 {
		t.Errorf("empty_search_observations = %d, want 1", counters["empty_search_observations"])
	}
	if counters["successful_after_retry"] != 1 {
		t.Errorf("successful_after_retry = %d, want 1", counters["successful_after_retry"])
	}
	if counters["logical_failures"] != 0 {
		t.Errorf("logical_failures = %d, want 0", counters["logical_failures"])
	}
}
