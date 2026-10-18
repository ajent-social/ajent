// SPDX-License-Identifier: Apache-2.0

package fakerelease

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestBuildAssetsVerifies(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	binary := []byte("#!/bin/sh\necho hi\n")
	assets := BuildAssets(t, "0.7.0", "linux", "amd64", binary, priv)

	archiveName := "ajent_0.7.0_linux_amd64.tar.gz"
	archive, ok := assets[archiveName]
	if !ok {
		t.Fatalf("missing %s in %v", archiveName, assetNames(assets))
	}
	checksums, ok := assets["checksums.txt"]
	if !ok {
		t.Fatalf("missing checksums.txt in %v", assetNames(assets))
	}
	sig, ok := assets["checksums.txt.sig"]
	if !ok {
		t.Fatalf("missing checksums.txt.sig in %v", assetNames(assets))
	}

	if !strings.HasSuffix(string(sig), "\n") {
		t.Errorf("checksums.txt.sig does not end with a newline: %q", sig)
	}
	sigBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	if !ed25519.Verify(pub, checksums, sigBytes) {
		t.Fatal("signature does not verify against checksums.txt")
	}

	if !strings.Contains(string(checksums), archiveName) {
		t.Errorf("checksums.txt = %q, want it to mention %s", checksums, archiveName)
	}

	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	tr := tar.NewReader(gz)
	found := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		if hdr.Name != "ajent" {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read tar entry: %v", err)
		}
		if !bytes.Equal(data, binary) {
			t.Fatalf("archive entry = %q, want %q", data, binary)
		}
		found = true
	}
	if !found {
		t.Fatal(`archive does not contain an "ajent" entry`)
	}
}

func TestBuildAssetsWindowsZip(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	binary := []byte("MZ...")
	assets := BuildAssets(t, "0.7.0", "windows", "amd64", binary, priv)

	archiveName := "ajent_0.7.0_windows_amd64.zip"
	archive, ok := assets[archiveName]
	if !ok {
		t.Fatalf("missing %s in %v", archiveName, assetNames(assets))
	}

	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	found := false
	for _, f := range zr.File {
		if f.Name != "ajent.exe" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open zip entry: %v", err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read zip entry: %v", err)
		}
		if !bytes.Equal(data, binary) {
			t.Fatalf("zip entry = %q, want %q", data, binary)
		}
		found = true
	}
	if !found {
		t.Fatal(`zip does not contain an "ajent.exe" entry`)
	}
}

func assetNames(assets map[string][]byte) []string {
	out := make([]string, 0, len(assets))
	for name := range assets {
		out = append(out, name)
	}
	return out
}

func TestRoutes(t *testing.T) {
	s := New(t)

	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	stableAssets := BuildAssets(t, "0.7.0", "linux", "amd64", []byte("stable-binary"), priv)
	s.AddRelease("0.7.0", false, stableAssets)
	s.AddRelease("0.8.0-rc.1", true, map[string][]byte{
		"ajent_0.8.0-rc.1_linux_amd64.tar.gz": []byte("prerelease-archive"),
	})

	// GET /releases/latest returns the newest non-prerelease release.
	resp, err := http.Get(s.API() + "/releases/latest")
	if err != nil {
		t.Fatalf("GET /releases/latest: %v", err)
	}
	var latest ghRelease
	if err := decodeJSON(resp, &latest); err != nil {
		t.Fatalf("decode /releases/latest: %v", err)
	}
	if latest.TagName != "v0.7.0" {
		t.Errorf("tag_name = %q, want v0.7.0", latest.TagName)
	}
	if latest.Prerelease {
		t.Error("latest.Prerelease = true, want false")
	}

	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag header on /releases/latest")
	}

	// A conditional request with the same ETag gets a 304.
	req, err := http.NewRequest(http.MethodGet, s.API()+"/releases/latest", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("If-None-Match", etag)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("conditional GET: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusNotModified {
		t.Errorf("conditional GET status = %d, want %d", resp2.StatusCode, http.StatusNotModified)
	}

	// GET /releases?per_page=20 returns every release, newest first.
	resp3, err := http.Get(s.API() + "/releases?per_page=20")
	if err != nil {
		t.Fatalf("GET /releases: %v", err)
	}
	var list []ghRelease
	if err := decodeJSON(resp3, &list); err != nil {
		t.Fatalf("decode /releases: %v", err)
	}
	if len(list) != 2 || list[0].TagName != "v0.8.0-rc.1" || list[1].TagName != "v0.7.0" {
		t.Fatalf("releases list = %+v, want [v0.8.0-rc.1 v0.7.0]", list)
	}

	// GET /releases/tags/v<version> returns that one release.
	resp4, err := http.Get(s.API() + "/releases/tags/v0.7.0")
	if err != nil {
		t.Fatalf("GET /releases/tags/v0.7.0: %v", err)
	}
	if resp4.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp4.StatusCode)
	}
	var tagged ghRelease
	if err := decodeJSON(resp4, &tagged); err != nil {
		t.Fatalf("decode /releases/tags/v0.7.0: %v", err)
	}
	if tagged.TagName != "v0.7.0" {
		t.Errorf("tag_name = %q, want v0.7.0", tagged.TagName)
	}
	if len(tagged.Assets) != len(stableAssets) {
		t.Errorf("assets = %d, want %d", len(tagged.Assets), len(stableAssets))
	}

	// An unknown tag 404s.
	resp5, err := http.Get(s.API() + "/releases/tags/v9.9.9")
	if err != nil {
		t.Fatalf("GET unknown tag: %v", err)
	}
	defer func() { _ = resp5.Body.Close() }()
	if resp5.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp5.StatusCode)
	}

	// GET /latest.json returns the fallback shape, newest stable only.
	resp6, err := http.Get(s.FallbackURL())
	if err != nil {
		t.Fatalf("GET /latest.json: %v", err)
	}
	var fb struct {
		Version    string            `json:"version"`
		Tag        string            `json:"tag"`
		Prerelease bool              `json:"prerelease"`
		Assets     map[string]string `json:"assets"`
	}
	if err := decodeJSON(resp6, &fb); err != nil {
		t.Fatalf("decode /latest.json: %v", err)
	}
	if fb.Version != "0.7.0" || fb.Tag != "v0.7.0" || fb.Prerelease {
		t.Errorf("fallback = %+v, want version 0.7.0, tag v0.7.0, prerelease false", fb)
	}
	if len(fb.Assets) != len(stableAssets) {
		t.Errorf("fallback assets = %d, want %d", len(fb.Assets), len(stableAssets))
	}

	// GET /download/v<version>/<name> serves the raw asset bytes.
	for name, want := range stableAssets {
		url := s.URL + "/download/v0.7.0/" + name
		resp, err := http.Get(url) //nolint:gosec // fixed prefix against our own fake server, name is our own map key
		if err != nil {
			t.Fatalf("GET %s: %v", url, err)
		}
		got, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("read %s: %v", url, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("GET %s = %q, want %q", url, got, want)
		}
	}

	// An unknown download 404s.
	resp7, err := http.Get(s.URL + "/download/v0.7.0/nope")
	if err != nil {
		t.Fatalf("GET unknown download: %v", err)
	}
	defer func() { _ = resp7.Body.Close() }()
	if resp7.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp7.StatusCode)
	}

	// An entirely unknown path 404s.
	resp8, err := http.Get(s.URL + "/nope")
	if err != nil {
		t.Fatalf("GET unknown path: %v", err)
	}
	defer func() { _ = resp8.Body.Close() }()
	if resp8.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp8.StatusCode)
	}

	// SetStatus forces a status for matching requests.
	s.SetStatus("/releases/latest", http.StatusForbidden)
	resp9, err := http.Get(s.API() + "/releases/latest")
	if err != nil {
		t.Fatalf("GET forced status: %v", err)
	}
	defer func() { _ = resp9.Body.Close() }()
	if resp9.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp9.StatusCode)
	}

	got := s.Requests()
	if len(got) == 0 {
		t.Fatal("Requests() is empty")
	}
	if got[0] != "GET /repos/ajent-social/ajent/releases/latest" {
		t.Errorf("Requests()[0] = %q, want the first GET recorded verbatim", got[0])
	}
}

func decodeJSON(resp *http.Response, v any) error {
	defer resp.Body.Close() //nolint:errcheck // best-effort close after reading the response
	return json.NewDecoder(resp.Body).Decode(v)
}
