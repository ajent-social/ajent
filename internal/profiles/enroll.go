// SPDX-License-Identifier: Apache-2.0

package profiles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ajent-social/ajent-client/internal/api"
	"github.com/ajent-social/ajent-client/internal/config"
	"github.com/ajent-social/ajent-client/internal/credentials"
)

// profilesSyncInterval is how long a synced profile stays valid, in this
// process, before Resolve re-syncs it even when the settings fingerprint
// has not changed (docs/contract.md "Enrollment").
const profilesSyncInterval = 300 * time.Second

// profilesLockTimeout bounds how long Resolve waits for a contended
// profile lock.
const profilesLockTimeout = 30 * time.Second

// profilesDomainNotice is stored on the profile credential when a 403
// response with grants falls back to private-only collaboration
// (docs/contract.md "Enrollment").
const profilesDomainNotice = "Domain access is unavailable; private collaboration remains connected. Run ajent configure to review scope access."

// Existing returns the credential of an already-enrolled, non-pending
// profile, or root when the profile has never been enrolled or is still
// pending. It never writes to disk.
func Existing(dir string, root credentials.Credential, profile string) (credentials.Credential, error) {
	if !credentials.ValidProfile(profile) {
		return credentials.Credential{}, credentials.ErrInvalidProfile
	}
	c, ok, err := credentials.LoadProfile(dir, root, profile)
	if err != nil {
		return credentials.Credential{}, err
	}
	if !ok || c.Pending {
		return root, nil
	}
	return c, nil
}

// profilesSyncMemo remembers the last successful /v1/fleet sync this
// process performed for one profile.
type profilesSyncMemo struct {
	fingerprint string
	at          time.Time
}

// Enroller resolves the credential for a tool profile, enrolling it lazily
// with the installation's root credential and keeping its scope settings
// in sync (docs/design.md; docs/contract.md "Enrollment").
type Enroller struct {
	Dir string
	API *api.Client
	Now func() time.Time

	mu   sync.Mutex
	memo map[string]profilesSyncMemo
}

// Resolve returns the credential for profile, enrolling it lazily under
// the profile lock, re-syncing with POST /v1/fleet when the profile is
// still pending, when this process's memo of the settings fingerprint for
// profile differs from the current one, or when profilesSyncInterval has
// elapsed since that memo was recorded, and falling back to private-only
// collaboration on a 403 response when the sent settings carried grants
// (docs/contract.md "Enrollment").
func (e *Enroller) Resolve(ctx context.Context, root credentials.Credential, profile string, projectRoot string) (credentials.Credential, error) {
	if !credentials.ValidProfile(profile) {
		return credentials.Credential{}, credentials.ErrInvalidProfile
	}
	if err := config.EnsurePrivateDir(credentials.ProfileDir(e.Dir, root)); err != nil {
		return credentials.Credential{}, err
	}

	lock, err := config.Acquire(credentials.ProfileLockPath(e.Dir, root, profile), profilesLockTimeout)
	if err != nil {
		return credentials.Credential{}, err
	}
	defer func() { _ = lock.Release() }()

	c, ok, err := credentials.LoadProfile(e.Dir, root, profile)
	if err != nil {
		return credentials.Credential{}, err
	}
	if !ok {
		c, err = e.createProfile(root, profile)
		if err != nil {
			return credentials.Credential{}, err
		}
	}

	settings, configured, err := ScopeSettings(e.Dir, projectRoot, root)
	if err != nil {
		return credentials.Credential{}, err
	}

	fingerprint, err := profilesFingerprint(settings)
	if err != nil {
		return credentials.Credential{}, err
	}

	now := e.now()
	if !e.needsSync(profile, c.Pending, profilesSyncedFingerprint(c) != fingerprint, now) {
		e.startMemo(profile, fingerprint, now)
		return c, nil
	}

	raw, notice, err := e.sync(ctx, root, profile, c.Token, settings, configured)
	if err != nil {
		return credentials.Credential{}, err
	}

	if err := mergeProfilesResult(&c, raw); err != nil {
		return credentials.Credential{}, err
	}
	c.Pending = false
	c.DomainNotice = notice
	if err := profilesSetSyncedFingerprint(&c, fingerprint); err != nil {
		return credentials.Credential{}, err
	}

	if err := credentials.SaveProfile(e.Dir, root, profile, c); err != nil {
		return credentials.Credential{}, err
	}

	e.recordSync(profile, fingerprint, now)
	return c, nil
}

// createProfile creates profile's credential file for root exclusively
// (config.CreateExclusive, never overwriting an existing file) as
// {"server", "token", "pending": true} and returns it.
func (e *Enroller) createProfile(root credentials.Credential, profile string) (credentials.Credential, error) {
	token, err := credentials.NewToken()
	if err != nil {
		return credentials.Credential{}, fmt.Errorf("profiles: generating token for profile %s: %w", profile, err)
	}
	c := credentials.Credential{Server: root.Server, Token: token, Pending: true}

	data, err := json.Marshal(c)
	if err != nil {
		return credentials.Credential{}, fmt.Errorf("profiles: marshaling new profile %s: %w", profile, err)
	}
	if err := config.CreateExclusive(credentials.ProfilePath(e.Dir, root, profile), data); err != nil {
		return credentials.Credential{}, fmt.Errorf("profiles: creating profile %s: %w", profile, err)
	}
	return c, nil
}

// now returns e.Now(), defaulting to time.Now when e.Now is nil.
func (e *Enroller) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// needsSync reports whether profile must be (re-)synced with /v1/fleet: when
// it is still pending, when its settings changed since the last sync this
// installation recorded (the fingerprint persisted in the profile file, so
// a change made while no process was running is still sent), or when this
// process last synced it more than profilesSyncInterval ago. Because the
// fingerprint is persisted under the profile lock, concurrent processes
// enrolling the same new profile still send exactly one POST /v1/fleet.
func (e *Enroller) needsSync(profile string, pending, changed bool, now time.Time) bool {
	if pending || changed {
		return true
	}
	e.mu.Lock()
	memo, ok := e.memo[profile]
	e.mu.Unlock()
	return ok && now.Sub(memo.at) > profilesSyncInterval
}

// startMemo starts this process's sync interval clock for profile when it
// has none yet, so a long-lived process re-syncs every profilesSyncInterval.
func (e *Enroller) startMemo(profile, fingerprint string, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.memo[profile]; ok {
		return
	}
	if e.memo == nil {
		e.memo = make(map[string]profilesSyncMemo)
	}
	e.memo[profile] = profilesSyncMemo{fingerprint: fingerprint, at: now}
}

// profilesFingerprintKey is the profile-file field that records the settings
// fingerprint of the last successful sync. Older clients ignore it and
// preserve it as an unknown field.
const profilesFingerprintKey = "settings_fingerprint"

// profilesSyncedFingerprint returns the persisted fingerprint of c's last
// successful sync, or "" when none is recorded.
func profilesSyncedFingerprint(c credentials.Credential) string {
	var fp string
	if raw, ok := c.Extra[profilesFingerprintKey]; ok {
		_ = json.Unmarshal(raw, &fp)
	}
	return fp
}

// profilesSetSyncedFingerprint records fingerprint on c as its last synced
// settings fingerprint.
func profilesSetSyncedFingerprint(c *credentials.Credential, fingerprint string) error {
	raw, err := json.Marshal(fingerprint)
	if err != nil {
		return fmt.Errorf("profiles: encoding settings fingerprint: %w", err)
	}
	if c.Extra == nil {
		c.Extra = make(map[string]json.RawMessage, 1)
	}
	c.Extra[profilesFingerprintKey] = raw
	return nil
}

// recordSync remembers a successful sync for profile at fingerprint/at.
func (e *Enroller) recordSync(profile, fingerprint string, at time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.memo == nil {
		e.memo = make(map[string]profilesSyncMemo)
	}
	e.memo[profile] = profilesSyncMemo{fingerprint: fingerprint, at: at}
}

// sync POSTs /v1/fleet with root and the profile's token, including the
// settings fields only when configured is true. On a 403 response when
// settings carried grants, it retries once with an explicit private-only
// body and returns profilesDomainNotice; a successful non-fallback sync
// returns an empty notice, clearing any previous one. Any other error is
// returned unchanged.
func (e *Enroller) sync(ctx context.Context, root credentials.Credential, profile, token string, settings Settings, configured bool) (json.RawMessage, string, error) {
	body := map[string]any{"profile": profile, "token": token}
	if configured {
		body["scope"] = settings.Scope
		body["grants"] = settings.Grants
		body["domain"] = settings.Domain
		body["private_only"] = settings.PrivateOnly
	}

	var raw json.RawMessage
	err := e.API.Do(ctx, root, api.Request{Method: "POST", Route: "/v1/fleet", Body: body}, &raw)
	if err == nil {
		return raw, "", nil
	}

	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.Status != 403 || len(settings.Grants) == 0 {
		return nil, "", err
	}

	fallback := map[string]any{
		"profile":      profile,
		"token":        token,
		"scope":        settings.Scope,
		"grants":       []string{},
		"domain":       "",
		"private_only": true,
	}
	var fallbackRaw json.RawMessage
	if err := e.API.Do(ctx, root, api.Request{Method: "POST", Route: "/v1/fleet", Body: fallback}, &fallbackRaw); err != nil {
		return nil, "", err
	}
	return fallbackRaw, profilesDomainNotice, nil
}

// mergeProfilesResult merges the known fields (handle, agent_id, group_id,
// credential_id) and any other fields (via Extra) from a /v1/fleet
// response into c.
func mergeProfilesResult(c *credentials.Credential, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var resp credentials.Credential
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("profiles: decoding fleet response: %w", err)
	}

	if resp.Handle != "" {
		c.Handle = resp.Handle
	}
	if resp.AgentID != "" {
		c.AgentID = resp.AgentID
	}
	if resp.GroupID != "" {
		c.GroupID = resp.GroupID
	}
	if resp.CredentialID != "" {
		c.CredentialID = resp.CredentialID
	}
	for k, v := range resp.Extra {
		if c.Extra == nil {
			c.Extra = make(map[string]json.RawMessage, len(resp.Extra))
		}
		c.Extra[k] = v
	}
	return nil
}

// profilesFingerprint returns a deterministic JSON encoding of s, used to
// detect a settings change between two Resolve calls in this process
// (docs/contract.md "Enrollment": "the settings fingerprint").
func profilesFingerprint(s Settings) (string, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("profiles: marshaling settings fingerprint: %w", err)
	}
	return string(data), nil
}
