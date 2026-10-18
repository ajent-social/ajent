// SPDX-License-Identifier: Apache-2.0

// External test package: fakerelease imports update (for Newer), so this
// file must not live in package update, or the two would form an import
// cycle in the test binary.
package update_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ajent-social/ajent/internal/testutil/fakerelease"
	"github.com/ajent-social/ajent/internal/update"
)

// recordingTransport wraps http.DefaultTransport, recording each request's
// headers and each response's status code, so tests can assert on exactly
// what Source sent and how the fake server answered (in particular, that a
// cached request really got a 304).
type recordingTransport struct {
	mu       sync.Mutex
	headers  []http.Header
	statuses []int
}

func (rt *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.headers = append(rt.headers, req.Header.Clone())
	rt.mu.Unlock()

	resp, err := http.DefaultTransport.RoundTrip(req)
	if err == nil {
		rt.mu.Lock()
		rt.statuses = append(rt.statuses, resp.StatusCode)
		rt.mu.Unlock()
	}
	return resp, err
}

// newTestSource returns a Source pointed at the fake release server s, with
// its own temp cache directory.
func newTestSource(t *testing.T, s *fakerelease.Server) *update.Source {
	t.Helper()
	return &update.Source{
		HTTP:        &http.Client{Timeout: 3 * time.Second},
		GitHubAPI:   s.API(),
		FallbackURL: s.FallbackURL(),
		CacheDir:    t.TempDir(),
	}
}

func TestLatestStable(t *testing.T) {
	s := fakerelease.New(t)
	s.AddRelease("0.7.0", false, nil)
	src := newTestSource(t, s)

	rel, err := src.Latest(context.Background(), false)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rel.Version != "0.7.0" {
		t.Errorf("Version = %q, want 0.7.0", rel.Version)
	}
	if rel.Prerelease {
		t.Error("Prerelease = true, want false")
	}
}

func TestLatestSkipsPrerelease(t *testing.T) {
	s := fakerelease.New(t)
	s.AddRelease("0.7.0", false, nil)
	s.AddRelease("0.8.0-rc.1", true, nil)
	src := newTestSource(t, s)

	rel, err := src.Latest(context.Background(), false)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rel.Version != "0.7.0" {
		t.Errorf("Version = %q, want 0.7.0 (prerelease must be skipped)", rel.Version)
	}
}

func TestLatestPre(t *testing.T) {
	s := fakerelease.New(t)
	s.AddRelease("0.7.0", false, nil)
	s.AddRelease("0.8.0-rc.1", true, nil)
	src := newTestSource(t, s)

	rel, err := src.Latest(context.Background(), true)
	if err != nil {
		t.Fatalf("Latest(pre=true): %v", err)
	}
	if rel.Version != "0.8.0-rc.1" {
		t.Errorf("Version = %q, want 0.8.0-rc.1", rel.Version)
	}
	if !rel.Prerelease {
		t.Error("Prerelease = false, want true")
	}
}

func TestLatestETagCache(t *testing.T) {
	s := fakerelease.New(t)
	s.AddRelease("0.7.0", false, nil)
	src := newTestSource(t, s)

	rt := &recordingTransport{}
	src.HTTP = &http.Client{Timeout: 3 * time.Second, Transport: rt}

	ctx := context.Background()
	first, err := src.Latest(ctx, false)
	if err != nil {
		t.Fatalf("Latest #1: %v", err)
	}

	second, err := src.Latest(ctx, false)
	if err != nil {
		t.Fatalf("Latest #2: %v", err)
	}

	if !reflect.DeepEqual(first, second) {
		t.Errorf("Latest #2 = %+v, want identical to #1 %+v", second, first)
	}

	if len(rt.statuses) != 2 {
		t.Fatalf("recorded %d responses, want 2: %v", len(rt.statuses), rt.statuses)
	}
	if rt.statuses[0] != http.StatusOK {
		t.Errorf("first response status = %d, want 200", rt.statuses[0])
	}
	if rt.statuses[1] != http.StatusNotModified {
		t.Errorf("second response status = %d, want 304", rt.statuses[1])
	}
	if len(rt.headers) != 2 || rt.headers[1].Get("If-None-Match") == "" {
		t.Errorf("second request did not send If-None-Match: %+v", rt.headers)
	}
}

func TestLatestFallbackOn403(t *testing.T) {
	s := fakerelease.New(t)
	s.AddRelease("0.7.0", false, nil)
	s.SetStatus("/releases/latest", http.StatusForbidden)
	src := newTestSource(t, s)

	rel, err := src.Latest(context.Background(), false)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rel.Version != "0.7.0" {
		t.Errorf("Version = %q, want 0.7.0 (from fallback)", rel.Version)
	}

	if !requestedFallback(s) {
		t.Errorf("Requests() = %v, want a request to /latest.json", s.Requests())
	}
}

func TestLatestFallbackOn5xx(t *testing.T) {
	s := fakerelease.New(t)
	s.AddRelease("0.7.0", false, nil)
	s.SetStatus("/releases/latest", http.StatusServiceUnavailable)
	src := newTestSource(t, s)

	rel, err := src.Latest(context.Background(), false)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rel.Version != "0.7.0" {
		t.Errorf("Version = %q, want 0.7.0 (from fallback)", rel.Version)
	}

	if !requestedFallback(s) {
		t.Errorf("Requests() = %v, want a request to /latest.json", s.Requests())
	}
}

func requestedFallback(s *fakerelease.Server) bool {
	for _, req := range s.Requests() {
		if strings.Contains(req, "/latest.json") {
			return true
		}
	}
	return false
}

func TestLatestOfflineWithin3s(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("Close listener: %v", err)
	}
	// addr now refuses every connection.

	src := &update.Source{
		HTTP:        &http.Client{Timeout: 3 * time.Second},
		GitHubAPI:   "http://" + addr,
		FallbackURL: "http://" + addr + "/latest.json",
		CacheDir:    t.TempDir(),
	}

	start := time.Now()
	_, err = src.Latest(context.Background(), false)
	elapsed := time.Since(start)

	if elapsed > 3500*time.Millisecond {
		t.Errorf("Latest took %v, want <= 3.5s", elapsed)
	}
	if !errors.Is(err, update.ErrUnavailable) {
		t.Errorf("err = %v, want it to wrap ErrUnavailable", err)
	}
}

func TestGetVersion(t *testing.T) {
	s := fakerelease.New(t)
	s.AddRelease("0.7.0", false, map[string][]byte{"asset.txt": []byte("hi")})
	src := newTestSource(t, s)

	rel, err := src.Get(context.Background(), "0.7.0")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rel.Version != "0.7.0" {
		t.Errorf("Version = %q, want 0.7.0", rel.Version)
	}
	if len(rel.Assets) != 1 {
		t.Errorf("Assets = %v, want 1 entry", rel.Assets)
	}
}

func TestGetMissing404(t *testing.T) {
	s := fakerelease.New(t)
	src := newTestSource(t, s)

	if _, err := src.Get(context.Background(), "9.9.9"); err == nil {
		t.Fatal("Get: want an error for a missing version, got nil")
	}
}

func TestUserAgentOnly(t *testing.T) {
	s := fakerelease.New(t)
	s.AddRelease("0.7.0", false, nil)
	src := newTestSource(t, s)

	rt := &recordingTransport{}
	src.HTTP = &http.Client{Timeout: 3 * time.Second, Transport: rt}

	if _, err := src.Latest(context.Background(), false); err != nil {
		t.Fatalf("Latest: %v", err)
	}

	if len(rt.headers) == 0 {
		t.Fatal("no requests recorded")
	}
	for _, h := range rt.headers {
		if h.Get("Authorization") != "" {
			t.Errorf("request carried an Authorization header: %v", h)
		}
		if ua := h.Get("User-Agent"); !strings.HasPrefix(ua, "ajent/") {
			t.Errorf("User-Agent = %q, want a value starting with ajent/", ua)
		}
		if got := h.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept = %q, want application/vnd.github+json", got)
		}
	}
}

func TestCheckAllowed(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"nothing set", map[string]string{}, true},
		{"no update check", map[string]string{"AJENT_NO_UPDATE_CHECK": "1"}, false},
		{"ci", map[string]string{"CI": "true"}, false},
		{"both set", map[string]string{"AJENT_NO_UPDATE_CHECK": "1", "CI": "true"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(key string) string { return tt.env[key] }
			if got := update.CheckAllowed(getenv); got != tt.want {
				t.Errorf("CheckAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultSourceEnvOverride(t *testing.T) {
	t.Setenv("AJENT_UPDATE_API", "https://example.test/api")
	t.Setenv("AJENT_UPDATE_FALLBACK_URL", "https://example.test/latest.json")

	src := update.DefaultSource(t.TempDir())
	if src.GitHubAPI != "https://example.test/api" {
		t.Errorf("GitHubAPI = %q, want override", src.GitHubAPI)
	}
	if src.FallbackURL != "https://example.test/latest.json" {
		t.Errorf("FallbackURL = %q, want override", src.FallbackURL)
	}
	if src.HTTP == nil || src.HTTP.Timeout != 3*time.Second {
		t.Errorf("HTTP.Timeout = %v, want 3s", src.HTTP.Timeout)
	}
}

func TestDefaultSourceDefaults(t *testing.T) {
	t.Setenv("AJENT_UPDATE_API", "")
	t.Setenv("AJENT_UPDATE_FALLBACK_URL", "")

	src := update.DefaultSource(t.TempDir())
	if src.GitHubAPI != update.DefaultGitHubAPI {
		t.Errorf("GitHubAPI = %q, want %q", src.GitHubAPI, update.DefaultGitHubAPI)
	}
	if src.FallbackURL != update.DefaultFallbackURL {
		t.Errorf("FallbackURL = %q, want %q", src.FallbackURL, update.DefaultFallbackURL)
	}
}
