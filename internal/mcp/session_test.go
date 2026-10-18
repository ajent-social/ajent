// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/ajent-social/ajent-client/internal/credentials"
)

func TestSessionCredentialNoResolver(t *testing.T) {
	s := &Session{Profile: "terminal"}
	_, err := s.Credential(context.Background())
	if err == nil || err.Error() != "mcp: session has no credential resolver" {
		t.Fatalf("Credential() error = %v, want the no-resolver error", err)
	}
}

func TestSessionCredentialResolveError(t *testing.T) {
	wantErr := errors.New("session_test: resolve failed")
	s := &Session{
		Profile: "terminal",
		Resolve: func(context.Context, string) (credentials.Credential, error) {
			return credentials.Credential{}, wantErr
		},
	}
	_, err := s.Credential(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("Credential() error = %v, want %v", err, wantErr)
	}
}

func TestSessionCredentialSetsScope(t *testing.T) {
	var gotProfile string
	s := &Session{
		Profile: "myprofile",
		Resolve: func(_ context.Context, profile string) (credentials.Credential, error) {
			gotProfile = profile
			return credentials.Credential{Server: "https://example.test", AgentID: "agent-1", GroupID: "group-1"}, nil
		},
	}

	c, err := s.Credential(context.Background())
	if err != nil {
		t.Fatalf("Credential() error = %v", err)
	}
	if gotProfile != "myprofile" {
		t.Errorf("Resolve called with profile %q, want %q", gotProfile, "myprofile")
	}
	if c.Server != "https://example.test" {
		t.Errorf("returned credential Server = %q", c.Server)
	}

	want := []string{"https://example.test", "agent-1", "group-1"}
	if len(s.Scope) != len(want) {
		t.Fatalf("Scope = %v, want %v", s.Scope, want)
	}
	for i := range want {
		if s.Scope[i] != want[i] {
			t.Errorf("Scope[%d] = %q, want %q", i, s.Scope[i], want[i])
		}
	}
}
