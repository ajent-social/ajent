// SPDX-License-Identifier: Apache-2.0

// Command signchecksums signs a GoReleaser checksums.txt with Ed25519, for
// use as the "signs" cmd in .goreleaser.yaml, and generates throwaway key
// pairs for tests and release rehearsals. See
// docs/adr/004-release-signing.md and docs/tasks/T5.3.md.
//
// Usage:
//
//	signchecksums -in checksums.txt -out checksums.txt.sig
//	signchecksums -keygen /path/to/prefix
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stderr))
}

// run implements the signchecksums CLI against args, getenv and stderr, so
// main_test.go can exercise every code path without touching real
// environment variables, os.Exit or the process's real stderr. It returns
// the process exit code: 0 on success, 1 for a signing failure (a missing,
// malformed or wrong-length key, or an I/O error), 2 for a flag-usage
// mistake.
func run(args []string, getenv func(string) string, stderr io.Writer) int {
	fs := flag.NewFlagSet("signchecksums", flag.ContinueOnError)
	fs.SetOutput(stderr)
	in := fs.String("in", "", "path to the checksums file to sign")
	out := fs.String("out", "", "path to write the signature to")
	keygen := fs.String("keygen", "", "generate a new Ed25519 key pair at this path prefix, for tests and rehearsals only, then exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *keygen != "" {
		if err := signchecksumsKeygen(*keygen); err != nil {
			_, _ = fmt.Fprintf(stderr, "signchecksums: %v\n", err)
			return 1
		}
		return 0
	}

	if *in == "" || *out == "" {
		_, _ = fmt.Fprintln(stderr, "signchecksums: -in and -out are required (or use -keygen)")
		return 2
	}

	key, err := signchecksumsLoadKey(getenv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "signchecksums: %v\n", err)
		return 1
	}

	// #nosec G304 -- in is a maintainer/CI-supplied -in flag naming the release checksums file to sign, not attacker input.
	data, err := os.ReadFile(*in)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "signchecksums: reading %s: %v\n", *in, err)
		return 1
	}

	sig := ed25519.Sign(key, data)
	encoded := base64.StdEncoding.EncodeToString(sig) + "\n"
	// #nosec G306 G703 -- out is a maintainer/CI-supplied -out flag naming where to write the signature, not attacker input; checksums.txt.sig is a public release artifact meant to be world-readable, like checksums.txt itself.
	if err := os.WriteFile(*out, []byte(encoded), 0o644); err != nil {
		_, _ = fmt.Fprintf(stderr, "signchecksums: writing %s: %v\n", *out, err)
		return 1
	}
	return 0
}

// signchecksumsLoadKey decodes RELEASE_ED25519_KEY (getenv's value for that
// name) and returns the Ed25519 private key it holds. The env value is
// standard base64 of either the 32-byte seed (the normal case, expanded
// with ed25519.NewKeyFromSeed) or the full 64-byte private key, accepted
// for robustness. Its error messages never include the raw env value, only
// its decoded length, so a malformed key never leaks key material to logs.
func signchecksumsLoadKey(getenv func(string) string) (ed25519.PrivateKey, error) {
	raw := getenv("RELEASE_ED25519_KEY")
	if raw == "" {
		return nil, fmt.Errorf("RELEASE_ED25519_KEY is not set")
	}

	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("RELEASE_ED25519_KEY is not valid standard base64")
	}

	switch len(decoded) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(decoded), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(decoded), nil
	default:
		return nil, fmt.Errorf("RELEASE_ED25519_KEY decodes to %d bytes, want %d (seed) or %d (private key)", len(decoded), ed25519.SeedSize, ed25519.PrivateKeySize)
	}
}

// signchecksumsKeygen generates a fresh Ed25519 key pair for tests and
// release rehearsals and writes it at prefix under three suffixes:
// "<prefix>.seed" (standard base64 of the 32-byte seed plus a newline,
// mode 0600), "<prefix>.pub" (standard base64 of the 32-byte public key
// plus a newline, mode 0644) and "<prefix>.pem" (a PKIX "PUBLIC KEY" PEM
// block, mode 0644).
func signchecksumsKeygen(prefix string) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generating key: %w", err)
	}

	seedB64 := base64.StdEncoding.EncodeToString(priv.Seed()) + "\n"
	if err := os.WriteFile(prefix+".seed", []byte(seedB64), 0o600); err != nil {
		return fmt.Errorf("writing seed: %w", err)
	}

	pubB64 := base64.StdEncoding.EncodeToString(pub) + "\n"
	// #nosec G306 -- the .pub file holds only the public key, meant to be shared; the .seed file above carries the secret and is written 0600.
	if err := os.WriteFile(prefix+".pub", []byte(pubB64), 0o644); err != nil {
		return fmt.Errorf("writing public key: %w", err)
	}

	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return fmt.Errorf("marshaling public key: %w", err)
	}
	block := &pem.Block{Type: "PUBLIC KEY", Bytes: der}
	// #nosec G306 -- the .pem file holds only the public key, meant to be shared.
	if err := os.WriteFile(prefix+".pem", pem.EncodeToMemory(block), 0o644); err != nil {
		return fmt.Errorf("writing PEM: %w", err)
	}
	return nil
}
