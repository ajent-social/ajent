// SPDX-License-Identifier: Apache-2.0

// Package fakeserver provides a fixture-backed httptest server for tests
// that exercise internal/api (and its consumers) without making real
// network calls. Responses default to testdata/contract/routes.json; tests
// override single routes with Handle.
package fakeserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/ajent-social/ajent/internal/credentials"
)

// Recorded captures one request the fake server received.
type Recorded struct {
	Method, Path, RawQuery string
	Header                 http.Header
	Body                   []byte
}

// matcher matches an exact path or, when the route starts with "^", a
// compiled regexp against the path.
type matcher struct {
	exact   string
	pattern *regexp.Regexp
}

func newMatcher(route string) matcher {
	if strings.HasPrefix(route, "^") {
		return matcher{pattern: regexp.MustCompile(route)}
	}
	return matcher{exact: route}
}

func (m matcher) match(path string) bool {
	if m.pattern != nil {
		return m.pattern.MatchString(path)
	}
	return m.exact == path
}

type fixtureRoute struct {
	method  string
	matcher matcher
	status  int
	body    []byte
}

type handlerEntry struct {
	method  string
	matcher matcher
	handler http.HandlerFunc
}

// Server is a fixture-backed fake of the hosted service.
type Server struct {
	URL string

	mu       sync.Mutex
	handlers []handlerEntry
	requests []Recorded

	routes []fixtureRoute
	srv    *httptest.Server
}

// routeEntry is one row of testdata/contract/routes.json.
type routeEntry struct {
	Method   string `json:"method"`
	Pattern  string `json:"pattern"`
	Status   int    `json:"status"`
	Response string `json:"response"`
}

// New starts an httptest.Server that serves responses from
// testdata/contract fixtures and records every request. Tests override
// single routes with Handle. New also sets AJENT_URL to the server's URL,
// so the loopback origin is trusted for the lifetime of the test.
func New(t testing.TB) *Server {
	t.Helper()

	root, err := moduleRoot()
	if err != nil {
		t.Fatalf("fakeserver: %v", err)
	}
	contractDir := filepath.Join(root, "testdata", "contract")

	//nolint:gosec // contractDir is the module's own testdata/contract, not
	// an externally controlled path.
	raw, err := os.ReadFile(filepath.Join(contractDir, "routes.json"))
	if err != nil {
		t.Fatalf("fakeserver: reading routes.json: %v", err)
	}
	var entries []routeEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("fakeserver: parsing routes.json: %v", err)
	}

	routes := make([]fixtureRoute, 0, len(entries))
	for _, e := range entries {
		//nolint:gosec // e.Response comes from the module's own
		// testdata/contract/routes.json fixture, not external input.
		body, err := os.ReadFile(filepath.Join(contractDir, e.Response))
		if err != nil {
			t.Fatalf("fakeserver: reading %s: %v", e.Response, err)
		}
		routes = append(routes, fixtureRoute{
			method:  e.Method,
			matcher: newMatcher(e.Pattern),
			status:  e.Status,
			body:    body,
		})
	}

	s := &Server{routes: routes}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serveHTTP))
	s.URL = s.srv.URL

	t.Cleanup(s.srv.Close)
	t.Setenv("AJENT_URL", s.URL)

	return s
}

// moduleRoot locates the module root by walking up from the working
// directory until go.mod is found.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("fakeserver: go.mod not found above " + dir)
		}
		dir = parent
	}
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	bodyBytes, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	s.mu.Lock()
	s.requests = append(s.requests, Recorded{
		Method:   r.Method,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
		Header:   r.Header.Clone(),
		Body:     bodyBytes,
	})
	handlers := make([]handlerEntry, len(s.handlers))
	copy(handlers, s.handlers)
	s.mu.Unlock()

	for _, h := range handlers {
		if h.method == r.Method && h.matcher.match(r.URL.Path) {
			h.handler(w, r)
			return
		}
	}

	for _, rt := range s.routes {
		if rt.method == r.Method && rt.matcher.match(r.URL.Path) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(rt.status)
			_, _ = w.Write(rt.body)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"detail":"not found"}`))
}

// Handle registers h to answer method+route, overriding the routes.json
// default. route is an exact path, or a regexp when it starts with "^".
// Handlers registered this way win over routes.json defaults, in
// registration order.
func (s *Server) Handle(method, route string, h http.HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers = append(s.handlers, handlerEntry{method: method, matcher: newMatcher(route), handler: h})
}

// Requests returns every request the server has received so far, in
// order.
func (s *Server) Requests() []Recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Recorded, len(s.requests))
	copy(out, s.requests)
	return out
}

// Credential returns a credential trusted for this server.
func (s *Server) Credential() credentials.Credential {
	return credentials.Credential{
		Server: s.URL,
		Token:  "aj_0000000000000000_testtoken",
	}
}
