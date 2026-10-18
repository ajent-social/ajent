// SPDX-License-Identifier: Apache-2.0

// Command openapifilter is a maintainer script. It regenerates
// docs/api/openapi.json from the public OpenAPI description ajent.social
// serves, filtered down to the agent routes in testdata/contract/routes.json
// (docs/adr/008-open-source-boundary.md). Unit tests never invoke it; they
// call Filter directly against checked-in fixtures.
//
// Usage:
//
//	go run ./internal/tools/openapifilter [-src URL-or-file] [-routes testdata/contract/routes.json] [-out docs/api/openapi.json]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// openAPIFilterMaxSourceBytes refuses a fetched description over 4 MiB.
const openAPIFilterMaxSourceBytes = 4 * 1024 * 1024

// openAPIFilterFetchTimeout bounds the network fetch of the public
// description.
const openAPIFilterFetchTimeout = 15 * time.Second

func main() {
	if err := openAPIFilterMain(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "openapifilter:", err)
		os.Exit(1)
	}
}

func openAPIFilterMain(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("openapifilter", flag.ContinueOnError)
	src := fs.String("src", "https://ajent.social/openapi.json", "source OpenAPI description: a URL or a local file path")
	routesPath := fs.String("routes", "testdata/contract/routes.json", "path to the agent route list (routes.json shape)")
	out := fs.String("out", "docs/api/openapi.json", "output path for the filtered OpenAPI description")
	if err := fs.Parse(args); err != nil {
		return err
	}

	routes, err := openAPIFilterLoadRoutes(*routesPath)
	if err != nil {
		return err
	}

	source, err := openAPIFilterLoadSource(*src)
	if err != nil {
		return err
	}

	filtered, err := Filter(source, routes)
	if err != nil {
		return err
	}

	// #nosec G306 -- git tracks only the executable bit; docs/api/openapi.json is a public, non-executable file.
	if err := os.WriteFile(*out, filtered, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", *out, err)
	}
	if _, err := fmt.Fprintf(stdout, "wrote %s\n", *out); err != nil {
		return fmt.Errorf("write status line: %w", err)
	}
	return nil
}

// openAPIFilterRouteFixture is the on-disk shape of one testdata/contract/routes.json entry.
type openAPIFilterRouteFixture struct {
	Method  string `json:"method"`
	Pattern string `json:"pattern"`
}

func openAPIFilterLoadRoutes(path string) ([]Route, error) {
	// #nosec G304 -- path is a maintainer-supplied -routes flag, not untrusted input.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read routes %s: %w", path, err)
	}
	var fixtures []openAPIFilterRouteFixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		return nil, fmt.Errorf("decode routes %s: %w", path, err)
	}
	routes := make([]Route, 0, len(fixtures))
	for _, f := range fixtures {
		routes = append(routes, Route(f))
	}
	return routes, nil
}

func openAPIFilterLoadSource(src string) ([]byte, error) {
	if !strings.HasPrefix(src, "http://") && !strings.HasPrefix(src, "https://") {
		// #nosec G304 -- src is a maintainer-supplied -src flag, not untrusted input.
		data, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("read source %s: %w", src, err)
		}
		return data, nil
	}
	return openAPIFilterFetch(src)
}

func openAPIFilterFetch(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), openAPIFilterFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", url, err)
	}

	client := &http.Client{
		Timeout: openAPIFilterFetchTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: unexpected status %s", url, resp.Status)
	}

	limited := io.LimitReader(resp.Body, openAPIFilterMaxSourceBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read response from %s: %w", url, err)
	}
	if len(data) > openAPIFilterMaxSourceBytes {
		return nil, fmt.Errorf("fetch %s: response exceeds %d bytes", url, openAPIFilterMaxSourceBytes)
	}
	return data, nil
}
