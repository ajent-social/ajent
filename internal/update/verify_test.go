// SPDX-License-Identifier: Apache-2.0

package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"testing"
)

// verifyTestDigest returns data's SHA-256 digest as lowercase hex, the same
// form GoReleaser writes in checksums.txt.
func verifyTestDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// verifyTestChecksumsFor builds a one-entry checksums.txt body for data
// under a fixed archive name, in the "<digest>  <name>" form GoReleaser
// writes (two spaces), and returns it alongside that name.
func verifyTestChecksumsFor(data []byte) (checksums []byte, name string) {
	name = "ajent_1.0.0_linux_amd64.tar.gz"
	return []byte(verifyTestDigest(data) + "  " + name + "\n"), name
}

// verifyTestSign generates a fresh Ed25519 key pair, signs checksums, and
// returns the public key alongside the signature file content
// VerifyChecksums expects (standard base64 plus a trailing newline).
func verifyTestSign(t *testing.T, checksums []byte) (ed25519.PublicKey, []byte) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}

	sig := ed25519.Sign(priv, checksums)
	return pub, []byte(base64.StdEncoding.EncodeToString(sig) + "\n")
}

func TestVerifyChecksumsValid(t *testing.T) {
	checksums := []byte("deadbeef  ajent_1.0.0_linux_amd64.tar.gz\n")
	pub, sig := verifyTestSign(t, checksums)

	if err := VerifyChecksums(checksums, sig, []ed25519.PublicKey{pub}); err != nil {
		t.Fatalf("VerifyChecksums() = %v, want nil", err)
	}
}

func TestVerifyChecksumsTamperedMessage(t *testing.T) {
	checksums := []byte("deadbeef  ajent_1.0.0_linux_amd64.tar.gz\n")
	pub, sig := verifyTestSign(t, checksums)

	tampered := append([]byte(nil), checksums...)
	tampered[0] ^= 0xFF

	if err := VerifyChecksums(tampered, sig, []ed25519.PublicKey{pub}); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("VerifyChecksums(tampered message) = %v, want %v", err, ErrBadSignature)
	}
}

func TestVerifyChecksumsTamperedSignature(t *testing.T) {
	checksums := []byte("deadbeef  ajent_1.0.0_linux_amd64.tar.gz\n")
	pub, sig := verifyTestSign(t, checksums)

	decoded, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(sig)))
	if err != nil {
		t.Fatalf("decoding signature: %v", err)
	}
	decoded[0] ^= 0xFF
	tamperedSig := []byte(base64.StdEncoding.EncodeToString(decoded) + "\n")

	if err := VerifyChecksums(checksums, tamperedSig, []ed25519.PublicKey{pub}); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("VerifyChecksums(tampered signature) = %v, want %v", err, ErrBadSignature)
	}
}

func TestVerifyChecksumsSecondKeyOfRotation(t *testing.T) {
	checksums := []byte("deadbeef  ajent_1.0.0_linux_amd64.tar.gz\n")

	firstPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	secondPub, sig := verifyTestSign(t, checksums)

	if err := VerifyChecksums(checksums, sig, []ed25519.PublicKey{firstPub, secondPub}); err != nil {
		t.Fatalf("VerifyChecksums() = %v, want nil", err)
	}
}

func TestVerifyChecksumsNoKeys(t *testing.T) {
	checksums := []byte("deadbeef  ajent_1.0.0_linux_amd64.tar.gz\n")
	_, sig := verifyTestSign(t, checksums)

	if err := VerifyChecksums(checksums, sig, nil); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("VerifyChecksums(no keys) = %v, want %v", err, ErrBadSignature)
	}
}

func TestVerifyChecksumsTrailingWhitespace(t *testing.T) {
	checksums := []byte("deadbeef  ajent_1.0.0_linux_amd64.tar.gz\n")
	pub, sig := verifyTestSign(t, checksums)

	padded := append([]byte(" \t"), sig...)
	padded = append(padded, []byte("\n\n  ")...)

	if err := VerifyChecksums(checksums, padded, []ed25519.PublicKey{pub}); err != nil {
		t.Fatalf("VerifyChecksums(padded signature) = %v, want nil", err)
	}
}

func TestVerifyFileMatch(t *testing.T) {
	data := []byte("archive contents")
	checksums, name := verifyTestChecksumsFor(data)

	if err := VerifyFile(checksums, name, data); err != nil {
		t.Fatalf("VerifyFile() = %v, want nil", err)
	}
}

func TestVerifyFileMismatch(t *testing.T) {
	data := []byte("archive contents")
	checksums, name := verifyTestChecksumsFor(data)

	if err := VerifyFile(checksums, name, []byte("different contents")); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("VerifyFile(wrong data) = %v, want %v", err, ErrChecksumMismatch)
	}
}

func TestVerifyFileNotListed(t *testing.T) {
	data := []byte("archive contents")
	checksums, _ := verifyTestChecksumsFor(data)

	if err := VerifyFile(checksums, "not_listed.tar.gz", data); !errors.Is(err, ErrNotListed) {
		t.Fatalf("VerifyFile(unlisted name) = %v, want %v", err, ErrNotListed)
	}
}

func TestVerifyFileSimilarNames(t *testing.T) {
	data := []byte("archive contents")
	sbomData := []byte("sbom contents")

	sumArchive := verifyTestDigest(data)
	sumSBOM := verifyTestDigest(sbomData)

	checksums := []byte(
		sumArchive + "  ajent_1.0.0_linux_amd64.tar.gz\n" +
			sumSBOM + "  ajent_1.0.0_linux_amd64.tar.gz.sbom.json\n",
	)

	if err := VerifyFile(checksums, "ajent_1.0.0_linux_amd64.tar.gz", data); err != nil {
		t.Fatalf("VerifyFile(archive) = %v, want nil", err)
	}
	if err := VerifyFile(checksums, "ajent_1.0.0_linux_amd64.tar.gz.sbom.json", sbomData); err != nil {
		t.Fatalf("VerifyFile(sbom) = %v, want nil", err)
	}
	// The archive's digest must never be accepted for the longer sbom name.
	if err := VerifyFile(checksums, "ajent_1.0.0_linux_amd64.tar.gz.sbom.json", data); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("VerifyFile(sbom name, archive data) = %v, want %v", err, ErrChecksumMismatch)
	}
}

func TestLoadKeysEmbedded(t *testing.T) {
	for _, entry := range releaseKeys {
		raw, err := base64.StdEncoding.DecodeString(entry)
		if err != nil {
			t.Fatalf("releaseKeys entry %q: not valid base64: %v", entry, err)
		}
		if len(raw) != ed25519.PublicKeySize {
			t.Fatalf("releaseKeys entry %q: decodes to %d bytes, want %d", entry, len(raw), ed25519.PublicKeySize)
		}
	}
}

// TestLoadKeysPanicsOnBadEntry proves loadKeys refuses to start with a
// malformed releaseKeys entry rather than silently shipping an incomplete
// trust list.
func TestLoadKeysPanicsOnBadEntry(t *testing.T) {
	saved := releaseKeys
	t.Cleanup(func() { releaseKeys = saved })

	releaseKeys = []string{base64.StdEncoding.EncodeToString([]byte("too-short"))}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("loadKeys() did not panic on a malformed releaseKeys entry")
		}
	}()
	loadKeys()
}
