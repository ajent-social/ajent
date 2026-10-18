// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// signchecksumsTestGetenv returns a getenv func backed by vals, returning
// "" for any name not present, like a real, mostly-empty environment.
func signchecksumsTestGetenv(vals map[string]string) func(string) string {
	return func(name string) string {
		return vals[name]
	}
}

// TestSignRoundTrip keygens into t.TempDir(), signs a file with the
// generated seed, verifies the signature with ed25519.Verify, and checks
// the generated .pem parses with x509.ParsePKIXPublicKey to the same key.
func TestSignRoundTrip(t *testing.T) {
	dir := t.TempDir()
	prefix := filepath.Join(dir, "k")

	var stderr bytes.Buffer
	if code := run([]string{"-keygen", prefix}, signchecksumsTestGetenv(nil), &stderr); code != 0 {
		t.Fatalf("run(-keygen) = %d, want 0; stderr: %s", code, stderr.String())
	}

	seedB64, err := os.ReadFile(prefix + ".seed")
	if err != nil {
		t.Fatalf("reading seed: %v", err)
	}
	pubB64, err := os.ReadFile(prefix + ".pub")
	if err != nil {
		t.Fatalf("reading pub: %v", err)
	}
	pemBytes, err := os.ReadFile(prefix + ".pem")
	if err != nil {
		t.Fatalf("reading pem: %v", err)
	}

	if runtime.GOOS != "windows" {
		if info, err := os.Stat(prefix + ".seed"); err != nil {
			t.Fatalf("stat seed: %v", err)
		} else if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("seed file mode = %o, want 0600", perm)
		}
	}

	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(seedB64)))
	if err != nil {
		t.Fatalf("decoding seed: %v", err)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)

	pubRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(pubB64)))
	if err != nil {
		t.Fatalf("decoding pub: %v", err)
	}
	if !bytes.Equal(pubRaw, pub) {
		t.Fatalf("prefix.pub does not match the seed's derived public key")
	}

	block, _ := pem.Decode(pemBytes)
	if block == nil {
		t.Fatalf("prefix.pem did not decode as PEM")
	}
	if block.Type != "PUBLIC KEY" {
		t.Errorf("PEM block type = %q, want %q", block.Type, "PUBLIC KEY")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatalf("x509.ParsePKIXPublicKey: %v", err)
	}
	parsedPub, ok := parsed.(ed25519.PublicKey)
	if !ok {
		t.Fatalf("parsed key type = %T, want ed25519.PublicKey", parsed)
	}
	if !bytes.Equal(parsedPub, pub) {
		t.Fatalf("PEM public key does not match the seed's derived public key")
	}

	// Now sign a checksums file with the generated seed and verify it.
	checksumsPath := filepath.Join(dir, "checksums.txt")
	checksums := []byte("deadbeef  ajent_1.0.0_linux_amd64.tar.gz\n")
	if err := os.WriteFile(checksumsPath, checksums, 0o600); err != nil {
		t.Fatalf("writing checksums: %v", err)
	}

	sigPath := filepath.Join(dir, "checksums.txt.sig")
	getenv := signchecksumsTestGetenv(map[string]string{"RELEASE_ED25519_KEY": string(bytes.TrimSpace(seedB64))})
	stderr.Reset()
	if code := run([]string{"-in", checksumsPath, "-out", sigPath}, getenv, &stderr); code != 0 {
		t.Fatalf("run(sign) = %d, want 0; stderr: %s", code, stderr.String())
	}

	sigB64, err := os.ReadFile(sigPath)
	if err != nil {
		t.Fatalf("reading signature: %v", err)
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigB64)))
	if err != nil {
		t.Fatalf("decoding signature: %v", err)
	}
	if !ed25519.Verify(pub, checksums, sig) {
		t.Fatalf("ed25519.Verify: signature does not verify")
	}
}

// TestMissingKey proves run exits 1 with a stderr message naming
// RELEASE_ED25519_KEY when the environment variable is unset.
func TestMissingKey(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(in, []byte("deadbeef  ajent_1.0.0_linux_amd64.tar.gz\n"), 0o600); err != nil {
		t.Fatalf("writing checksums: %v", err)
	}
	out := filepath.Join(dir, "checksums.txt.sig")

	var stderr bytes.Buffer
	code := run([]string{"-in", in, "-out", out}, signchecksumsTestGetenv(nil), &stderr)

	if code != 1 {
		t.Fatalf("run() = %d, want 1; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "RELEASE_ED25519_KEY") {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), "RELEASE_ED25519_KEY")
	}
}

// TestBadKeyLength proves a RELEASE_ED25519_KEY that decodes to neither a
// seed nor a private key gives exit 1.
func TestBadKeyLength(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(in, []byte("deadbeef  ajent_1.0.0_linux_amd64.tar.gz\n"), 0o600); err != nil {
		t.Fatalf("writing checksums: %v", err)
	}
	out := filepath.Join(dir, "checksums.txt.sig")

	shortKey := base64.StdEncoding.EncodeToString([]byte("0123456789")) // 10 bytes
	getenv := signchecksumsTestGetenv(map[string]string{"RELEASE_ED25519_KEY": shortKey})

	var stderr bytes.Buffer
	code := run([]string{"-in", in, "-out", out}, getenv, &stderr)

	if code != 1 {
		t.Fatalf("run() = %d, want 1; stderr: %s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), shortKey) {
		t.Errorf("stderr leaked the key material: %q", stderr.String())
	}
}

// TestAccepts64ByteKey proves signing with a RELEASE_ED25519_KEY that
// decodes to a full 64-byte private key (rather than the normal 32-byte
// seed) works and produces a signature ed25519.Verify accepts.
func TestAccepts64ByteKey(t *testing.T) {
	dir := t.TempDir()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}

	in := filepath.Join(dir, "checksums.txt")
	checksums := []byte("deadbeef  ajent_1.0.0_linux_amd64.tar.gz\n")
	if err := os.WriteFile(in, checksums, 0o600); err != nil {
		t.Fatalf("writing checksums: %v", err)
	}
	out := filepath.Join(dir, "checksums.txt.sig")

	getenv := signchecksumsTestGetenv(map[string]string{
		"RELEASE_ED25519_KEY": base64.StdEncoding.EncodeToString(priv), // 64-byte private key
	})

	var stderr bytes.Buffer
	if code := run([]string{"-in", in, "-out", out}, getenv, &stderr); code != 0 {
		t.Fatalf("run() = %d, want 0; stderr: %s", code, stderr.String())
	}

	sigB64, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading signature: %v", err)
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigB64)))
	if err != nil {
		t.Fatalf("decoding signature: %v", err)
	}
	if !ed25519.Verify(pub, checksums, sig) {
		t.Fatalf("ed25519.Verify: signature does not verify")
	}
}
