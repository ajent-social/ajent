// SPDX-License-Identifier: Apache-2.0

package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"os"
	"strings"
	"testing"
)

func TestReleaseTrustMaterial(t *testing.T) {
	if len(releaseKeys) == 0 {
		t.Fatal("official release trust must contain a public key")
	}
	raw, err := os.ReadFile("../../packaging/ajent-release.pub.pem")
	if err != nil {
		t.Fatal(err)
	}
	// Git checkouts may use CRLF on Windows; compare the same PEM text.
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PUBLIC KEY" || len(bytes.TrimSpace(rest)) != 0 {
		t.Fatal("invalid release public PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		t.Fatal("release key is not Ed25519")
	}
	trusted := false
	for _, encoded := range releaseKeys {
		if encoded == base64.StdEncoding.EncodeToString(key) {
			trusted = true
		}
	}
	if !trusted {
		t.Fatal("packaging key is not compiled into release trust")
	}
	digest := sha256.Sum256(block.Bytes)
	tests := []struct{ name, path, want string }{
		{"installer", "../../scripts/install.sh", "<<'AJENT_RELEASE_PEM'\n" + string(raw) + "AJENT_RELEASE_PEM"},
		{"security public key", "../../SECURITY.md", base64.StdEncoding.EncodeToString(key)},
		{"security fingerprint", "../../SECURITY.md", hex.EncodeToString(digest[:])},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(tt.path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.ReplaceAll(string(data), "\r\n", "\n"), tt.want) {
				t.Fatal("published trust material does not match packaging key")
			}
		})
	}
}
