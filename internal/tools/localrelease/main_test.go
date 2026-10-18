// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixtureKey(t *testing.T, dir string) (ed25519.PrivateKey, string) {
	t.Helper()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{17}, 32))
	pub := key.Public().(ed25519.PublicKey)
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string][]byte{
		"internal/update/keys.go":         []byte("package update\nvar releaseKeys = []string{\"" + base64.StdEncoding.EncodeToString(pub) + "\"}\n"),
		"packaging/ajent-release.pub.pem": pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}),
	} {
		p := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return key, base64.StdEncoding.EncodeToString(key.Seed())
}

func TestSigningKeyTrust(t *testing.T) {
	for _, tc := range []string{"seed", "full", "missing", "malformed", "wrong-key", "inconsistent-full", "empty-source", "wrong-pem"} {
		t.Run(tc, func(t *testing.T) {
			dir := t.TempDir()
			key, raw := fixtureKey(t, dir)
			switch tc {
			case "full":
				raw = base64.StdEncoding.EncodeToString(key)
			case "missing":
				raw = ""
			case "malformed":
				raw = "secret-invalid-key"
			case "wrong-key":
				raw = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{18}, 32))
			case "inconsistent-full":
				key[63] ^= 1
				raw = base64.StdEncoding.EncodeToString(key)
			case "empty-source":
				if err := os.WriteFile(filepath.Join(dir, "internal/update/keys.go"), []byte("package update\nvar releaseKeys=[]string{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "wrong-pem":
				if err := os.WriteFile(filepath.Join(dir, "packaging/ajent-release.pub.pem"), []byte("invalid"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := signingKey(raw, dir)
			wantOK := tc == "seed" || tc == "full"
			if (err == nil) != wantOK {
				t.Fatalf("success=%v, want %v", err == nil, wantOK)
			}
			if err != nil && raw != "" && strings.Contains(err.Error(), raw) {
				t.Fatal("error leaked key")
			}
		})
	}
}

func TestArchiveContents(t *testing.T) {
	for _, ext := range []string{".zip", ".tar.gz"} {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range []string{"binary", "LICENSE", "NOTICE", "README.md"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(dir, "candidate"+ext)
			stamp := time.Unix(1700000000, 0)
			if err := archive(path, dir, filepath.Join(dir, "binary"), "ajent", stamp); err != nil {
				t.Fatal(err)
			}
			found := map[string]string{}
			if ext == ".zip" {
				z, err := zip.OpenReader(path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := z.Close(); err != nil {
						t.Errorf("close archive reader: %v", err)
					}
				})
				for _, f := range z.File {
					if !f.Modified.Equal(stamp) {
						t.Fatalf("ZIP timestamp mismatch for %s: %v", f.Name, f.Modified)
					}
					r, err := f.Open()
					if err != nil {
						t.Fatal(err)
					}
					b, err := io.ReadAll(r)
					if err != nil {
						t.Fatal(err)
					}
					if err := r.Close(); err != nil {
						t.Fatal(err)
					}
					found[f.Name] = string(b)
				}
			} else {
				f, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := f.Close(); err != nil {
						t.Errorf("close archive reader: %v", err)
					}
				})
				g, err := gzip.NewReader(f)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := g.Close(); err != nil {
						t.Errorf("close archive reader: %v", err)
					}
				})
				tr := tar.NewReader(g)
				for {
					h, err := tr.Next()
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if !h.ModTime.Equal(stamp) {
						t.Fatalf("tar timestamp mismatch for %s: %v", h.Name, h.ModTime)
					}
					b, err := io.ReadAll(tr)
					if err != nil {
						t.Fatal(err)
					}
					found[h.Name] = string(b)
				}
			}
			if len(found) != 4 || found["ajent"] != "binary" || found["LICENSE"] != "LICENSE" || found["NOTICE"] != "NOTICE" || found["README.md"] != "README.md" {
				t.Fatal("archive contents mismatch")
			}
			if err := archive(path, dir, filepath.Join(dir, "binary"), "ajent", time.Now()); err == nil {
				t.Fatal("overwrote existing archive")
			}
		})
	}
}

func TestRunPreparation(t *testing.T) {
	for _, tc := range []string{"signed", "local", "missing-key", "dirty", "wrong-head", "failed-check", "failed-build", "failed-native", "failed-verifier", "existing-output"} {
		t.Run(tc, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, "source")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			key, raw := fixtureKey(t, dir)
			sha := strings.Repeat("a", 40)
			out := filepath.Join(parent, "candidate")
			if tc == "missing-key" {
				raw = ""
			}
			if tc == "existing-output" {
				if err := os.Mkdir(out, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			var snapshot bytes.Buffer
			tw := tar.NewWriter(&snapshot)
			for _, n := range []string{"LICENSE", "NOTICE", "README.md"} {
				if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(n))}); err != nil {
					t.Fatal(err)
				}
				if _, err := tw.Write([]byte(n)); err != nil {
					t.Fatal(err)
				}
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			built := false
			call := func(_ context.Context, name string, args []string, _ string, env []string) ([]byte, error) {
				for _, e := range env {
					if strings.HasPrefix(e, "RELEASE_ED25519_KEY=") || strings.Contains(e, "extraKeyB64") {
						t.Fatal("secret or test trust reached child")
					}
				}
				joined := strings.Join(args, " ")
				switch name {
				case "git":
					switch joined {
					case "rev-parse --show-toplevel":
						return []byte(dir), nil
					case "rev-parse HEAD":
						if tc == "wrong-head" {
							return []byte(strings.Repeat("b", 40)), nil
						}
						return []byte(sha), nil
					case "status --porcelain --untracked-files=all":
						if tc == "dirty" {
							return []byte(" M file"), nil
						}
						return nil, nil
					case "archive --format=tar " + sha:
						return snapshot.Bytes(), nil
					case "show -s --format=%cI " + sha:
						return []byte("2026-10-16T00:00:00Z"), nil
					}
				case "gofmt":
					return nil, nil
				case "make", "govulncheck":
					if tc == "failed-check" {
						return []byte("diagnostic"), errors.New("failed")
					}
					return nil, nil
				case "go":
					if joined == "version" {
						return []byte("go version fixture"), nil
					}
					if args[0] == "build" {
						built = true
						if tc == "failed-build" {
							return nil, errors.New("failed")
						}
						for i, a := range args {
							if a == "-o" {
								return nil, os.WriteFile(args[i+1], []byte("fixture binary"), 0o700)
							}
						}
					}
				default:
					if args[0] == "version" {
						if tc == "failed-native" {
							return []byte(`{"version":"dev"}`), nil
						}
						version := "0.7.0"
						if tc == "local" {
							version += "-local." + sha[:12]
						}
						return json.Marshal(map[string]string{"version": version, "commit": sha})
					}
					if args[0] == "verify-release" {
						if tc == "failed-verifier" {
							return nil, errors.New("untrusted signature")
						}
						checks, err := os.ReadFile(args[2])
						if err != nil {
							return nil, err
						}
						sig, err := os.ReadFile(args[4])
						if err != nil {
							return nil, err
						}
						decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
						if err != nil || !ed25519.Verify(key.Public().(ed25519.PublicKey), checks, decoded) {
							return nil, errors.New("bad signature")
						}
						return []byte("ok"), nil
					}
				}
				return nil, fmt.Errorf("unexpected command %s", name)
			}
			args := []string{"prepare", "--version", "0.7.0", "--source-sha", sha, "--out", out}
			if tc == "local" {
				args = append(args, "--local")
			}
			err := run(context.Background(), args, func(k string) string {
				if k == "RELEASE_ED25519_KEY" {
					return raw
				}
				return ""
			}, call)
			success := tc == "signed" || tc == "local"
			if (err == nil) != success {
				t.Fatalf("success=%v want %v: %v", err == nil, success, err)
			}
			_, markerErr := os.Stat(filepath.Join(out, "prepared.json"))
			if (markerErr == nil) != success {
				t.Fatal("incorrect preparation marker")
			}
			if tc == "missing-key" && built {
				t.Fatal("built without approved key")
			}
			if !success {
				return
			}
			checks, err := os.ReadFile(filepath.Join(out, "checksums.txt"))
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(checks)), "\n")
			if len(lines) != 7 {
				t.Fatal("expected six archives and verification receipt")
			}
			for _, line := range lines {
				p := strings.SplitN(line, "  ", 2)
				if len(p) != 2 {
					t.Fatal("invalid checksum format")
				}
				data, err := os.ReadFile(filepath.Join(out, p[1]))
				if err != nil {
					t.Fatal(err)
				}
				if fmt.Sprintf("%x", sha256.Sum256(data)) != p[0] {
					t.Fatal("checksum mismatch")
				}
			}
			if tc == "local" {
				if _, err := os.Stat(filepath.Join(out, "checksums.txt.sig")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("unsigned local candidate has signature")
				}
			}
		})
	}
}

func TestUnpackUnsafeSource(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind byte
	}{{"../escape", tar.TypeReg}, {"link", tar.TypeSymlink}} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			tw := tar.NewWriter(&b)
			if err := tw.WriteHeader(&tar.Header{Name: tc.name, Typeflag: tc.kind, Linkname: "target"}); err != nil {
				t.Fatal(err)
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := unpack(b.Bytes(), t.TempDir()); err == nil {
				t.Fatal("accepted unsafe source")
			}
		})
	}
}

func TestRunRejectsUsageAndSecretFlags(t *testing.T) {
	for _, args := range [][]string{
		{}, {"publish"}, {"prepare", "--RELEASE_ED25519_KEY=secret-value"},
		{"prepare", "--version", "v0.7.0"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			called := false
			err := run(context.Background(), args, func(string) string { return "" }, func(context.Context, string, []string, string, []string) ([]byte, error) {
				called = true
				return nil, nil
			})
			if err == nil || called {
				t.Fatal("invalid usage reached execution")
			}
			if strings.Contains(err.Error(), "secret-value") {
				t.Fatal("flag error leaked value")
			}
		})
	}
}

func TestChildEnvDropsAmbientOverrides(t *testing.T) {
	env := childEnv(func(k string) string {
		switch k {
		case "PATH":
			return "tools"
		case "HOME":
			return "home"
		default:
			return "secret-value"
		}
	})
	for _, entry := range env {
		if strings.HasPrefix(entry, "RELEASE_ED25519_KEY=") || strings.HasPrefix(entry, "GOOS=") || strings.HasPrefix(entry, "GOARCH=") || entry == "GOFLAGS=secret-value" {
			t.Fatal("ambient build overrides survived")
		}
	}
	if !strings.Contains(strings.Join(env, "\n"), "GOTOOLCHAIN=local") {
		t.Fatal("automatic toolchain download allowed")
	}
}

func TestAddSourceSizeLimits(t *testing.T) {
	for _, tc := range []struct {
		name              string
		total, size, want int64
		fail              bool
	}{
		{name: "empty"},
		{name: "entry boundary", size: maxSourceEntry, want: maxSourceEntry},
		{name: "total boundary", total: maxSourceTotal - maxSourceEntry, size: maxSourceEntry, want: maxSourceTotal},
		{name: "negative entry", size: -1, fail: true},
		{name: "oversized entry", size: maxSourceEntry + 1, fail: true},
		{name: "oversized total", total: maxSourceTotal, size: 1, fail: true},
		{name: "invalid total", total: -1, fail: true},
		{name: "overflow total", total: 1<<63 - 1, size: 1, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := addSourceSize(tc.total, tc.size)
			if (err != nil) != tc.fail || (!tc.fail && got != tc.want) {
				t.Fatalf("got total %d, error %v", got, err)
			}
		})
	}
}

func TestUnpackRejectsOversizedHeader(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int64
	}{
		{name: "entry limit", size: maxSourceEntry + 1},
		{name: "huge entry", size: 1 << 40},
		{name: "truncated entry", size: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			tw := tar.NewWriter(&b)
			if err := tw.WriteHeader(&tar.Header{Name: "oversized", Mode: 0o600, Size: tc.size}); err != nil {
				t.Fatal(err)
			}
			// Header only: deliberately omit payload and tar trailer, avoiding large allocation.
			dir := t.TempDir()
			err := unpack(b.Bytes(), dir)
			if err == nil {
				t.Fatal("accepted oversized or truncated source entry")
			}
			if tc.size > maxSourceEntry {
				if !strings.Contains(err.Error(), "size limit") {
					t.Fatalf("did not reject size before copying: %v", err)
				}
				if _, err := os.Stat(filepath.Join(dir, "oversized")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("oversized entry created a file")
				}
			}
		})
	}
}
