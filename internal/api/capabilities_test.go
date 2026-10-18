// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/ajent-social/ajent-client/internal/testutil/fakeserver"
)

// serveReliability registers a GET /v1/reliability handler on srv that
// always returns body as a JSON 200 response.
func serveReliability(srv *fakeserver.Server, body string) {
	srv.Handle("GET", "/v1/reliability", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	})
}

func TestRequirePresent(t *testing.T) {
	srv := fakeserver.New(t)
	cred := srv.Credential()
	serveReliability(srv, `{"capabilities":["goals","group_lifecycle"]}`)

	c := &Client{Sleep: noSleep}
	if err := c.Require(context.Background(), cred, "goals"); err != nil {
		t.Fatalf("Require() = %v, want nil", err)
	}
}

func TestRequireAbsent(t *testing.T) {
	srv := fakeserver.New(t)
	cred := srv.Credential()
	serveReliability(srv, `{"capabilities":["group_lifecycle"]}`)

	c := &Client{Sleep: noSleep}
	err := c.Require(context.Background(), cred, "goals")

	var uce *UnsupportedCapabilityError
	if !errors.As(err, &uce) {
		t.Fatalf("Require() error = %v (%T), want *UnsupportedCapabilityError", err, err)
	}
	want := "ajent: unsupported_capability. Server capability goals is unavailable. Upgrade before retrying this operation."
	if got := uce.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestCapabilitiesCached(t *testing.T) {
	srv := fakeserver.New(t)
	cred := srv.Credential()
	serveReliability(srv, `{"capabilities":["goals"]}`)

	c := &Client{Sleep: noSleep}
	if err := c.Require(context.Background(), cred, "goals"); err != nil {
		t.Fatalf("Require() #1 = %v, want nil", err)
	}
	if err := c.Require(context.Background(), cred, "goals"); err != nil {
		t.Fatalf("Require() #2 = %v, want nil", err)
	}

	var reliabilityCalls int
	for _, r := range srv.Requests() {
		if r.Path == "/v1/reliability" {
			reliabilityCalls++
		}
	}
	if reliabilityCalls != 1 {
		t.Errorf("reliability requests = %d, want 1", reliabilityCalls)
	}
}

func TestCapabilitiesErrorNotCached(t *testing.T) {
	srv := fakeserver.New(t)
	cred := srv.Credential()

	var calls int32
	srv.Handle("GET", "/v1/reliability", func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"capabilities":["goals"]}`))
	})

	c := &Client{Sleep: noSleep}

	_, err := c.Capabilities(context.Background(), cred)
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("Capabilities() #1 error = %v (%T), want *Error", err, err)
	}

	caps, err := c.Capabilities(context.Background(), cred)
	if err != nil {
		t.Fatalf("Capabilities() #2 = %v, want nil error", err)
	}
	if len(caps) != 1 || caps[0] != "goals" {
		t.Errorf("Capabilities() #2 = %v, want [goals]", caps)
	}

	if got := len(srv.Requests()); got != 2 {
		t.Errorf("len(Requests()) = %d, want 2", got)
	}
}

func TestGatedRouteNotCalled(t *testing.T) {
	srv := fakeserver.New(t)
	cred := srv.Credential()
	serveReliability(srv, `{"capabilities":[]}`)

	c := &Client{Sleep: noSleep}

	// Simulate a caller that guards a gated route with Require.
	call := func() error {
		if err := c.Require(context.Background(), cred, "goals"); err != nil {
			return err
		}
		return c.Do(context.Background(), cred, Request{Method: "GET", Route: "/v1/goals"}, nil)
	}

	err := call()
	var uce *UnsupportedCapabilityError
	if !errors.As(err, &uce) {
		t.Fatalf("call() error = %v (%T), want *UnsupportedCapabilityError", err, err)
	}

	for _, r := range srv.Requests() {
		if r.Path == "/v1/goals" {
			t.Fatalf("unexpected request to /v1/goals")
		}
	}
}
