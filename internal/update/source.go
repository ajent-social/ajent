// SPDX-License-Identifier: Apache-2.0

package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ajent-social/ajent/internal/config"
	"github.com/ajent-social/ajent/internal/version"
)

// DefaultGitHubAPI is the GitHub REST API base for the ajent repository.
const DefaultGitHubAPI = "https://api.github.com/repos/ajent-social/ajent"

// DefaultFallbackURL serves the same release information as
// DefaultGitHubAPI's "latest release", for use when GitHub is unreachable
// or rate-limited.
const DefaultFallbackURL = "https://pkg.ajent.social/latest.json"

// ErrUnavailable wraps every error Source returns because release
// information could not be obtained at all (network failure or timeout).
// Callers treat it as "no update information", not a hard failure.
var ErrUnavailable = errors.New("update: release information unavailable")

// Release is one published ajent version, as reported by the GitHub
// Releases API or the fallback JSON.
type Release struct {
	Version    string            `json:"version"` // without a leading "v"
	Prerelease bool              `json:"prerelease"`
	Assets     map[string]string `json:"assets"` // file name -> download URL
}

// Source discovers releases from the GitHub Releases API, with an ETag
// cache and a fallback URL for when GitHub is unreachable.
type Source struct {
	HTTP        *http.Client
	GitHubAPI   string
	FallbackURL string
	CacheDir    string
}

// DefaultSource returns a Source that talks to the real GitHub API and
// pkg.ajent.social, caching in configDir.
//
// GitHubAPI and FallbackURL honor the AJENT_UPDATE_API and
// AJENT_UPDATE_FALLBACK_URL environment variables when set. These exist so
// tests and self-hosted mirrors can point the client elsewhere; they do not
// weaken verification, because every release is still checked against the
// embedded Ed25519 keys (T5.3) regardless of where its bytes came from.
func DefaultSource(configDir string) *Source {
	api := DefaultGitHubAPI
	if v := os.Getenv("AJENT_UPDATE_API"); v != "" {
		api = v
	}
	fallback := DefaultFallbackURL
	if v := os.Getenv("AJENT_UPDATE_FALLBACK_URL"); v != "" {
		fallback = v
	}
	return &Source{
		HTTP:        &http.Client{Timeout: 3 * time.Second},
		GitHubAPI:   api,
		FallbackURL: fallback,
		CacheDir:    configDir,
	}
}

// CheckAllowed reports whether ajent may check for updates at all. It
// returns false when AJENT_NO_UPDATE_CHECK or CI is non-empty (ADR 005:
// automatic updates are always off in CI).
func CheckAllowed(getenv func(string) string) bool {
	if getenv("AJENT_NO_UPDATE_CHECK") != "" {
		return false
	}
	if getenv("CI") != "" {
		return false
	}
	return true
}

// ghAsset is one GitHub release asset.
type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// ghRelease is the subset of the GitHub Releases API release object that
// Source needs.
type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Prerelease bool      `json:"prerelease"`
	Draft      bool      `json:"draft"`
	Assets     []ghAsset `json:"assets"`
}

// toRelease converts g to a Release, stripping the leading "v" from the
// tag. It reports false when the resulting version is not Valid.
func (g ghRelease) toRelease() (Release, bool) {
	v := strings.TrimPrefix(g.TagName, "v")
	if !Valid(v) {
		return Release{}, false
	}

	assets := make(map[string]string, len(g.Assets))
	for _, a := range g.Assets {
		assets[a.Name] = a.BrowserDownloadURL
	}

	return Release{Version: v, Prerelease: g.Prerelease, Assets: assets}, true
}

// Latest returns the newest release. When pre is false it fetches
// {GitHubAPI}/releases/latest and falls back to FallbackURL on rate
// limiting or a server error. When pre is true it lists
// {GitHubAPI}/releases and returns the release with the greatest version
// (by Newer), including prereleases and ignoring drafts; there is no
// fallback for prereleases.
func (s *Source) Latest(ctx context.Context, pre bool) (Release, error) {
	if pre {
		return s.latestPre(ctx)
	}
	return s.latestStable(ctx)
}

func (s *Source) latestStable(ctx context.Context) (Release, error) {
	url := s.GitHubAPI + "/releases/latest"
	body, status, err := s.fetch(ctx, url)
	if err != nil {
		return Release{}, err
	}

	if status == http.StatusOK {
		var g ghRelease
		if err := json.Unmarshal(body, &g); err != nil {
			return Release{}, fmt.Errorf("update: decode %s: %w", url, err)
		}
		rel, ok := g.toRelease()
		if !ok {
			return Release{}, fmt.Errorf("update: %s: invalid release version %q", url, g.TagName)
		}
		return rel, nil
	}

	if rateLimitedOrServerError(status) {
		return s.fallback(ctx)
	}
	return Release{}, fmt.Errorf("update: %s: unexpected status %d", url, status)
}

func (s *Source) latestPre(ctx context.Context) (Release, error) {
	url := s.GitHubAPI + "/releases?per_page=20"
	body, status, err := s.fetch(ctx, url)
	if err != nil {
		return Release{}, err
	}
	if status != http.StatusOK {
		return Release{}, fmt.Errorf("update: %s: unexpected status %d", url, status)
	}

	var list []ghRelease
	if err := json.Unmarshal(body, &list); err != nil {
		return Release{}, fmt.Errorf("update: decode %s: %w", url, err)
	}

	var best Release
	found := false
	for _, g := range list {
		if g.Draft {
			continue
		}
		rel, ok := g.toRelease()
		if !ok {
			continue
		}
		if !found || Newer(rel.Version, best.Version) {
			best = rel
			found = true
		}
	}
	if !found {
		return Release{}, fmt.Errorf("update: %s: no releases found", url)
	}
	return best, nil
}

// fallback fetches FallbackURL and decodes it directly into Release; the
// fallback JSON's extra "tag" field is ignored.
func (s *Source) fallback(ctx context.Context) (Release, error) {
	body, status, err := s.fetch(ctx, s.FallbackURL)
	if err != nil {
		return Release{}, err
	}
	if status != http.StatusOK {
		return Release{}, fmt.Errorf("update: %s: unexpected status %d", s.FallbackURL, status)
	}

	var rel Release
	if err := json.Unmarshal(body, &rel); err != nil {
		return Release{}, fmt.Errorf("update: decode %s: %w", s.FallbackURL, err)
	}
	return rel, nil
}

// Get fetches the release tagged v<version>. It uses the same ETag cache
// and network-error rules as Latest, but never falls back to FallbackURL.
func (s *Source) Get(ctx context.Context, version string) (Release, error) {
	url := s.GitHubAPI + "/releases/tags/v" + version
	body, status, err := s.fetch(ctx, url)
	if err != nil {
		return Release{}, err
	}
	if status != http.StatusOK {
		return Release{}, fmt.Errorf("update: %s: unexpected status %d", url, status)
	}

	var g ghRelease
	if err := json.Unmarshal(body, &g); err != nil {
		return Release{}, fmt.Errorf("update: decode %s: %w", url, err)
	}
	rel, ok := g.toRelease()
	if !ok {
		return Release{}, fmt.Errorf("update: %s: invalid release version %q", url, g.TagName)
	}
	return rel, nil
}

// rateLimitedOrServerError reports whether status is a GitHub rate-limit
// response (403, 429) or any 5xx server error.
func rateLimitedOrServerError(status int) bool {
	if status == http.StatusForbidden || status == http.StatusTooManyRequests {
		return true
	}
	return status >= 500 && status < 600
}

// cacheEntry is one entry of the release-cache.json ETag cache.
type cacheEntry struct {
	ETag string          `json:"etag"`
	Body json.RawMessage `json:"body"`
}

func (s *Source) cachePath() string {
	return filepath.Join(s.CacheDir, "release-cache.json")
}

func (s *Source) loadCache() map[string]cacheEntry {
	cache := map[string]cacheEntry{}
	if s.CacheDir == "" {
		return cache
	}
	// A missing or corrupt cache is not an error: it just means every
	// request starts uncached.
	_ = config.ReadJSON(s.cachePath(), &cache)
	if cache == nil {
		cache = map[string]cacheEntry{}
	}
	return cache
}

func (s *Source) saveCache(cache map[string]cacheEntry) {
	if s.CacheDir == "" {
		return
	}
	// Caching is a performance optimization; a write failure must not fail
	// the request that triggered it.
	_ = config.WriteJSON(s.cachePath(), cache)
}

// fetch performs one GET request to url, honoring and updating the ETag
// cache keyed by the exact url. It returns the response body (or the
// cached body on a 304) and the effective HTTP status (a 304 is reported
// back to the caller as 200, since the cached body is authoritative).
//
// A network error or context deadline is reported as an error wrapping
// ErrUnavailable.
func (s *Source) fetch(ctx context.Context, url string) ([]byte, int, error) {
	cache := s.loadCache()
	entry, cached := cache[url]

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("update: %s: %w: %w", url, ErrUnavailable, err)
	}
	req.Header.Set("User-Agent", version.UserAgent())
	req.Header.Set("Accept", "application/vnd.github+json")
	if cached && entry.ETag != "" {
		req.Header.Set("If-None-Match", entry.ETag)
	}

	client := s.HTTP
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("update: %s: %w: %w", url, ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified && cached {
		return entry.Body, http.StatusOK, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("update: %s: %w: %w", url, ErrUnavailable, err)
	}

	if resp.StatusCode == http.StatusOK {
		if etag := resp.Header.Get("ETag"); etag != "" {
			cache[url] = cacheEntry{ETag: etag, Body: json.RawMessage(body)}
			s.saveCache(cache)
		}
	}

	return body, resp.StatusCode, nil
}
