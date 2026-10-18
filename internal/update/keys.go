// SPDX-License-Identifier: Apache-2.0

package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
)

// releaseKeys lists trusted Ed25519 release public keys (standard base64 of 32 raw bytes).
// Rotation: add the new key here and ship a release, then remove the old key in a later release.
var releaseKeys = []string{
	"8TzTBuE6mVB0IT8wuXP47yUs/gei06gDNY4/PeIXEYc=",
}

// extraKeyB64 is set only with -ldflags "-X github.com/ajent-social/ajent/internal/update.extraKeyB64=..."
// for tests and release rehearsals. Official release builds never set it.
var extraKeyB64 string

// PublicKeys are the trusted Ed25519 release keys.
var PublicKeys = loadKeys()

// loadKeys decodes releaseKeys into ed25519.PublicKey values, then appends
// the key decoded from extraKeyB64 when that is set and valid. Each
// releaseKeys entry must be standard base64 of exactly 32 bytes; a
// malformed entry is a build-time programming error, and loadKeys panics
// rather than starting with a silently incomplete trust list (a unit test
// covers this). extraKeyB64 is best-effort: when it is empty, not valid
// base64, or not 32 bytes, it is ignored rather than treated as fatal,
// since it comes from an optional link-time flag used only by tests and
// rehearsals, never by official release builds.
func loadKeys() []ed25519.PublicKey {
	keys := make([]ed25519.PublicKey, 0, len(releaseKeys)+1)
	for _, entry := range releaseKeys {
		raw, err := base64.StdEncoding.DecodeString(entry)
		if err != nil {
			panic(fmt.Sprintf("update: releaseKeys entry %q is not valid standard base64: %v", entry, err))
		}
		if len(raw) != ed25519.PublicKeySize {
			panic(fmt.Sprintf("update: releaseKeys entry decodes to %d bytes, want %d", len(raw), ed25519.PublicKeySize))
		}
		keys = append(keys, ed25519.PublicKey(raw))
	}

	if extraKeyB64 != "" {
		if raw, err := base64.StdEncoding.DecodeString(extraKeyB64); err == nil && len(raw) == ed25519.PublicKeySize {
			keys = append(keys, ed25519.PublicKey(raw))
		}
	}

	return keys
}
