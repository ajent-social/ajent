// SPDX-License-Identifier: Apache-2.0

// Package credentials defines the Credential type and the token and
// profile-name helpers shared by the CLI, the API client and the
// file-backed credential store that T2.3 adds in other files of this
// package.
package credentials

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
)

// ErrNoCredential indicates that no credential has been set up yet.
var ErrNoCredential = errors.New("credentials: not set up; run ajent setup")

// ErrInvalidProfile indicates that a profile name failed ValidProfile.
var ErrInvalidProfile = errors.New("credentials: invalid profile name")

// Credential holds one stored identity: the server it was issued for, its
// bearer token, and any linkage metadata the server has assigned. Unknown
// JSON fields are preserved in Extra so a load/save round trip never drops
// data written by a newer client or server.
type Credential struct {
	Server           string `json:"server"`
	Token            string `json:"token"`
	Handle           string `json:"handle,omitempty"`
	AgentID          string `json:"agent_id,omitempty"`
	GroupID          string `json:"group_id,omitempty"`
	CredentialID     string `json:"credential_id,omitempty"`
	Pending          bool   `json:"pending,omitempty"`
	HumanLinked      bool   `json:"human_linked,omitempty"`
	HumanLinkPending bool   `json:"human_link_pending,omitempty"`
	DomainNotice     string `json:"domain_notice,omitempty"`

	// Extra holds unknown fields so a load/save round trip preserves them.
	Extra map[string]json.RawMessage `json:"-"`
}

// credentialKeys lists the known JSON keys in the order MarshalJSON writes
// them, and that UnmarshalJSON removes before what remains becomes Extra.
var credentialKeys = []string{
	"server",
	"token",
	"handle",
	"agent_id",
	"group_id",
	"credential_id",
	"pending",
	"human_linked",
	"human_link_pending",
	"domain_notice",
}

// UnmarshalJSON fills the known fields and preserves every other key in
// Extra.
func (c *Credential) UnmarshalJSON(b []byte) error {
	// alias has Credential's fields and tags but none of its methods, so
	// decoding into it cannot recurse back into UnmarshalJSON.
	type alias Credential
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	for _, key := range credentialKeys {
		delete(raw, key)
	}

	*c = Credential(a)
	if len(raw) > 0 {
		c.Extra = raw
	} else {
		c.Extra = nil
	}
	return nil
}

// MarshalJSON writes the known fields first, in the order credentialKeys
// lists, omitting empty optional values, followed by the Extra keys in
// sorted order. It never drops an Extra key; a known field name present in
// Extra is ignored because the struct field always wins.
func (c Credential) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true

	write := func(key string, value any) error {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		k, err := json.Marshal(key)
		if err != nil {
			return fmt.Errorf("credentials: marshaling key %s: %w", key, err)
		}
		buf.Write(k)
		buf.WriteByte(':')
		v, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("credentials: marshaling %s: %w", key, err)
		}
		buf.Write(v)
		return nil
	}

	if err := write("server", c.Server); err != nil {
		return nil, err
	}
	if err := write("token", c.Token); err != nil {
		return nil, err
	}
	if c.Handle != "" {
		if err := write("handle", c.Handle); err != nil {
			return nil, err
		}
	}
	if c.AgentID != "" {
		if err := write("agent_id", c.AgentID); err != nil {
			return nil, err
		}
	}
	if c.GroupID != "" {
		if err := write("group_id", c.GroupID); err != nil {
			return nil, err
		}
	}
	if c.CredentialID != "" {
		if err := write("credential_id", c.CredentialID); err != nil {
			return nil, err
		}
	}
	if c.Pending {
		if err := write("pending", c.Pending); err != nil {
			return nil, err
		}
	}
	if c.HumanLinked {
		if err := write("human_linked", c.HumanLinked); err != nil {
			return nil, err
		}
	}
	if c.HumanLinkPending {
		if err := write("human_link_pending", c.HumanLinkPending); err != nil {
			return nil, err
		}
	}
	if c.DomainNotice != "" {
		if err := write("domain_notice", c.DomainNotice); err != nil {
			return nil, err
		}
	}

	extraKeys := make([]string, 0, len(c.Extra))
	for k := range c.Extra {
		extraKeys = append(extraKeys, k)
	}
	sort.Strings(extraKeys)
	for _, k := range extraKeys {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, fmt.Errorf("credentials: marshaling extra key %s: %w", k, err)
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(c.Extra[k])
	}

	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// String renders a redacted summary. The token itself never appears; when
// Token is empty it reports "<none>" rather than "<redacted>" so the two
// cases stay distinguishable in logs.
func (c Credential) String() string {
	token := "<redacted>"
	if c.Token == "" {
		token = "<none>"
	}
	return fmt.Sprintf("credentials.Credential{server=%s handle=%s agent_id=%s token=%s}", c.Server, c.Handle, c.AgentID, token)
}

// Format implements fmt.Formatter so every verb, including %#v (which would
// otherwise bypass String and print the Token field directly), renders the
// redacted summary. The token must never appear in any formatted
// representation of a Credential.
func (c Credential) Format(f fmt.State, _ rune) {
	_, _ = io.WriteString(f, c.String())
}

// Identity returns a short, stable, non-reversible fingerprint of the
// token, suitable for use in file and directory names.
func (c Credential) Identity() string {
	sum := sha256.Sum256([]byte(c.Token))
	return hex.EncodeToString(sum[:])[:16]
}

// NewToken generates a fresh bearer token: "aj_" followed by 16 hex digits
// (an 8-byte random id), an underscore, and the base64url (no padding)
// encoding of 32 random bytes.
func NewToken() (string, error) {
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return "", fmt.Errorf("credentials: generating token id: %w", err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("credentials: generating token secret: %w", err)
	}
	return "aj_" + hex.EncodeToString(id) + "_" + base64.RawURLEncoding.EncodeToString(secret), nil
}

// profileNamePattern matches a valid profile name: it starts with a
// lowercase letter or digit, followed by 1 to 39 more lowercase letters,
// digits or hyphens (2 to 40 characters total).
var profileNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,39}$`)

// ValidProfile reports whether name is a valid profile name.
func ValidProfile(name string) bool {
	return profileNamePattern.MatchString(name)
}
