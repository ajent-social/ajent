// SPDX-License-Identifier: Apache-2.0

package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

// ErrBadSignature is returned by VerifyChecksums when sig does not decode
// to a 64-byte Ed25519 signature that verifies against checksums under any
// of the given keys, including when the key list is empty.
var ErrBadSignature = errors.New("update: release signature does not verify")

// ErrChecksumMismatch is returned by VerifyFile when name is listed in
// checksums but data's SHA-256 digest does not match the listed value.
var ErrChecksumMismatch = errors.New("update: checksum does not match")

// ErrNotListed is returned by VerifyFile when name has no entry in
// checksums.
var ErrNotListed = errors.New("update: file not listed in checksums")

// VerifyChecksums reports whether sig is a valid Ed25519 signature over the
// exact bytes of checksums, produced by any of keys. sig is the standard
// base64 encoding (with padding) of the 64-byte signature, optionally
// followed by a trailing newline; surrounding ASCII whitespace is trimmed
// before decoding. It returns ErrBadSignature when sig is not valid base64,
// does not decode to exactly ed25519.SignatureSize bytes, or verifies
// against none of keys -- including when keys is empty, so a build with no
// embedded trusted key fails closed rather than accepting anything.
func VerifyChecksums(checksums, sig []byte, keys []ed25519.PublicKey) error {
	trimmed := bytes.TrimSpace(sig)

	decoded, err := base64.StdEncoding.DecodeString(string(trimmed))
	if err != nil || len(decoded) != ed25519.SignatureSize {
		return ErrBadSignature
	}

	for _, key := range keys {
		if ed25519.Verify(key, checksums, decoded) {
			return nil
		}
	}
	return ErrBadSignature
}

// VerifyFile reports whether data's SHA-256 digest matches the entry for
// name in checksums, a GoReleaser-style checksums.txt: one entry per
// non-empty line of the form "<64 lowercase hex digest><sep><name>", where
// sep is two spaces (as GoReleaser writes them), a single space, or a
// single space followed by a "*" binary marker. The digest comparison is
// constant-time. VerifyFile returns ErrNotListed when checksums has no
// entry whose name exactly matches, and ErrChecksumMismatch when an entry
// is found but the digest differs.
func VerifyFile(checksums []byte, name string, data []byte) error {
	digest, ok := verifyFileLookup(checksums, name)
	if !ok {
		return ErrNotListed
	}

	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])

	if subtle.ConstantTimeCompare([]byte(strings.ToLower(digest)), []byte(got)) != 1 {
		return ErrChecksumMismatch
	}
	return nil
}

// verifyFileLookup scans checksums line by line for an entry whose name
// exactly equals name, and returns its hex digest.
func verifyFileLookup(checksums []byte, name string) (digest string, ok bool) {
	for _, line := range strings.Split(string(checksums), "\n") {
		line = strings.TrimRight(line, "\r")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.SplitN(line, " ", 2)
		if len(fields) != 2 {
			continue
		}

		entryName := strings.TrimLeft(fields[1], " *")
		if entryName == name {
			return fields[0], true
		}
	}
	return "", false
}
