// SPDX-License-Identifier: Apache-2.0

// Package fakerelease serves GitHub-shaped release metadata and signed
// release assets for tests, so internal/update and its consumers never
// make a real network call to api.github.com.
package fakerelease

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ajent-social/ajent/internal/update"
)

// release is one registered fake release.
type release struct {
	version    string
	prerelease bool
	assets     map[string][]byte // name -> content
}

// Server is a fake GitHub Releases API plus a fake pkg.ajent.social
// fallback endpoint, backed by an httptest.Server.
type Server struct {
	URL string

	t        testing.TB
	srv      *httptest.Server
	mu       sync.Mutex
	releases []release
	statuses map[string]int
	requests []string
}

// New starts a fake release server, closed automatically via t.Cleanup.
func New(t testing.TB) *Server {
	t.Helper()

	s := &Server{
		t:        t,
		statuses: map[string]int{},
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	s.URL = s.srv.URL
	t.Cleanup(s.srv.Close)
	return s
}

// API returns the fake GitHub Releases API base, matching the shape of
// update.DefaultGitHubAPI.
func (s *Server) API() string {
	return s.URL + "/repos/ajent-social/ajent"
}

// FallbackURL returns the fake pkg.ajent.social latest.json URL, matching
// the shape of update.DefaultFallbackURL.
func (s *Server) FallbackURL() string {
	return s.URL + "/latest.json"
}

// AddRelease registers a release. assets maps an asset file name to its
// content; each is served at s.URL+"/download/v<version>/<name>" and
// listed in the release's browser_download_url.
func (s *Server) AddRelease(version string, prerelease bool, assets map[string][]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releases = append(s.releases, release{version: version, prerelease: prerelease, assets: assets})
}

// SetStatus forces the given HTTP status code for any request whose path
// ends with path (for example "/releases/latest" or the full
// "/repos/ajent-social/ajent/releases/latest").
func (s *Server) SetStatus(path string, code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses[path] = code
}

// Requests returns every request received so far, as "METHOD path[?query]",
// in the order they arrived.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.requests))
	copy(out, s.requests)
	return out
}

// ghAsset mirrors the subset of a GitHub release asset that update.Source
// consumes.
type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// ghRelease mirrors the subset of a GitHub release object that
// update.Source consumes.
type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Prerelease bool      `json:"prerelease"`
	Draft      bool      `json:"draft"`
	Assets     []ghAsset `json:"assets"`
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, r.Method+" "+r.URL.RequestURI())
	forcedCode, forced := 0, false
	for suffix, code := range s.statuses {
		if strings.HasSuffix(r.URL.Path, suffix) {
			forcedCode, forced = code, true
			break
		}
	}
	s.mu.Unlock()

	if forced {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(forcedCode)
		_, _ = w.Write([]byte(`{"message":"forced status"}`))
		return
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/repos/ajent-social/ajent/releases/latest":
		s.serveLatest(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/repos/ajent-social/ajent/releases":
		s.serveList(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/ajent-social/ajent/releases/tags/v"):
		s.serveTag(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/latest.json":
		s.serveFallback(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/download/"):
		s.serveDownload(w, r)
	default:
		http.NotFound(w, r)
	}
}

// newestStable returns the highest-version non-prerelease release.
func (s *Server) newestStable() (release, bool) {
	var best release
	found := false
	for _, rel := range s.releases {
		if rel.prerelease {
			continue
		}
		if !found || update.Newer(rel.version, best.version) {
			best = rel
			found = true
		}
	}
	return best, found
}

func (s *Server) serveLatest(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	rel, ok := s.newestStable()
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, r, toGHRelease(rel, s.URL))
}

func (s *Server) serveList(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	all := make([]release, len(s.releases))
	copy(all, s.releases)
	s.mu.Unlock()

	// Newest first, ignoring drafts (fake releases are never drafts).
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && update.Newer(all[j].version, all[j-1].version); j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}

	out := make([]ghRelease, 0, len(all))
	for _, rel := range all {
		out = append(out, toGHRelease(rel, s.URL))
	}
	writeJSON(w, r, out)
}

func (s *Server) serveTag(w http.ResponseWriter, r *http.Request) {
	tag := strings.TrimPrefix(r.URL.Path, "/repos/ajent-social/ajent/releases/tags/")
	version := strings.TrimPrefix(tag, "v")

	s.mu.Lock()
	var found release
	ok := false
	for _, rel := range s.releases {
		if rel.version == version {
			found, ok = rel, true
			break
		}
	}
	s.mu.Unlock()

	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, r, toGHRelease(found, s.URL))
}

func (s *Server) serveFallback(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	rel, ok := s.newestStable()
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}

	assets := make(map[string]string, len(rel.assets))
	for name := range rel.assets {
		assets[name] = s.URL + "/download/v" + rel.version + "/" + name
	}

	writeJSON(w, r, map[string]any{
		"version":    rel.version,
		"tag":        "v" + rel.version,
		"prerelease": rel.prerelease,
		"assets":     assets,
	})
}

func (s *Server) serveDownload(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/download/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	version := strings.TrimPrefix(parts[0], "v")
	name := parts[1]

	s.mu.Lock()
	var content []byte
	found := false
	for _, rel := range s.releases {
		if rel.version != version {
			continue
		}
		content, found = rel.assets[name]
		break
	}
	s.mu.Unlock()

	if !found {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

// toGHRelease converts a fake release into the GitHub-shaped JSON object
// update.Source parses, pointing each asset's browser_download_url at
// baseURL+"/download/v<version>/<name>".
func toGHRelease(rel release, baseURL string) ghRelease {
	names := make([]string, 0, len(rel.assets))
	for name := range rel.assets {
		names = append(names, name)
	}
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}

	assets := make([]ghAsset, 0, len(names))
	for _, name := range names {
		assets = append(assets, ghAsset{
			Name:               name,
			BrowserDownloadURL: baseURL + "/download/v" + rel.version + "/" + name,
		})
	}

	return ghRelease{
		TagName:    "v" + rel.version,
		Prerelease: rel.prerelease,
		Draft:      false,
		Assets:     assets,
	}
}

// writeJSON marshals v, sets a content-hash ETag, and honors
// If-None-Match with a 304.
func writeJSON(w http.ResponseWriter, r *http.Request, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`

	if inm := r.Header.Get("If-None-Match"); inm != "" && inm == etag {
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// BuildAssets builds one platform's release assets: the archive (tar.gz on
// every platform except windows, which uses zip), checksums.txt and its
// checksums.txt.sig signature. The archive contains a single file, "ajent"
// (or "ajent.exe" on windows), holding binary. The returned map's keys use
// the GoReleaser-compatible name
// "ajent_<version>_<goos>_<goarch>.<ext>" for the archive, plus
// "checksums.txt" and "checksums.txt.sig".
func BuildAssets(t testing.TB, version, goos, goarch string, binary []byte, key ed25519.PrivateKey) map[string][]byte {
	t.Helper()

	binName := "ajent"
	ext := "tar.gz"
	var archive []byte
	var err error
	if goos == "windows" {
		binName = "ajent.exe"
		ext = "zip"
		archive, err = buildZip(binName, binary)
	} else {
		archive, err = buildTarGz(binName, binary)
	}
	if err != nil {
		t.Fatalf("fakerelease: build archive: %v", err)
	}

	archiveName := fmt.Sprintf("ajent_%s_%s_%s.%s", version, goos, goarch, ext)

	sum := sha256.Sum256(archive)
	checksums := hex.EncodeToString(sum[:]) + "  " + archiveName + "\n"

	sig := ed25519.Sign(key, []byte(checksums))
	sigFile := base64.StdEncoding.EncodeToString(sig) + "\n"

	return map[string][]byte{
		archiveName:         archive,
		"checksums.txt":     []byte(checksums),
		"checksums.txt.sig": []byte(sigFile),
	}
}

func buildTarGz(name string, content []byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	if err := tw.WriteHeader(&tar.Header{
		Name: name,
		Mode: 0o755,
		Size: int64(len(content)),
	}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(content); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func buildZip(name string, content []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	fw, err := zw.Create(name)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(content); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
