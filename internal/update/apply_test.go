// SPDX-License-Identifier: Apache-2.0

// External test package: fakerelease imports update (for Newer), so this
// file must not live in package update, or the two would form an import
// cycle in the test binary (the same reason source_test.go, T6.2, is
// package update_test). TestReplaceWindowsRollback, which needs whitebox
// access to the unexported replaceExecutable and applyTestLockShareRead,
// lives in detach_test.go instead, which has no fakerelease dependency.
package update_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/ajent-social/ajent-client/internal/testutil/fakerelease"
	"github.com/ajent-social/ajent-client/internal/update"
)

// applyTestKey generates a fresh Ed25519 key pair, appends its public key
// to update.PublicKeys for the duration of the test (restored by
// t.Cleanup, mirroring cmd/ajent/cmd_verify_release_test.go's
// verifyReleaseTestKey), and returns the private key for signing test
// release assets.
func applyTestKey(t *testing.T) ed25519.PrivateKey {
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

// applyTestExeName returns the file name a target executable would have
// on the current platform.
func applyTestExeName() string {
	if runtime.GOOS == "windows" {
		return "ajent.exe"
	}
	return "ajent"
}

var (
	applyTestBinMu    sync.Mutex
	applyTestBinCache = map[string][]byte{}
)

// applyTestBuildBinary builds the real ajent binary at version v (with
// -ldflags "-X .../internal/version.Version=v", so Apply's smoke test
// "<tmp> version --json" reports it) and returns its bytes, caching by
// version so the same build is reused across subtests. It skips the test
// in -short mode, since it invokes the Go toolchain.
func applyTestBuildBinary(t *testing.T, v string) []byte {
	t.Helper()
	if testing.Short() {
		t.Skip("applyTestBuildBinary: skipping go build in -short mode")
	}

	applyTestBinMu.Lock()
	defer applyTestBinMu.Unlock()

	if cached, ok := applyTestBinCache[v]; ok {
		return cached
	}

	out := filepath.Join(t.TempDir(), "ajent-helper-"+v)
	if runtime.GOOS == "windows" {
		out += ".exe"
	}

	// #nosec G204 -- "go" and every argument are fixed or test-derived (v is a literal version string this test file passes), never attacker input.
	cmd := exec.Command("go", "build",
		"-ldflags", "-X github.com/ajent-social/ajent-client/internal/version.Version="+v,
		"-o", out,
		"github.com/ajent-social/ajent-client/cmd/ajent",
	)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building helper binary for version %s: %v\n%s", v, err, combined)
	}

	data, err := os.ReadFile(out) //nolint:gosec // G304: out is a path this same helper just built under t.TempDir().
	if err != nil {
		t.Fatalf("reading helper binary: %v", err)
	}

	applyTestBinCache[v] = data
	return data
}

// applyTestFlipByte returns a copy of data with its first byte inverted,
// for tamper tests.
func applyTestFlipByte(data []byte) []byte {
	out := append([]byte(nil), data...)
	if len(out) > 0 {
		out[0] ^= 0xFF
	}
	return out
}

// applyTestServer starts a fakerelease server publishing one release built
// from assets, and returns a Source pointed at it.
func applyTestServer(t *testing.T, version string, assets map[string][]byte) *update.Source {
	t.Helper()

	srv := fakerelease.New(t)
	srv.AddRelease(version, false, assets)

	return &update.Source{
		HTTP:        http.DefaultClient,
		GitHubAPI:   srv.API(),
		FallbackURL: srv.FallbackURL(),
		CacheDir:    t.TempDir(),
	}
}

// applyTestSeedExe writes before as the target executable's initial
// content in a fresh temp directory and returns its path.
func applyTestSeedExe(t *testing.T, before []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), applyTestExeName())
	if err := os.WriteFile(path, before, 0o755); err != nil { //nolint:gosec // G306: this is the mode a real executable needs; the path is under t.TempDir().
		t.Fatalf("seeding exe: %v", err)
	}
	return path
}

func TestArchiveNameMatchesFakeRelease(t *testing.T) {
	priv := applyTestKey(t)

	assets := fakerelease.BuildAssets(t, "1.2.3", runtime.GOOS, runtime.GOARCH, []byte("stub-binary"), priv)

	name := update.ArchiveName("1.2.3", runtime.GOOS, runtime.GOARCH)
	if _, ok := assets[name]; !ok {
		keys := make([]string, 0, len(assets))
		for k := range assets {
			keys = append(keys, k)
		}
		t.Errorf("BuildAssets keys = %v, want one matching ArchiveName() = %q", keys, name)
	}
}

func TestApplyReplaces(t *testing.T) {
	priv := applyTestKey(t)
	binary := applyTestBuildBinary(t, "0.7.1")

	assets := fakerelease.BuildAssets(t, "0.7.1", runtime.GOOS, runtime.GOARCH, binary, priv)
	s := applyTestServer(t, "0.7.1", assets)

	ctx := context.Background()
	r, err := s.Get(ctx, "0.7.1")
	if err != nil {
		t.Fatalf("s.Get: %v", err)
	}

	exePath := applyTestSeedExe(t, []byte("old-binary-0.7.0-placeholder"))

	if err := update.Apply(ctx, s, r, exePath); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// #nosec G204 -- exePath is the file Apply just installed in this test's own temp directory.
	out, err := exec.Command(exePath, "version", "--json").Output()
	if err != nil {
		t.Fatalf("running replaced binary: %v", err)
	}
	if got := applyTestDecodeVersion(t, out); got != "0.7.1" {
		t.Errorf("replaced binary reports version %q, want 0.7.1", got)
	}
}

// applyTestDecodeVersion decodes out (the JSON "ajent version --json"
// prints, via cli.PrintJSON's two-space indenting) and returns its
// "version" field.
func applyTestDecodeVersion(t *testing.T, out []byte) string {
	t.Helper()
	var info struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(out, &info); err != nil {
		t.Fatalf("decoding %s: %v", out, err)
	}
	return info.Version
}

func TestApplyTamperedChecksums(t *testing.T) {
	priv := applyTestKey(t)

	assets := fakerelease.BuildAssets(t, "0.7.1", runtime.GOOS, runtime.GOARCH, []byte("fake-binary"), priv)
	assets["checksums.txt"] = applyTestFlipByte(assets["checksums.txt"])

	s := applyTestServer(t, "0.7.1", assets)
	ctx := context.Background()
	r, err := s.Get(ctx, "0.7.1")
	if err != nil {
		t.Fatalf("s.Get: %v", err)
	}

	before := []byte("original-exe-bytes")
	exePath := applyTestSeedExe(t, before)

	err = update.Apply(ctx, s, r, exePath)
	if !errors.Is(err, update.ErrBadSignature) {
		t.Fatalf("Apply() error = %v, want ErrBadSignature", err)
	}

	after, readErr := os.ReadFile(exePath) //nolint:gosec // G304: exePath is this test's own t.TempDir() file.
	if readErr != nil {
		t.Fatalf("reading exe: %v", readErr)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("exe changed after a tampered-checksums Apply")
	}
}

func TestApplyTamperedSignature(t *testing.T) {
	priv := applyTestKey(t)

	assets := fakerelease.BuildAssets(t, "0.7.1", runtime.GOOS, runtime.GOARCH, []byte("fake-binary"), priv)

	_, otherPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	wrongSig := base64.StdEncoding.EncodeToString(ed25519.Sign(otherPriv, assets["checksums.txt"])) + "\n"
	assets["checksums.txt.sig"] = []byte(wrongSig)

	s := applyTestServer(t, "0.7.1", assets)
	ctx := context.Background()
	r, err := s.Get(ctx, "0.7.1")
	if err != nil {
		t.Fatalf("s.Get: %v", err)
	}

	before := []byte("original-exe-bytes")
	exePath := applyTestSeedExe(t, before)

	err = update.Apply(ctx, s, r, exePath)
	if !errors.Is(err, update.ErrBadSignature) {
		t.Fatalf("Apply() error = %v, want ErrBadSignature", err)
	}

	after, readErr := os.ReadFile(exePath) //nolint:gosec // G304: exePath is this test's own t.TempDir() file.
	if readErr != nil {
		t.Fatalf("reading exe: %v", readErr)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("exe changed after a tampered-signature Apply")
	}
}

func TestApplyTamperedArchive(t *testing.T) {
	priv := applyTestKey(t)

	assets := fakerelease.BuildAssets(t, "0.7.1", runtime.GOOS, runtime.GOARCH, []byte("fake-binary"), priv)
	name := update.ArchiveName("0.7.1", runtime.GOOS, runtime.GOARCH)
	assets[name] = applyTestFlipByte(assets[name])

	s := applyTestServer(t, "0.7.1", assets)
	ctx := context.Background()
	r, err := s.Get(ctx, "0.7.1")
	if err != nil {
		t.Fatalf("s.Get: %v", err)
	}

	before := []byte("original-exe-bytes")
	exePath := applyTestSeedExe(t, before)

	err = update.Apply(ctx, s, r, exePath)
	if !errors.Is(err, update.ErrChecksumMismatch) {
		t.Fatalf("Apply() error = %v, want ErrChecksumMismatch", err)
	}

	after, readErr := os.ReadFile(exePath) //nolint:gosec // G304: exePath is this test's own t.TempDir() file.
	if readErr != nil {
		t.Fatalf("reading exe: %v", readErr)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("exe changed after a tampered-archive Apply")
	}
}

func TestApplyWrongVersionBinary(t *testing.T) {
	priv := applyTestKey(t)
	binary := applyTestBuildBinary(t, "0.7.0")

	// The release claims 0.7.1, but the archive actually holds the 0.7.0
	// binary built above.
	assets := fakerelease.BuildAssets(t, "0.7.1", runtime.GOOS, runtime.GOARCH, binary, priv)
	s := applyTestServer(t, "0.7.1", assets)

	ctx := context.Background()
	r, err := s.Get(ctx, "0.7.1")
	if err != nil {
		t.Fatalf("s.Get: %v", err)
	}

	before := []byte("original-exe-bytes")
	exePath := applyTestSeedExe(t, before)

	err = update.Apply(ctx, s, r, exePath)
	if err == nil || !strings.Contains(err.Error(), "self-check") {
		t.Fatalf("Apply() error = %v, want a self-check error", err)
	}

	after, readErr := os.ReadFile(exePath) //nolint:gosec // G304: exePath is this test's own t.TempDir() file.
	if readErr != nil {
		t.Fatalf("reading exe: %v", readErr)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("exe changed after a wrong-version-binary Apply")
	}
}

func TestApplyMissingAsset(t *testing.T) {
	r := update.Release{Version: "0.7.1", Assets: map[string]string{}}
	s := &update.Source{HTTP: http.DefaultClient, CacheDir: t.TempDir()}

	exePath := applyTestSeedExe(t, []byte("original"))

	err := update.Apply(context.Background(), s, r, exePath)
	if !errors.Is(err, update.ErrNoAsset) {
		t.Fatalf("Apply() error = %v, want ErrNoAsset", err)
	}
}

func TestApplyReadOnlyDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("read-only directory permissions are not meaningful on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root ignores directory permissions")
	}

	priv := applyTestKey(t)
	binary := applyTestBuildBinary(t, "0.7.1")
	assets := fakerelease.BuildAssets(t, "0.7.1", runtime.GOOS, runtime.GOARCH, binary, priv)
	s := applyTestServer(t, "0.7.1", assets)

	ctx := context.Background()
	r, err := s.Get(ctx, "0.7.1")
	if err != nil {
		t.Fatalf("s.Get: %v", err)
	}

	before := []byte("original-exe-bytes")
	exePath := applyTestSeedExe(t, before)
	dir := filepath.Dir(exePath)

	if err := os.Chmod(dir, 0o555); err != nil { //nolint:gosec // G302: dir needs read+execute (not write) so this test's own later os.ReadFile can still traverse it; that is the read-only scenario under test.
		t.Fatalf("making %s read-only: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) }) //nolint:gosec // G302: restores dir to a normal, writable t.TempDir() mode.

	err = update.Apply(ctx, s, r, exePath)
	if err == nil {
		t.Fatal("Apply() succeeded in a read-only directory, want an error")
	}
	if !strings.Contains(err.Error(), "cannot write") {
		t.Errorf("Apply() error = %v, want it to contain %q", err, "cannot write")
	}

	if err := os.Chmod(dir, 0o755); err != nil { //nolint:gosec // G302: restores dir to a normal, writable t.TempDir() mode before reading it back below.
		t.Fatalf("restoring %s permissions: %v", dir, err)
	}
	after, readErr := os.ReadFile(exePath) //nolint:gosec // G304: exePath is this test's own t.TempDir() file.
	if readErr != nil {
		t.Fatalf("reading exe: %v", readErr)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("exe changed despite a read-only directory")
	}
}

// applyTestArchiveWithEntry builds an archive in the current platform's
// format (tar.gz, or zip on windows) containing a single entry named
// entryName holding content, without any of Apply's safety checks --
// unlike fakerelease.BuildAssets, which always uses a safe entry name.
func applyTestArchiveWithEntry(t *testing.T, entryName string, content []byte) []byte {
	t.Helper()
	if runtime.GOOS == "windows" {
		return applyTestBuildZipEntry(t, entryName, content)
	}
	return applyTestBuildTarGzEntry(t, entryName, content)
}

func applyTestBuildTarGzEntry(t *testing.T, name string, content []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func applyTestBuildZipEntry(t *testing.T, name string, content []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	fw, err := zw.Create(name)
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestApplyZipSlip(t *testing.T) {
	priv := applyTestKey(t)

	name := update.ArchiveName("0.7.1", runtime.GOOS, runtime.GOARCH)
	maliciousEntry := "../ajent"
	if runtime.GOOS == "windows" {
		maliciousEntry = "../ajent.exe"
	}
	archive := applyTestArchiveWithEntry(t, maliciousEntry, []byte("malicious-payload"))

	sum := sha256.Sum256(archive)
	checksums := []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, checksums)) + "\n"

	assets := map[string][]byte{
		name:                archive,
		"checksums.txt":     checksums,
		"checksums.txt.sig": []byte(sig),
	}
	s := applyTestServer(t, "0.7.1", assets)

	ctx := context.Background()
	r, err := s.Get(ctx, "0.7.1")
	if err != nil {
		t.Fatalf("s.Get: %v", err)
	}

	before := []byte("original-exe-bytes")
	exePath := applyTestSeedExe(t, before)

	err = update.Apply(ctx, s, r, exePath)
	if err == nil {
		t.Fatal("Apply() succeeded on an archive with a path-traversal entry, want an error")
	}

	after, readErr := os.ReadFile(exePath) //nolint:gosec // G304: exePath is this test's own t.TempDir() file.
	if readErr != nil {
		t.Fatalf("reading exe: %v", readErr)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("exe changed despite the rejected archive")
	}
}

func TestCleanupOld(t *testing.T) {
	exePath := applyTestSeedExe(t, []byte("current"))
	oldPath := exePath + ".old"
	if err := os.WriteFile(oldPath, []byte("stale"), 0o644); err != nil { //nolint:gosec // G306: a plain leftover file is fine at this mode; the path is under t.TempDir().
		t.Fatalf("seeding %s: %v", oldPath, err)
	}

	update.CleanupOld(exePath)

	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("CleanupOld() left %s in place: %v", oldPath, err)
	}

	// A missing .old file must not panic or error out.
	update.CleanupOld(exePath)
}
