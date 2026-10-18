// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ajent-social/ajent/internal/cli"
	"github.com/ajent-social/ajent/internal/update"
)

// verifyReleaseTestDigest returns data's SHA-256 digest as lowercase hex,
// the same form GoReleaser writes in checksums.txt.
func verifyReleaseTestDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// verifyReleaseTestKey generates a fresh Ed25519 key pair, appends its
// public key to update.PublicKeys for the duration of the test (restored
// by t.Cleanup, docs/tasks/T5.3.md step 8), and returns the private key for
// signing test fixtures.
func verifyReleaseTestKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}

	original := update.PublicKeys
	update.PublicKeys = append(append([]ed25519.PublicKey{}, original...), pub)
	t.Cleanup(func() { update.PublicKeys = original })

	return priv
}

// verifyReleaseTestEnv builds a minimal *cli.Env with buffered Stdout and
// Stderr for verify-release tests, which need neither credentials nor the
// API client.
func verifyReleaseTestEnv() (env *cli.Env, stdout, stderr *bytes.Buffer) {
	stdout = &bytes.Buffer{}
	stderr = &bytes.Buffer{}
	env = &cli.Env{Stdout: stdout, Stderr: stderr}
	return env, stdout, stderr
}

// verifyReleaseTestWrite writes data to name under dir and returns the
// full path.
func verifyReleaseTestWrite(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

func TestVerifyReleaseValid(t *testing.T) {
	priv := verifyReleaseTestKey(t)
	dir := t.TempDir()

	archiveData := []byte("archive contents")
	sum := verifyReleaseTestDigest(archiveData)
	checksums := []byte(sum + "  ajent_1.0.0_linux_amd64.tar.gz\n")

	checksumsPath := verifyReleaseTestWrite(t, dir, "checksums.txt", checksums)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, checksums)) + "\n"
	sigPath := verifyReleaseTestWrite(t, dir, "checksums.txt.sig", []byte(sig))
	archivePath := verifyReleaseTestWrite(t, dir, "ajent_1.0.0_linux_amd64.tar.gz", archiveData)

	env, stdout, stderr := verifyReleaseTestEnv()
	code := cli.Main(context.Background(), env, []string{
		"verify-release", "--checksums", checksumsPath, "--sig", sigPath, "--file", archivePath,
	})

	if code != 0 {
		t.Fatalf("cli.Main() = %d, want 0; stderr: %s", code, stderr.String())
	}
	if stdout.String() != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout.String(), "ok\n")
	}
}

func TestVerifyReleaseTamperedChecksums(t *testing.T) {
	priv := verifyReleaseTestKey(t)
	dir := t.TempDir()

	checksums := []byte("deadbeef  ajent_1.0.0_linux_amd64.tar.gz\n")
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, checksums)) + "\n"
	sigPath := verifyReleaseTestWrite(t, dir, "checksums.txt.sig", []byte(sig))

	tampered := append([]byte(nil), checksums...)
	tampered = append(tampered, 'x')
	checksumsPath := verifyReleaseTestWrite(t, dir, "checksums.txt", tampered)

	env, _, stderr := verifyReleaseTestEnv()
	code := cli.Main(context.Background(), env, []string{
		"verify-release", "--checksums", checksumsPath, "--sig", sigPath,
	})

	if code != 1 {
		t.Fatalf("cli.Main() = %d, want 1; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "release verification failed") {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), "release verification failed")
	}
}

func TestVerifyReleaseMissingSigFlag(t *testing.T) {
	dir := t.TempDir()
	checksumsPath := verifyReleaseTestWrite(t, dir, "checksums.txt", []byte("deadbeef  ajent_1.0.0_linux_amd64.tar.gz\n"))

	env, _, stderr := verifyReleaseTestEnv()
	code := cli.Main(context.Background(), env, []string{
		"verify-release", "--checksums", checksumsPath,
	})

	if code != 2 {
		t.Fatalf("cli.Main() = %d, want 2; stderr: %s", code, stderr.String())
	}
	if stderr.String() == "" {
		t.Error("stderr is empty, want a usage message")
	}
}
