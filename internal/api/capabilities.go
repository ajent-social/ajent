// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"sync"
	"time"

	"github.com/ajent-social/ajent/internal/credentials"
)

// UnsupportedCapabilityError is returned by Require when the server does
// not advertise capability (docs/contract.md section 9).
type UnsupportedCapabilityError struct{ Capability string }

// Error implements the error interface.
func (e *UnsupportedCapabilityError) Error() string {
	return "ajent: unsupported_capability. Server capability " + e.Capability + " is unavailable. Upgrade before retrying this operation."
}

// capCacheKey identifies one Client, server and credential identity for
// the capabilities cache below. It never holds a token.
type capCacheKey struct {
	client   *Client
	server   string
	identity string
}

// capCache caches the capabilities list per Client, server and credential
// identity. It is a package-level sync.Map, keyed by capCacheKey, so the
// Client struct (owned by T2.4, internal/api/client.go) stays untouched.
var capCache sync.Map // capCacheKey -> []string

// Capabilities returns the "capabilities" list from GET /v1/reliability,
// cached per Client and credential. A failed lookup is never cached.
func (c *Client) Capabilities(ctx context.Context, cred credentials.Credential) ([]string, error) {
	key := capCacheKey{client: c, server: cred.Server, identity: cred.Identity()}
	if cached, ok := capCache.Load(key); ok {
		return cached.([]string), nil
	}

	var body struct {
		Capabilities []string `json:"capabilities"`
	}
	req := Request{
		Method:    "GET",
		Route:     "/v1/reliability",
		Timeout:   3 * time.Second,
		Attempts:  1,
		NoObserve: true,
	}
	if err := c.Do(ctx, cred, req, &body); err != nil {
		return nil, err
	}

	capCache.Store(key, body.Capabilities)
	return body.Capabilities, nil
}

// Require returns an *UnsupportedCapabilityError when capability is not
// among the server's advertised capabilities. It returns the underlying
// error, unchanged, when the capability lookup itself fails (docs/contract.md
// section 9, "Intentional break").
func (c *Client) Require(ctx context.Context, cred credentials.Credential, capability string) error {
	caps, err := c.Capabilities(ctx, cred)
	if err != nil {
		return err
	}
	for _, got := range caps {
		if got == capability {
			return nil
		}
	}
	return &UnsupportedCapabilityError{Capability: capability}
}
