// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"errors"

	"github.com/ajent-social/ajent/internal/api"
	"github.com/ajent-social/ajent/internal/credentials"
)

// Session holds the state Serve needs to answer one MCP connection.
type Session struct {
	Profile string
	Dir     string
	Root    credentials.Credential
	API     *api.Client
	// Resolve returns the lazily enrolled profile credential. cmd/ajent sets
	// it to cli.Env.Resolve (wired by T3.6), so internal/mcp never imports
	// internal/profiles.
	Resolve     func(ctx context.Context, profile string) (credentials.Credential, error)
	Cache       *Cache
	ProjectRoot string
	// Scope is the delivery-cache scope; Credential() updates it to
	// [server, agent_id, group_id].
	Scope []string
}

// errNoResolver is returned by Credential when Resolve is nil.
var errNoResolver = errors.New("mcp: session has no credential resolver")

// Credential returns the profile credential, enrolling it lazily through
// Resolve, and updates s.Scope on success.
func (s *Session) Credential(ctx context.Context) (credentials.Credential, error) {
	if s.Resolve == nil {
		return credentials.Credential{}, errNoResolver
	}
	c, err := s.Resolve(ctx, s.Profile)
	if err != nil {
		return credentials.Credential{}, err
	}
	s.Scope = []string{c.Server, c.AgentID, c.GroupID}
	return c, nil
}
