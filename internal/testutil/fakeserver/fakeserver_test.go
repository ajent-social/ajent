// SPDX-License-Identifier: Apache-2.0

package fakeserver

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// uuidPlaceholder matches the [a-f0-9-]{36} fragment routes.json uses in a
// pattern; tests substitute a concrete UUID for it.
var uuidPlaceholder = regexp.MustCompile(`\[a-f0-9-\]\{36\}`)

const testUUID = "00000000-0000-4000-8000-000000000099"

// concretePath turns a routes.json regexp pattern into a literal request
// path by stripping the anchors and substituting a UUID for the
// [a-f0-9-]{36} placeholder.
func concretePath(pattern string) string {
	path := strings.TrimPrefix(pattern, "^")
	path = strings.TrimSuffix(path, "$")
	return uuidPlaceholder.ReplaceAllString(path, testUUID)
}

// TestFakeServerDefaults checks that every route in routes.json answers
// with its fixture's status and body.
func TestFakeServerDefaults(t *testing.T) {
	root, err := moduleRoot()
	if err != nil {
		t.Fatalf("moduleRoot: %v", err)
	}
	contractDir := filepath.Join(root, "testdata", "contract")

	//nolint:gosec // contractDir is the module's own testdata/contract, not
	// an externally controlled path.
	raw, err := os.ReadFile(filepath.Join(contractDir, "routes.json"))
	if err != nil {
		t.Fatalf("ReadFile(routes.json): %v", err)
	}
	var entries []routeEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("Unmarshal(routes.json): %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("routes.json has no entries")
	}

	s := New(t)

	for _, e := range entries {
		e := e
		t.Run(e.Method+" "+e.Pattern, func(t *testing.T) {
			//nolint:gosec // e.Response comes from the module's own
			// testdata/contract/routes.json fixture, not external input.
			want, err := os.ReadFile(filepath.Join(contractDir, e.Response))
			if err != nil {
				t.Fatalf("ReadFile(%s): %v", e.Response, err)
			}

			req, err := http.NewRequest(e.Method, s.URL+concretePath(e.Pattern), nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != e.Status {
				t.Errorf("status = %d, want %d", resp.StatusCode, e.Status)
			}
			if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			got, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("body = %s, want %s", got, want)
			}
		})
	}
}

func TestFakeServerHandleOverride(t *testing.T) {
	s := New(t)

	s.Handle("GET", "/v1/me", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(`{"overridden":true}`))
	})

	resp, err := http.Get(s.URL + "/v1/me")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusTeapot {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusTeapot)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != `{"overridden":true}` {
		t.Errorf("body = %s, want the overridden body", got)
	}
}

func TestFakeServerHandleRegexp(t *testing.T) {
	s := New(t)

	var seenPath string
	s.Handle("GET", `^/v1/custom/[0-9]+$`, func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})

	resp, err := http.Get(s.URL + "/v1/custom/42")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if seenPath != "/v1/custom/42" {
		t.Errorf("seenPath = %q, want /v1/custom/42", seenPath)
	}
}

func TestFakeServerUnmatchedRoute(t *testing.T) {
	s := New(t)

	resp, err := http.Get(s.URL + "/v1/does-not-exist")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != `{"detail":"not found"}` {
		t.Errorf("body = %s, want the not-found body", got)
	}
}

func TestFakeServerRequestsAndCredential(t *testing.T) {
	s := New(t)

	if got := s.Requests(); len(got) != 0 {
		t.Fatalf("Requests() = %v, want empty before any request", got)
	}

	req, err := http.NewRequest("POST", s.URL+"/v1/posts?x=1", strings.NewReader(`{"a":1}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Idempotency-Key", "k1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	reqs := s.Requests()
	if len(reqs) != 1 {
		t.Fatalf("len(Requests()) = %d, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Method != "POST" || r.Path != "/v1/posts" || r.RawQuery != "x=1" {
		t.Errorf("Recorded = %+v, want method POST, path /v1/posts, query x=1", r)
	}
	if r.Header.Get("Idempotency-Key") != "k1" {
		t.Errorf("Header Idempotency-Key = %q, want k1", r.Header.Get("Idempotency-Key"))
	}
	if string(r.Body) != `{"a":1}` {
		t.Errorf("Body = %s, want {\"a\":1}", r.Body)
	}

	cred := s.Credential()
	if cred.Server != s.URL {
		t.Errorf("Credential().Server = %q, want %q", cred.Server, s.URL)
	}
	if cred.Token == "" {
		t.Error("Credential().Token is empty")
	}
}

func TestFakeServerSetsAJENTURL(t *testing.T) {
	s := New(t)
	if got := os.Getenv("AJENT_URL"); got != s.URL {
		t.Errorf("AJENT_URL = %q, want %q", got, s.URL)
	}
}
